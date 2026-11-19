//go:build windows

// Package cloud starts and stops one hlab cloud inside hlabd: settings,
// host checks, state, switch, appliance, host networking, agent document,
// the Hetzner API on the transit address and the management pipe.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/agentclient"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/hostnet"
	hnwin "github.com/webcodingcat/hlab/internal/hostnet/windows"
	"github.com/webcodingcat/hlab/internal/images"
	"github.com/webcodingcat/hlab/internal/metrics"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/hyperv"
	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/resource"
	actionsres "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/datacenters"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/isos"
	"github.com/webcodingcat/hlab/internal/resources/locations"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/resources/pricing"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/servertypes"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/settings"
	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/sysint"
	syswin "github.com/webcodingcat/hlab/internal/sysint/windows"
	"github.com/webcodingcat/hlab/internal/version"
)

// Transit network between host and appliance (ARCHITECTURE.md "Network").
var (
	hostAddr      = netip.MustParsePrefix("198.19.0.2/30")
	applianceAddr = netip.MustParseAddr("198.19.0.1")
	apiListen     = netip.MustParseAddrPort("198.19.0.2:8780")
	apiProxy      = netip.MustParseAddrPort("198.19.0.1:80")
	agentAddr     = "198.19.0.1:9100"
)

// The public segment's private VLAN (DECISIONS.md "Guest-visible identifiers").
const (
	pvlanPrimary   = 4094
	pvlanSecondary = 4093
)

const (
	// agentReadyTimeout bounds the wait for the agent after the appliance
	// starts (boot, first-boot cloud-init, agent install).
	agentReadyTimeout = 10 * time.Minute
	// listenTimeout bounds the wait until the host address is usable.
	listenTimeout = time.Minute
	// serverShutdownTimeout bounds the graceful end of the HTTP servers.
	serverShutdownTimeout = 10 * time.Second
	// statusFile is the cloud's state for hlab and the tray.
	statusFile = "status.json"
)

// Deps are what a Cloud needs from the program that runs it.
type Deps struct {
	// Paths are the folders of the current profile.
	Paths sysint.Paths
	// Level is set to logging.level of the settings at start.
	Level *slog.LevelVar
}

// Cloud is one cloud of the current profile. Start and Stop are called one
// after another, never at the same time.
type Cloud struct {
	d   Deps
	tag string

	set      settings.Settings
	net      *hnwin.HostNet
	netCfg   hostnet.Config
	problems []hostnet.Problem // non-fatal problems of Check
	netUp    bool

	st     *store.Store
	plat   platform.Platform
	cat    *catalog.Catalog
	images *images.Store
	agent  *agentclient.Client
	acts   *actions.Manager
	mods   []resource.Module
	// pushMu keeps agent documents in the order they were built.
	pushMu sync.Mutex
	// jobMu runs one appliance job at a time.
	jobMu sync.Mutex

	applianceOn bool
	apiSrv      *http.Server
	mgmtSrv     *http.Server
	// stopJobs cancels the scheduled jobs and waits until they returned.
	stopJobs func()
}

// New returns a stopped cloud.
func New(d Deps) *Cloud {
	return &Cloud{d: d, tag: profile.Current().Tag}
}

// problemCustomServerTypes is the /status problem of own server types
// that LoadAll could not add or a catalog update dropped.
const problemCustomServerTypes = "custom_server_types"

// status is the content of the status file.
type status struct {
	State   string   `json:"state"` // "starting", "running", "refused", "stopped"
	Problem *problem `json:"problem,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// problem is a problem of hostnet.Check in the status file.
type problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// refusal is a fatal problem of hostnet.Check.
type refusal struct{ p hostnet.Problem }

func (r *refusal) Error() string { return "refused: " + r.p.Code + ": " + r.p.Message }

// Start brings the cloud up. On failure it undoes what it did, writes the
// status file and returns the error.
func (c *Cloud) Start(ctx context.Context) error {
	c.writeStatus(status{State: "starting"})
	err := c.start(ctx)
	if err == nil {
		c.writeStatus(status{State: sysint.StateRunning})
		slog.Info("cloud running")
		return nil
	}
	var r *refusal
	if errors.As(err, &r) {
		slog.Error("cloud start refused", "code", r.p.Code, "message", r.p.Message)
		c.writeStatus(status{State: "refused", Problem: &problem{Code: r.p.Code, Message: r.p.Message, Fatal: r.p.Fatal}})
		return err
	}
	slog.Error("cloud start failed", "err", err)
	if terr := c.teardown(context.WithoutCancel(ctx), sysint.StopRequested); terr != nil {
		slog.Error("cleanup after the failed start", "err", terr)
	}
	c.writeStatus(status{State: sysint.StateStopped, Error: err.Error()})
	return err
}

func (c *Cloud) start(ctx context.Context) error {
	set, err := settings.Load(filepath.Join(c.d.Paths.Config, "settings.toml"))
	if err != nil {
		return err
	}
	c.set = set
	var l slog.Level
	if err := l.UnmarshalText([]byte(set.Logging.Level)); err == nil {
		c.d.Level.Set(l)
	}
	slog.Info("cloud starting", "version", version.Version, "profile", profile.Current().Name,
		"public_range", set.Network.PublicRange.String(), "data_folder", set.Storage.DataFolder)

	zones := []string{"hetzner.internal"}
	zones = append(zones, agentapi.ReverseZones(set.Network.PublicRange)...)
	c.netCfg = hostnet.Config{
		PublicRange:   set.Network.PublicRange,
		HostAddr:      hostAddr,
		ApplianceAddr: applianceAddr,
		SwitchName:    c.tag,
		BaseZones:     zones,
	}
	c.net = hnwin.New(hnwin.Options{StateDir: c.d.Paths.State})
	c.problems = nil
	for _, p := range c.net.Check(ctx, c.netCfg) {
		if p.Fatal {
			return &refusal{p}
		}
		slog.Warn("host problem", "code", p.Code, "message", p.Message)
		c.problems = append(c.problems, p)
	}

	if err := os.MkdirAll(set.Storage.DataFolder, 0o750); err != nil {
		return err
	}
	if c.st, err = store.Open(filepath.Join(c.d.Paths.State, "state.db")); err != nil {
		return err
	}
	var catWarns []string
	c.cat, catWarns, err = catalog.LoadAll(c.d.Paths.Catalog, c.d.Paths.Config,
		filepath.Join(c.d.Paths.Config, "server-types.custom.json"))
	if err != nil {
		return err
	}
	for _, w := range catWarns {
		slog.Warn("catalog problem", "message", w)
		c.problems = append(c.problems, hostnet.Problem{Code: problemCustomServerTypes, Message: w})
	}
	if c.plat, err = hyperv.New(hyperv.Options{DataDir: set.Storage.DataFolder, SwitchName: c.tag}); err != nil {
		return err
	}
	conv, ok := c.plat.(interface {
		ConvertToDynamic(ctx context.Context, src, dst string) error
	})
	if !ok {
		return errors.New("platform cannot convert images")
	}
	c.images = images.New(images.Options{
		Dir:           filepath.Join(set.Storage.DataFolder, "images"),
		Catalog:       c.cat.ImageSources(),
		Convert:       conv.ConvertToDynamic,
		ISOs:          c.cat.ISOSources(),
		ISODir:        filepath.Join(set.Storage.DataFolder, "isos"),
		RescueImage:   applianceImage,
		PrepareRescue: c.prepareRescue,
	})

	if err := c.plat.EnsureSwitch(ctx, platform.SwitchSpec{Name: c.tag, PVLANPrimary: pvlanPrimary, PVLANSecondary: pvlanSecondary}); err != nil {
		return err
	}
	secret, err := c.agentSecret()
	if err != nil {
		return err
	}
	if err := c.startAppliance(ctx, secret); err != nil {
		return err
	}

	c.netUp = true // Up may stop half way; Down removes what it added
	if err := c.net.Up(ctx, c.netCfg); err != nil {
		return err
	}

	c.acts = actions.New(c.st)
	c.mods = c.modules()
	if err := recoverModules(ctx, c.mods); err != nil {
		return err
	}
	router := api.New(api.Deps{Store: c.st, Actions: c.acts})
	for _, m := range c.mods {
		router.Mount(m)
	}
	if err := c.serveAPI(ctx, router); err != nil {
		return err
	}

	c.agent = agentclient.New(agentclient.Options{Addr: agentAddr, Secret: secret, Store: c.st})
	readyCtx, cancel := context.WithTimeout(ctx, agentReadyTimeout)
	err = c.agent.WaitReady(readyCtx)
	cancel()
	if err != nil {
		return err
	}
	if err := c.PushAgent(ctx); err != nil {
		return err
	}

	if err := c.serveManagement(router); err != nil {
		return err
	}
	c.startJobs(ctx)
	return nil
}

// startJobs runs the jobs of every module implementing resource.Scheduled
// until stopJobs is called.
func (c *Cloud) startJobs(ctx context.Context) {
	var jobs []scheduler.Job
	for _, m := range c.mods {
		if s, ok := m.(resource.Scheduled); ok {
			jobs = append(jobs, s.Jobs()...)
		}
	}
	jctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		scheduler.Run(jctx, scheduler.RealClock(), jobs)
		close(done)
	}()
	c.stopJobs = func() {
		cancel()
		<-done
	}
}

// rescueSeedHold covers the first boot of the rescue system up to
// cloud-init having read its seed.
const rescueSeedHold = 90 * time.Second

// metricsRetention is how long the samples of the servers' VMs are kept.
const metricsRetention = 24 * time.Hour

// modules are the resource modules of the Hetzner API. A new resource type
// is one more entry here; a module implementing resource.NetContributor
// adds its part to the agent document.
func (c *Cloud) modules() []resource.Module {
	srv := servers.New(servers.Deps{
		Catalog:     c.cat,
		PublicRange: c.set.Network.PublicRange,
		Store:       c.st,
		Platform:    c.plat,
		Images:      c.images,
		PushAgent:   c.PushAgent,
		AgentStatus: func(ctx context.Context) (agentapi.Status, error) { return c.agent.Status(ctx) },
		Metrics:     metrics.NewStore(metricsRetention),
		Settings: servers.Settings{
			DataFolder:    c.set.Storage.DataFolder,
			StopBehaviour: c.set.Cloud.StopBehaviour,
			ReserveRAMMB:  int64(c.set.Resources.ReserveRAMMB),
			ReserveDiskGB: int64(c.set.Resources.ReserveDiskGB),
			VMPrefix:      c.tag,
		},
		ExtraDisks:     volumeDisks,
		RescueSeedHold: rescueSeedHold,
	})
	vol := volumes.New(volumes.Deps{
		Catalog:      c.cat,
		Store:        c.st,
		Platform:     c.plat,
		DataFolder:   c.set.Storage.DataFolder,
		VMPrefix:     c.tag,
		CheckDisk:    srv.CheckDisk,
		ApplianceJob: c.applianceJob,
		PushAgent:    c.PushAgent,
	})
	srv.SetVolumes(vol)
	// The servers module comes before volumes: volumes adds to the
	// metadata the servers module writes into the agent document.
	return []resource.Module{
		actionsres.New(),
		locations.New(c.cat),
		datacenters.New(),
		servertypes.New(c.cat),
		pricing.New(c.cat),
		imagesres.NewModule(imagesres.Deps{Catalog: c.cat, DataFolder: c.set.Storage.DataFolder, Store: c.st}),
		isos.New(c.cat),
		sshkeys.New(),
		placementgroups.New(),
		srv,
		vol,
	}
}

// volumeDisks are the volume disks for the servers' disk rule.
func volumeDisks(tx store.Tx) ([]servers.DiskUse, error) {
	disks, err := volumes.Disks(tx)
	out := make([]servers.DiskUse, 0, len(disks))
	for _, d := range disks {
		out = append(out, servers.DiskUse{Path: d.Path, NominalBytes: d.NominalBytes})
	}
	return out, err
}

// PushAgent rebuilds the full agent document from the store and pushes it.
// Every state change that touches the network calls it.
func (c *Cloud) PushAgent(ctx context.Context) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	doc := agentapi.Config{
		PublicRange: c.set.Network.PublicRange,
		API:         agentapi.APIProxy{Listen: apiProxy, Backend: apiListen},
	}
	dns, err := c.net.DNSServers(ctx)
	if err != nil {
		slog.Warn("host DNS servers unknown; the agent uses its defaults", "err", err)
	}
	for _, a := range dns {
		doc.Upstream = append(doc.Upstream, netip.AddrPortFrom(a, 53))
	}
	err = c.st.Tx(func(tx store.Tx) error {
		for _, m := range c.mods {
			if nc, ok := m.(resource.NetContributor); ok {
				if err := nc.Contribute(tx, &doc); err != nil {
					return fmt.Errorf("%s: %w", m.Type(), err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("build agent document: %w", err)
	}
	if err := c.agent.Push(ctx, doc); err != nil {
		return err
	}
	slog.Info("agent document pushed", "servers", len(doc.Servers), "upstream", len(doc.Upstream))
	return nil
}

// serveAPI serves the Hetzner API on the transit address. The address may
// still be tentative right after hostnet.Up, so listening is retried.
func (c *Cloud) serveAPI(ctx context.Context, h http.Handler) error {
	var ln net.Listener
	deadline := time.Now().Add(listenTimeout)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		var err error
		ln, err = net.Listen("tcp4", apiListen.String())
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("listen on %s: %w", apiListen, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	c.apiSrv = &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go serve(c.apiSrv, ln, "api")
	slog.Info("API listening", "addr", apiListen.String())
	return nil
}

// serveManagement serves the management API on the pipe of the profile.
func (c *Cloud) serveManagement(apiHandler http.Handler) error {
	ln, err := syswin.ListenManagement()
	if err != nil {
		return fmt.Errorf("listen on the management pipe: %w", err)
	}
	deleters := map[string]resource.Deleter{}
	for _, m := range c.mods {
		if d, ok := m.(resource.Deleter); ok {
			deleters[m.Type()] = d // the store kind of every module is its type
		}
	}
	h := mgmt.New(mgmt.Deps{
		Store:     c.st,
		API:       apiHandler,
		Status:    c.mgmtStatus,
		Actions:   c.acts,
		Deleters:  deleters,
		PushAgent: c.PushAgent,
		UpdateCatalog: func(ctx context.Context, hetznerToken string) error {
			return catalog.Update(ctx, c.cat, catalog.UpdateOptions{DataDir: c.d.Paths.Config}, hetznerToken)
		},
		ServerTypes: func() int { return len(c.cat.Types()) },
	})
	c.mgmtSrv = &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go serve(c.mgmtSrv, ln, "management")
	return nil
}

func serve(srv *http.Server, ln net.Listener, name string) {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server ended", "server", name, "err", err)
	}
}

func (c *Cloud) mgmtStatus() mgmt.Status {
	st := mgmt.Status{State: sysint.StateRunning, Version: version.Version}
	for _, p := range c.problems {
		st.Problems = append(st.Problems, mgmt.Problem{Code: p.Code, Message: p.Message})
	}
	if w := c.cat.OwnTypesWarning(); w != "" {
		st.Problems = append(st.Problems, mgmt.Problem{Code: problemCustomServerTypes, Message: w})
	}
	return st
}

// Stop takes the cloud down. At a host shutdown Hyper-V has already
// applied the VMs' stop action, so the appliance is left alone.
func (c *Cloud) Stop(ctx context.Context, reason sysint.StopReason) error {
	slog.Info("cloud stopping", "reason", reason.String())
	err := c.teardown(ctx, reason)
	c.writeStatus(status{State: sysint.StateStopped})
	if err != nil {
		slog.Error("cloud stopped with errors", "err", err)
		return err
	}
	slog.Info("cloud stopped")
	return nil
}

// teardown undoes every part of start that ran, in reverse order, and
// returns the joined errors.
func (c *Cloud) teardown(ctx context.Context, reason sysint.StopReason) error {
	var errs []error
	if c.stopJobs != nil {
		c.stopJobs()
		c.stopJobs = nil
	}
	for _, srv := range []*http.Server{c.mgmtSrv, c.apiSrv} {
		if srv == nil {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, serverShutdownTimeout)
		if err := srv.Shutdown(sctx); err != nil {
			_ = srv.Close()
		}
		cancel()
	}
	c.mgmtSrv, c.apiSrv = nil, nil

	if c.applianceOn && reason != sysint.StopHostShutdown {
		if err := c.stopAppliance(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	c.applianceOn = false

	if c.netUp {
		if err := c.net.Down(ctx); err != nil {
			errs = append(errs, err)
		}
		c.netUp = false
	}
	if cl, ok := c.plat.(interface{ Close() error }); ok {
		_ = cl.Close()
	}
	c.plat = nil
	if c.st != nil {
		if err := c.st.Close(); err != nil {
			errs = append(errs, err)
		}
		c.st = nil
	}
	return errors.Join(errs...)
}

// writeStatus replaces the status file; a failure is only logged.
func (c *Cloud) writeStatus(s status) {
	data, err := json.Marshal(s)
	if err == nil {
		path := filepath.Join(c.d.Paths.State, statusFile)
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, data, 0o644); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		slog.Error("write status file", "err", err)
	}
}
