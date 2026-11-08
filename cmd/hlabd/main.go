// Command hlabd is the HLab service: the APIs, the lab control, the web UI,
// the state and the platform.
//
// On Windows it drives Hyper-V and the host's network, and needs
// administrator rights. `--platform fake` runs the lab on a machine in
// memory, on 127.0.0.1, on any system (docs/roadmap.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

// version is set at build time by bin/dev build (-ldflags -X main.version=...).
var version = "dev"

// fakePlatform is the name of the platform in memory.
const fakePlatform = "fake"

// applianceFile is the appliance's release disk, next to hlabd by default.
const applianceFile = "hlab-appliance.vhdx"

// loopback is where the lab control also listens, and where everything
// listens on the fake platform.
const loopback = "127.0.0.1"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	dataDir := flag.String("data", defaultDataDir, "the data folder")
	platformName := flag.String("platform", runtime.GOOS, "the platform: "+runtime.GOOS+" or "+fakePlatform)
	appliance := flag.String("appliance", "", "the appliance's release disk (default: "+applianceFile+" next to hlabd)")
	flag.Parse()
	if *showVersion {
		fmt.Println("hlabd", version)
		return
	}
	logger, closeLog, err := openLog(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hlabd: opening the log:", err)
		os.Exit(1)
	}
	err = run(*dataDir, *platformName, *appliance, logger)
	if err != nil {
		// also into the file: without a console it is the only trace
		logger.Error("hlabd stopped", "error", err)
	}
	_ = closeLog.Close()
	if err != nil {
		os.Exit(1)
	}
}

// run starts the service on the named platform and runs it until it is asked
// to stop.
func run(dataDir, platformName, appliance string, logger *slog.Logger) error {
	if appliance == "" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		appliance = filepath.Join(filepath.Dir(executable), applianceFile)
	}
	opts := serviceOptions{
		DataDir:  dataDir,
		Platform: platformName,
		Machine:  lab.MachineOptions{ApplianceRelease: appliance},
		Version:  version,
		Logger:   logger,
	}
	switch platformName {
	case fakePlatform:
		// the fake machine, with this machine's life cycle: one HLab per
		// machine, the control token for the CLI, Ctrl+C
		opts.Parts = fakeMachine(fake.New(), appliance)
		opts.Parts.Lifecycle = withAgents{Lifecycle: nativeLifecycle(), dialer: opts.Parts.Lifecycle.AgentDialer()}
		opts.Hosts = []string{loopback}
	case runtime.GOOS:
		parts, closeParts, err := nativePlatform(platformName, logger)
		if err != nil {
			return err
		}
		defer func() { _ = closeParts() }()
		opts.Parts = parts
		// the APIs on the host link, the lab control also on the loopback
		hostLink := picture.DefaultLab().HostAddresses()[0].Addr().String()
		opts.Hosts = []string{hostLink, loopback}
	default:
		return fmt.Errorf("unknown platform %s: use %s or %s", platformName, runtime.GOOS, fakePlatform)
	}
	svc, err := start(opts)
	if err != nil {
		return err
	}
	return svc.wait()
}

// fakeMachine gives a machine in memory the appliance's release disk and an
// agent in the appliance, and returns its parts.
func fakeMachine(machine *fake.Fake, appliance string) platform.Platform {
	machine.AddDisk(appliance, 4)
	machine.ServeAgent(system.ApplianceVM, fake.NewAgent().Serve)
	return machine.Platform()
}

// withAgents is a life cycle whose agents are another's: this machine's
// life cycle with the agents of the fake machine.
type withAgents struct {
	platform.Lifecycle
	dialer platform.AgentDialer
}

// AgentDialer returns the other's dialer.
func (w withAgents) AgentDialer() platform.AgentDialer {
	return w.dialer
}

// serviceOptions are what the service needs to start.
type serviceOptions struct {
	DataDir  string
	Platform string // its name, for the status
	Parts    platform.Platform
	Machine  lab.MachineOptions
	// Hosts are the addresses to listen on: the APIs and the lab control on
	// the first, the lab control also on the others.
	Hosts   []string
	Version string
	Logger  *slog.Logger
}

// stopContext returns a context that ends when the machine asks HLab to
// stop, so that Ctrl+C ends a start that waits for the appliance.
func stopContext(lifecycle platform.Lifecycle) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-lifecycle.StopRequests():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
