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
	"sync/atomic"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/agentclient"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/ca"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/console"
	"github.com/webcodingcat/hlab/internal/dnszone"
	"github.com/webcodingcat/hlab/internal/faults"
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
	"github.com/webcodingcat/hlab/internal/resources/certificates"
	"github.com/webcodingcat/hlab/internal/resources/datacenters"
	"github.com/webcodingcat/hlab/internal/resources/firewalls"
	"github.com/webcodingcat/hlab/internal/resources/floatingips"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/isos"
	"github.com/webcodingcat/hlab/internal/resources/loadbalancers"
	"github.com/webcodingcat/hlab/internal/resources/loadbalancertypes"
	"github.com/webcodingcat/hlab/internal/resources/locations"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/resources/pricing"
	"github.com/webcodingcat/hlab/internal/resources/primaryips"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/servertypes"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/resources/storageboxtypes"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/resources/zones"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/settings"
	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/sysint"
	syswin "github.com/webcodingcat/hlab/internal/sysint/windows"
	"github.com/webcodingcat/hlab/internal/transit"
	"github.com/webcodingcat/hlab/internal/version"
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
	// shadowLookupTimeout bounds the public SOA query per DNS server of the
	// shadow check of a zone.
	shadowLookupTimeout = 2 * time.Second
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
	addrs    transit.Addrs // transit addresses of set's public range
	net      *hnwin.HostNet
	netCfg   hostnet.Config
	problems []hostnet.Problem // non-fatal problems of Check
	netUp    bool

	st     *store.Store
	plat   platform.Platform
	cat    *catalog.Catalog
	images *images.Store
	agent  *agentclient.Client
	// storage talks to the storage agent; storageMu runs one apply of the
	// storage VM at a time and keeps jobs out of an apply, whose document
	// would otherwise miss a snapshot a job takes meanwhile.
	storage   *agentclient.StorageClient
	storageMu sync.Mutex
	// stopStorage ends the start's background apply of the storage VM.
	stopStorage func()
	acts        *actions.Manager
	ca          *ca.CA
	// console serves the server consoles of request_console.
	console *console.Service
	mods    []resource.Module
	// srv and vol are the servers and volumes modules of mods; the fault
	// effects act through them.
	srv *servers.Module
	vol *volumes.Module
	// faults holds the active faults of this run; nil while stopped. It is
	// read by API requests and module actions, hence atomic.
	faults atomic.Pointer[faults.Manager]
	// netEffects keeps the active net faults of this run for the agent
	// document; nil while stopped.
	netEffects atomic.Pointer[faults.NetEffects]
	// shadow keeps the primary zones that shadow a public domain.
	shadow *zones.Shadow
	// pushMu keeps agent documents in the order they were built.
	pushMu sync.Mutex
	// jobMu runs one appliance job at a time.
	jobMu sync.Mutex

	applianceOn bool
	apiSrv      *http.Server
	mgmtSrv     *http.Server
	// done is closed at the stop; it ends the log followers of the
	// management API.
	done chan struct{}
	// stopJobs cancels the scheduled jobs and waits until they returned.
	stopJobs func()
	// restored is set once Lifecycle.Restore ran; only then does the stop
	// halt the servers and record which ran.
	restored bool
}

// New returns a stopped cloud.
func New(d Deps) *Cloud {
	return &Cloud{d: d, tag: profile.Current().Tag}
}

// problemCustomServerTypes is the /status problem of own server types
// that LoadAll could not add or a catalog update dropped.
const problemCustomServerTypes = "custom_server_types"

// problemCustomStorageBoxTypes is the /status problem of own storage box
// types that are left out.
const problemCustomStorageBoxTypes = "custom_storage_box_types"

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

// Start brings the cloud up with the service's start parameters (see
// discard.Param). On failure it undoes what it did, writes the status file
// and returns the error.
func (c *Cloud) Start(ctx context.Context, params []string) error {
	c.writeStatus(status{State: "starting"})
	err := c.start(ctx, params)
	if err == nil {
		c.writeStatus(status{State: sysint.StateRunning})
		slog.Info("cloud running")
		return nil
	}
	var r *refusal
	if errors.As(err, &r) {
		slog.Error("cloud start refused", "code", r.p.Code, "message", r.p.Message)
		c.writeStatus(status{State: "refused", Problem: &problem{Code: r.p.Code, Message: r.p.Message, Fatal: r.p.Fatal}})
		if terr := c.teardown(context.WithoutCancel(ctx), sysint.StopRequested); terr != nil {
			slog.Error("cleanup after the refused start", "err", terr)
		}
		return err
	}
	slog.Error("cloud start failed", "err", err)
	if terr := c.teardown(context.WithoutCancel(ctx), sysint.StopRequested); terr != nil {
		slog.Error("cleanup after the failed start", "err", terr)
	}
	c.writeStatus(status{State: sysint.StateStopped, Error: err.Error()})
	return err
}

func (c *Cloud) start(ctx context.Context, params []string) error {
	set, err := settings.Load(filepath.Join(c.d.Paths.Config, "settings.toml"))
	if err != nil {
		return err
	}
	c.set = set
	c.addrs = transit.For(set.Network.PublicRange)
	var l slog.Level
	if err := l.UnmarshalText([]byte(set.Logging.Level)); err == nil {
		c.d.Level.Set(l)
	}
	slog.Info("cloud starting", "version", version.Version, "profile", profile.Current().Name,
		"public_range", set.Network.PublicRange.String(), "data_folder", set.Storage.DataFolder)

	base := []string{"hetzner.internal"}
	base = append(base, agentapi.ReverseZones(set.Network.PublicRange)...)
	c.netCfg = hostnet.Config{
		PublicRange:   set.Network.PublicRange,
		HostAddr:      c.addrs.Host,
		ApplianceAddr: c.addrs.Appliance,
		SwitchName:    c.tag,
		BaseZones:     base,
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
	if c.plat, err = hyperv.New(hyperv.Options{DataDir: set.Storage.DataFolder, SwitchName: c.tag}); err != nil {
		return err
	}
	if err := c.openState(ctx, params); err != nil {
		return err
	}
	if err := ensureCustomServerTypes(c.d.Paths.Config); err != nil {
		return fmt.Errorf("create the own server types file: %w", err)
	}
	if err := ensureCustomISOs(c.d.Paths.Config); err != nil {
		return fmt.Errorf("create the own ISOs file: %w", err)
	}
	if err := ensureConfigList(c.d.Paths.Config, catalog.OwnStorageBoxTypesFile); err != nil {
		return fmt.Errorf("create the own storage box types file: %w", err)
	}
	customFile := filepath.Join(c.d.Paths.Config, customServerTypesFile)
	var catWarns []string
	c.cat, catWarns, err = catalog.LoadAll(c.d.Paths.Catalog, c.d.Paths.Config, customFile)
	if err != nil {
		return err
	}
	if len(catWarns) > 0 {
		// ReloadOwn keeps the warning in the catalog, where /status reads it
		// and a reload clears it.
		c.cat.ReloadOwn(customFile)
	}
	c.cat.ReloadOwnStorageBoxTypes(filepath.Join(c.d.Paths.Config, catalog.OwnStorageBoxTypesFile))
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
		ISOSource:     c.cat.ISOSourceByCacheName,
		ISODir:        filepath.Join(set.Storage.DataFolder, "isos"),
		RescueImage:   applianceImage,
		PrepareRescue: c.prepareRescue,
	})
	c.loadOwnISOs(ctx)

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

	if err := c.openCA(ctx); err != nil {
		return err
	}
	c.acts = actions.New(c.st)
	if c.storage, err = c.newStorageClient(); err != nil {
		return err
	}
	c.shadow = zones.NewShadow(zones.PublicLookup(c.hostDNS(ctx), shadowLookupTimeout))
	if err := c.newConsole(); err != nil {
		return err
	}
	c.mods = c.modules()
	if err := recoverModules(ctx, c.mods); err != nil {
		return err
	}
	if err := c.publishZones(ctx); err != nil {
		return err
	}
	router := api.New(api.Deps{Store: c.st, Actions: c.acts, Faults: c.faultLayer})
	for _, m := range c.mods {
		router.Mount(m)
	}
	c.newFaults(router.Resources())
	if err := c.serveAPI(ctx, c.withCRL(router)); err != nil {
		return err
	}
	if err := c.serveConsole(); err != nil {
		return err
	}

	c.agent = agentclient.New(agentclient.Options{Addr: c.addrs.Agent.String(), Secret: secret, Store: c.st})
	readyCtx, cancel := context.WithTimeout(ctx, agentReadyTimeout)
	err = c.agent.WaitReady(readyCtx)
	cancel()
	if err != nil {
		return err
	}
	if err := c.PushAgent(ctx); err != nil {
		return err
	}
	c.restore(ctx)
	c.startStorage(ctx)

	if err := c.serveManagement(router); err != nil {
		return err
	}
	c.startJobs(ctx)
	return nil
}

// startJobs runs the cloud's jobs and those of every module implementing
// resource.Scheduled until stopJobs is called.
func (c *Cloud) startJobs(ctx context.Context) {
	jobs := c.cloudJobs()
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

// seedHold covers the first boot of a server up to cloud-init having read
// its seed (E48: up to 37 s), before hlab swaps the seed.
const seedHold = 90 * time.Second

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
		SeedHold:       seedHold,
		Console:        c.console,
		EndFaults:      c.endFaults,
		FaultActive:    c.faultActive,
		EndFault:       c.endFault,
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
		EndFaults:    c.endFaults,
	})
	srv.SetVolumes(vol)
	c.srv, c.vol = srv, vol
	nets := networks.New(networks.Deps{Store: c.st, Catalog: c.cat, Servers: srv, PushAgent: c.PushAgent})
	pips := primaryips.New(primaryips.Deps{
		Store: c.st, Catalog: c.cat, PublicRange: c.set.Network.PublicRange, Servers: srv, PushAgent: c.PushAgent,
	})
	fips := floatingips.New(floatingips.Deps{
		Store: c.st, Catalog: c.cat, PublicRange: c.set.Network.PublicRange, PushAgent: c.PushAgent,
	})
	srv.SetNetworks(nets)
	srv.SetPublicIPs(pips)
	srv.OnDelete(pips.ServerDeleted)
	srv.OnDelete(fips.ServerDeleted)
	srv.SetDNSPtrs(pips)
	fws := firewalls.New(firewalls.Deps{Store: c.st, PushAgent: c.PushAgent})
	srv.SetFirewalls(fws)
	srv.OnDelete(fws.ServerDeleted)
	lb := loadbalancers.New(loadbalancers.Deps{
		Store:       c.st,
		Catalog:     c.cat,
		PublicRange: c.set.Network.PublicRange,
		PushAgent:   c.PushAgent,
		AgentStatus: func(ctx context.Context) (agentapi.Status, error) { return c.agent.Status(ctx) },
		PublicIPs:   pips,
		Networks:    nets,
	})
	nets.SetLoadBalancers(lb)
	srv.OnDelete(lb.ServerDeleted)
	srv.SetLoadBalancers(lb.ForServer)
	certs := certificates.New(certificates.Deps{Store: c.st, CA: c.ca, PushAgent: c.PushAgent, UsedBy: lb.UsedBy})
	zns := zones.New(zones.Deps{Store: c.st, PushAgent: c.PushAgent, SetZones: c.net.SetZones, Shadow: c.shadow})
	// Order of the agent document: servers writes the public metadata
	// entries, networks adds the members' private entries, and volumes then
	// sets the volumes of every entry, public and private. Firewalls sets
	// the firewall IDs of the public entries after servers and floating IPs.
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
		nets,
		pips,
		fips,
		fws,
		loadbalancertypes.New(c.cat),
		certs,
		lb,
		zns,
		vol,
		storageboxtypes.New(c.cat),
		storageboxes.New(c.storageDeps()),
	}
}

// hostDNS returns the host's DNS servers on port 53; none when they are
// unknown (the agent and the shadow check then use their defaults).
func (c *Cloud) hostDNS(ctx context.Context) []netip.AddrPort {
	dns, err := c.net.DNSServers(ctx)
	if err != nil {
		slog.Warn("host DNS servers unknown", "err", err)
	}
	var out []netip.AddrPort
	for _, a := range dns {
		out = append(out, netip.AddrPortFrom(a, 53))
	}
	return out
}

// publishZones sets one NRPT rule per stored zone and starts the shadow
// check of every primary zone outside the private-use suffixes.
func (c *Cloud) publishZones(ctx context.Context) error {
	var names []string
	var primary []zones.ZoneRef
	err := c.st.Tx(func(tx store.Tx) error {
		var err error
		if names, err = zones.Names(tx); err != nil {
			return err
		}
		primary, err = zones.Primary(tx)
		return err
	})
	if err != nil {
		return fmt.Errorf("read zones: %w", err)
	}
	if err := c.net.SetZones(ctx, names); err != nil {
		return err
	}
	for _, z := range primary {
		if !dnszone.PrivateUse(z.Zone) {
			c.shadow.Check(z.Zone, z.Project)
		}
	}
	return nil
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
		API:         agentapi.APIProxy{Listen: c.addrs.APIProxy, Backend: c.addrs.APIListen},
		Console:     agentapi.ConsoleForward{Listen: c.addrs.ConsoleProxy, Backend: c.addrs.ConsoleListen},
	}
	doc.Upstream = c.hostDNS(ctx)
	err := c.st.Tx(func(tx store.Tx) error {
		for _, m := range c.mods {
			if nc, ok := m.(resource.NetContributor); ok {
				if err := nc.Contribute(tx, &doc); err != nil {
					return fmt.Errorf("%s: %w", m.Type(), err)
				}
			}
		}
		if net := c.netEffects.Load(); net != nil {
			fs, err := netFaults(tx, &doc, net.Faults())
			if err != nil {
				return fmt.Errorf("faults: %w", err)
			}
			doc.Faults = fs
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
		ln, err = net.Listen("tcp4", c.addrs.APIListen.String())
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("listen on %s: %w", c.addrs.APIListen, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	c.apiSrv = &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go serve(c.apiSrv, ln, "api")
	slog.Info("API listening", "addr", c.addrs.APIListen.String())
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
	c.done = make(chan struct{})
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
		ServerTypes:  func() int { return len(c.cat.Types()) },
		CACert:       c.ca.Certificate,
		Trust:        c.trust,
		SettingsPath: filepath.Join(c.d.Paths.Config, "settings.toml"),
		LogDir:       c.d.Paths.Logs,
		Reload:       c.reload,
		Done:         c.done,
		Faults:       c.faults.Load(),
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
	if w := c.cat.OwnStorageBoxTypesWarning(); w != "" {
		st.Problems = append(st.Problems, mgmt.Problem{Code: problemCustomStorageBoxTypes, Message: w})
	}
	for _, w := range c.cat.OwnISOsWarnings() {
		st.Problems = append(st.Problems, mgmt.Problem{Code: problemCustomISOs, Message: w})
	}
	for _, z := range c.shadow.List() {
		st.ShadowingZones = append(st.ShadowingZones, mgmt.ShadowingZone{Zone: z.Zone, Project: z.Project})
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
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
	// At a host shutdown the next cloud start undoes what is left.
	if m := c.faults.Load(); m != nil && reason != sysint.StopHostShutdown {
		if err := m.EndAll(ctx); err != nil {
			errs = append(errs, fmt.Errorf("end faults: %w", err))
		}
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
	c.faults.Store(nil)
	c.netEffects.Store(nil)
	c.closeConsole()
	if c.stopStorage != nil {
		c.stopStorage()
		c.stopStorage = nil
	}

	if c.restored {
		if err := c.halt(ctx, reason == sysint.StopHostShutdown); err != nil {
			errs = append(errs, err)
		}
		c.restored = false
	}

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
