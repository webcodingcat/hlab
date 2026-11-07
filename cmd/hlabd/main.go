// Command hlabd is the HLab service: the APIs, the lab control, the web UI,
// the state and the platform.
//
// For now only `--platform fake` runs: the lab without a hypervisor and
// without the host network, on 127.0.0.1 (docs/roadmap.md).
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
)

// version is set at build time by bin/dev build (-ldflags -X main.version=...).
var version = "dev"

// fakePlatform is the name of the platform in memory.
const fakePlatform = "fake"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	dataDir := flag.String("data", defaultDataDir, "the data folder")
	platformName := flag.String("platform", runtime.GOOS, "the platform: "+runtime.GOOS+" or "+fakePlatform)
	flag.Parse()
	if *showVersion {
		fmt.Println("hlabd", version)
		return
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*dataDir, *platformName, logger); err != nil {
		fmt.Fprintln(os.Stderr, "hlabd:", err)
		os.Exit(1)
	}
}

// run starts the service on the named platform and runs it until it is asked
// to stop.
func run(dataDir, platformName string, logger *slog.Logger) error {
	if platformName != fakePlatform {
		return fmt.Errorf("the platform %s is not yet implemented: use --platform %s", platformName, fakePlatform)
	}
	// the fake machine, with this machine's life cycle: one HLab per machine,
	// the control token for the CLI, Ctrl+C
	parts := fake.New().Platform()
	parts.Lifecycle = nativeLifecycle()
	svc, err := start(serviceOptions{
		DataDir:  dataDir,
		Platform: platformName,
		Parts:    parts,
		Host:     "127.0.0.1",
		Version:  version,
		Logger:   logger,
	})
	if err != nil {
		return err
	}
	return svc.wait()
}

// serviceOptions are what the service needs to start.
type serviceOptions struct {
	DataDir  string
	Platform string // its name, for the status
	Parts    platform.Platform
	Host     string // the address the APIs and the control listen on
	Version  string
	Logger   *slog.Logger
}
