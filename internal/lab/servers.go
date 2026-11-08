package lab

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/seed"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// The folders of the servers' files in the data folder.
const (
	ServerDiskDir = "disks/servers" // <id>.vhdx: a server's differencing disk
	SeedDir       = "disks/seeds"   // <id>.iso: a server's cloud-init seed
	ImageCacheDir = "images/cache"  // <sha256>.vhdx: the system images
)

// ErrNoRoom reports that the host has no room for one more running server:
// its memory is static, so Hyper-V would not start it. The API answers it
// with Hetzner's 412 resource_unavailable.
var ErrNoRoom = errors.New("the host has no room for the server")

// The bounds of the room the servers may take: memory is
// not overcommitted, the host keeps a reserve and the appliance its own;
// virtual cores may be four times the host's threads.
const (
	memoryReservePercent = 25
	memoryReserveMinMB   = 6 * 1024
	coresPerThread       = 4
	// a new server's disk grows on the data folder's drive, which keeps
	// this share free
	diskReservePercent = 10
)

// serverWatchInterval is how often the watchdog looks at the servers' VMs.
const serverWatchInterval = 2 * time.Second

// rebootTimeout bounds how long a reboot waits for the guest to shut down
// before it turns the VM off.
const rebootTimeout = time.Minute

// CheckDisk fails with ErrNoRoom if the data folder's drive has less than
// diskReservePercent of its size free. It counts the space free now, not
// the servers' disks at their full size: dynamic disks take only what the
// guests wrote, so the disks may be larger together than the drive. It
// calls the system, so the API calls it before its Change.
func (l *Lab) CheckDisk(ctx context.Context) error {
	space, err := l.system.DiskSpace(ctx, l.dataDir)
	if err != nil {
		return err
	}
	if space.FreeMB*100 < space.TotalMB*diskReservePercent {
		return fmt.Errorf("%w: %d MB of %d MB free on the data folder's drive", ErrNoRoom, space.FreeMB, space.TotalMB)
	}
	return nil
}

// ServerResource is how actions name a server.
const ServerResource = "server"

// serverRef names a server as actions and flags do.
func serverRef(id int64) state.ResourceRef {
	return state.ResourceRef{Type: ServerResource, ID: id}
}

// ServerVM returns the name of a server's VM.
func ServerVM(id int64) string {
	return fmt.Sprintf("hlab-server-%d", id)
}

// ServerMAC returns the MAC address of a server's public NIC: 96:00 and the
// server's ID in 32 bits, like Hetzner's 96:00:… addresses. IDs count up
// from state.FirstID, so they stay far below 2^32, where MACs would repeat.
func ServerMAC(id int64) string {
	return fmt.Sprintf("96:00:%02x:%02x:%02x:%02x", byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
}

// serverFiles returns the paths of a server's disk and seed.
func (l *Lab) serverFiles(id int64) (disk, seedISO string) {
	return filepath.Join(l.dataDir, ServerDiskDir, fmt.Sprint(id)+".vhdx"), l.ServerSeed(id)
}

// ServerSeed returns the path of a server's seed ISO.
func (l *Lab) ServerSeed(id int64) string {
	return filepath.Join(l.dataDir, SeedDir, fmt.Sprint(id)+".iso")
}

// CheckRoom fails with ErrNoRoom if the servers that want to run, with one
// more of serverType, would take more than the host can give; serverID is
// the server that is to start, which the sum leaves out (0 for a new one).
// Without the machine (the API's unit tests) there is always room.
func (l *Lab) CheckRoom(tx *state.Tx, serverID int64, serverType catalog.ServerType) error {
	if l.host.MemoryMB == 0 {
		return nil
	}
	memoryMB, cores := int(serverType.Memory*1024), serverType.Cores
	for _, server := range state.All(tx, state.Servers) {
		if server.ID == serverID || !server.WantRunning {
			continue
		}
		other, found := l.catalog.ServerTypeByName(server.Type)
		if !found {
			l.logger.Warn("the catalog lacks a server's type; the room check counts the server as nothing",
				"server", server.ID, "type", server.Type)
			continue
		}
		memoryMB += int(other.Memory * 1024)
		cores += other.Cores
	}
	reserve := max(l.host.MemoryMB*memoryReservePercent/100, memoryReserveMinMB)
	if memoryMB > l.host.MemoryMB-reserve-system.ApplianceMemoryMB || cores > l.host.Threads*coresPerThread {
		return fmt.Errorf("%w: %d MB and %d cores wanted", ErrNoRoom, memoryMB, cores)
	}
	return nil
}

// setServer changes a server's record, if it is still there.
func (l *Lab) setServer(id int64, change func(server *state.Server)) error {
	return l.Change(func(tx *state.Tx) error {
		server, err := state.Get(tx, state.Servers, id)
		if err != nil {
			return err
		}
		change(server)
		return nil
	})
}

// SetServerStatus sets a server's status.
func (l *Lab) SetServerStatus(id int64, status state.ServerStatus) error {
	return l.setServer(id, func(server *state.Server) { server.Status = status })
}

// BuildServer makes a new server's VM, off: the system image in the
// cache, the disk, the seed, the VM; then the appliance gets the pictures
// with the server.
func (l *Lab) BuildServer(ctx context.Context, id int64) error {
	server, err := Current(l, state.Servers, id)
	if err != nil {
		return err
	}
	image, found := l.catalog.ImageByID(server.ImageID)
	if !found {
		return fmt.Errorf("server %d: image %d is not in the catalog", id, server.ImageID)
	}
	base, err := l.cachedImage(ctx, image)
	if err != nil {
		return err
	}
	if err := l.setServer(id, func(server *state.Server) { server.BaseImage = base }); err != nil {
		return err
	}
	if _, err := l.makeServerVM(ctx, id); err != nil {
		return err
	}
	return l.UpdatePictures(ctx)
}

// makeServerVM makes a server's VM, off, with its disk on the base image
// its record names, and its seed. A disk that is there already is kept, and
// keptDisk says so (system.CreateServer).
func (l *Lab) makeServerVM(ctx context.Context, id int64) (keptDisk bool, err error) {
	var facts picture.Server
	var server *state.Server
	err = l.View(func(tx *state.Tx) error {
		server, err = state.Get(tx, state.Servers, id)
		if err != nil {
			return err
		}
		facts = serverFacts(tx, server)
		return nil
	})
	if err != nil {
		return false, err
	}
	serverType, found := l.catalog.ServerTypeByName(server.Type)
	if !found {
		return false, fmt.Errorf("server %d: server type %s is not in the catalog", id, server.Type)
	}
	disk, seedISO := l.serverFiles(id)
	spec := system.ServerSpec{
		VM:        ServerVM(id),
		CPUs:      serverType.Cores,
		MemoryMB:  int(serverType.Memory * 1024),
		Disk:      disk,
		DiskGB:    server.DiskGB,
		BaseImage: server.BaseImage,
		Seed:      seedISO,
		SeedFiles: seed.Files(seedServer(facts, l.basePictureLab())),
		MAC:       server.MAC,
	}
	return l.system.CreateServer(ctx, spec)
}

// cachedImage returns the cached file of a system image, and loads it
// first if the cache lacks it. One load per image runs at a time; others
// wait for it. The cache trusts its record: a file deleted by hand makes
// creating disks on it fail.
func (l *Lab) cachedImage(ctx context.Context, image catalog.Image) (string, error) {
	l.mu.Lock()
	load, found := l.imageLoads[image.Name]
	if !found {
		load = &sync.Mutex{}
		l.imageLoads[image.Name] = load
	}
	l.mu.Unlock()
	load.Lock()
	defer load.Unlock()

	var cached state.CachedImage
	var inCache bool
	_ = l.View(func(tx *state.Tx) error {
		cached, inCache = tx.Lab().Images[image.Name]
		return nil
	})
	if inCache && cached.Source == image.Source.URL {
		return cached.File, nil
	}
	started := time.Now()
	l.logger.Info("loading a system image", "image", image.Name, "source", image.Source.URL)
	source := system.ImageSource{URL: image.Source.URL, Checksums: image.Source.Checksums}
	loaded, err := l.system.LoadImage(ctx, source, filepath.Join(l.dataDir, ImageCacheDir))
	if err != nil {
		return "", fmt.Errorf("loading the image %s: %w", image.Name, err)
	}
	l.logger.Info("the system image is cached", "image", image.Name, "file", loaded.File,
		"took", time.Since(started).Round(time.Second))
	err = l.Change(func(tx *state.Tx) error {
		images := tx.Lab().Images
		if images == nil {
			images = map[string]state.CachedImage{}
			tx.Lab().Images = images
		}
		images[image.Name] = state.CachedImage{Source: image.Source.URL, SHA256: loaded.SHA256, File: loaded.File, Loaded: l.Now()}
		return nil
	})
	return loaded.File, err
}

// DeleteServerVM removes what a deleted server had on the machine: its VM,
// disk and seed; then the appliance gets the pictures without it.
func (l *Lab) DeleteServerVM(ctx context.Context, id int64) error {
	disk, seedISO := l.serverFiles(id)
	if err := l.system.DeleteServer(ctx, ServerVM(id), disk, seedISO); err != nil {
		return err
	}
	return l.UpdatePictures(ctx)
}

// PowerOnServer starts a server, as Hetzner's poweron: starting, then
// running; a server that runs already stays as it is.
func (l *Lab) PowerOnServer(ctx context.Context, id int64) error {
	server, err := Current(l, state.Servers, id)
	if err != nil {
		return err
	}
	runs, err := l.system.ServerRuns(ctx, ServerVM(id))
	if err != nil {
		return err
	}
	if runs && server.Status == state.ServerRunning {
		return nil
	}
	if err := l.setServer(id, func(server *state.Server) {
		server.Status, server.WantRunning = state.ServerStarting, true
	}); err != nil {
		return err
	}
	if !runs { // a VM that runs while its status says otherwise only needs the status
		if err := l.system.PowerServer(ctx, ServerVM(id), system.PowerOn); err != nil {
			return err
		}
	}
	if err := l.Sleep(ctx, PowerOn); err != nil {
		return err
	}
	if err := l.SetServerStatus(id, state.ServerRunning); err != nil {
		return err
	}
	return l.Sleep(ctx, PowerOnEnd)
}

// PowerOffServer turns a server off at once, as Hetzner's poweroff:
// stopping, then off. It leaves want_running to the action's plan, which
// cleared it when the action was asked for: a start queued behind this one
// has set it again since.
func (l *Lab) PowerOffServer(ctx context.Context, id int64) error {
	if err := l.SetServerStatus(id, state.ServerStopping); err != nil {
		return err
	}
	if err := l.system.PowerServer(ctx, ServerVM(id), system.PowerOff); err != nil {
		return err
	}
	if err := l.Sleep(ctx, PowerOff); err != nil {
		return err
	}
	if err := l.SetServerStatus(id, state.ServerOff); err != nil {
		return err
	}
	return l.Sleep(ctx, PowerOffEnd)
}

// ShutdownServer asks the guest to shut down, as Hetzner's shutdown, which
// ends once the button is pressed: the watchdog sees the VM go off.
func (l *Lab) ShutdownServer(ctx context.Context, id int64) error {
	if err := l.system.PowerServer(ctx, ServerVM(id), system.PowerShutdown); err != nil {
		return err
	}
	return l.Sleep(ctx, Shutdown)
}

// RebootServer restarts a server through its guest: Hyper-V has no ACPI
// reboot, so the guest shuts down (turned off after a minute) and the VM
// starts again. A server that is off starts, as on Hetzner (measured
// 2026-11-08).
func (l *Lab) RebootServer(ctx context.Context, id int64) error {
	vm := ServerVM(id)
	runs, err := l.system.ServerRuns(ctx, vm)
	if err != nil {
		return err
	}
	if err := l.setServer(id, func(server *state.Server) {
		server.Status, server.WantRunning = state.ServerStarting, true
	}); err != nil {
		return err
	}
	if runs {
		if err := l.system.PowerServer(ctx, vm, system.PowerShutdown); err != nil {
			return err
		}
		off, err := l.system.WaitServerOff(ctx, vm, rebootTimeout)
		if err != nil {
			return err
		}
		if !off {
			l.logger.Warn("the guest did not shut down for its reboot; turning it off", "server", id)
			if err := l.system.PowerServer(ctx, vm, system.PowerOff); err != nil {
				return err
			}
		}
	}
	return l.finishRestart(ctx, id, system.PowerOn)
}

// ResetServer resets a server, as Hetzner's reset; one that is off starts
// (measured 2026-11-08).
func (l *Lab) ResetServer(ctx context.Context, id int64) error {
	if err := l.setServer(id, func(server *state.Server) {
		server.Status, server.WantRunning = state.ServerStarting, true
	}); err != nil {
		return err
	}
	return l.finishRestart(ctx, id, system.PowerReset)
}

// finishRestart runs the power command of a reboot or reset and waits
// Hetzner's times: starting, then running.
func (l *Lab) finishRestart(ctx context.Context, id int64, power system.Power) error {
	if err := l.system.PowerServer(ctx, ServerVM(id), power); err != nil {
		return err
	}
	if err := l.Sleep(ctx, Restart); err != nil {
		return err
	}
	if err := l.SetServerStatus(id, state.ServerRunning); err != nil {
		return err
	}
	return l.Sleep(ctx, RestartEnd)
}

// UpdatePictures sends the appliance every picture built from the state as
// it is now; without the machine (the API's unit tests) there is nothing
// to send. Updates run one at a time, so that an older picture never
// follows a newer one.
func (l *Lab) UpdatePictures(ctx context.Context) error {
	l.pictureMu.Lock()
	defer l.pictureMu.Unlock()
	l.mu.Lock()
	appliance := l.appliance
	l.mu.Unlock()
	if appliance == nil {
		return nil
	}
	return appliance.Update(ctx, l.pictures(l.pictureLab()))
}

// serverFacts returns what the pictures need of a server: its addresses
// from its Primary IPs, and whether its project reaches the Internet.
func serverFacts(tx *state.Tx, server *state.Server) picture.Server {
	facts := picture.Server{
		ID:           server.ID,
		Name:         server.Name,
		Location:     server.Location,
		MAC:          server.MAC,
		PublicKeys:   server.PublicKeys,
		RootPassword: server.RootPassword,
		UserData:     server.UserData,
	}
	if project, err := state.Get(tx, state.Projects, server.ProjectID); err == nil {
		facts.Internet = project.Internet
	}
	if ip, err := state.Get(tx, state.PrimaryIPs, server.IPv4); err == nil {
		facts.IPv4, _ = netip.ParseAddr(ip.IP)
	}
	if ip, err := state.Get(tx, state.PrimaryIPs, server.IPv6); err == nil {
		facts.IPv6, _ = netip.ParsePrefix(ip.IP)
	}
	return facts
}

// seedServer returns what a server's seed is built from.
func seedServer(facts picture.Server, lab picture.Lab) seed.Server {
	return seed.Server{
		ID:           facts.ID,
		Name:         facts.Name,
		MAC:          facts.MAC,
		IPv4:         facts.IPv4.IsValid(),
		IPv6:         facts.IPv6,
		Resolvers:    lab.ApplianceAddresses(),
		PublicKeys:   picture.ServerKeys(lab, facts),
		RootPassword: facts.RootPassword,
		UserData:     facts.UserData,
	}
}

// watchServers looks at the servers' VMs every interval until ctx ends: a
// server that runs in the record but whose VM is off has shut itself down,
// so it is off and no longer wants to run, as on Hetzner. Servers
// that an action works on are left to it.
func (l *Lab) watchServers(ctx context.Context) {
	ticker := time.NewTicker(serverWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var ids []int64
		l.mu.Lock()
		for _, server := range state.All(state.Read(l.state), state.Servers) {
			if server.Status == state.ServerRunning && l.flags[serverRef(server.ID)] == 0 {
				ids = append(ids, server.ID)
			}
		}
		l.mu.Unlock()
		for _, id := range ids {
			runs, err := l.system.ServerRuns(ctx, ServerVM(id))
			if err != nil || runs {
				continue
			}
			l.markShutDown(id)
		}
	}
}

// markShutDown sets a server off whose guest shut down, unless an action
// took it over meanwhile.
func (l *Lab) markShutDown(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.flags[serverRef(id)] > 0 {
		return
	}
	marked := false
	err := l.changeLocked(func(tx *state.Tx) error {
		server, err := state.Get(tx, state.Servers, id)
		if err != nil || server.Status != state.ServerRunning {
			return err
		}
		server.Status, server.WantRunning = state.ServerOff, false
		marked = true
		return nil
	})
	switch {
	case err != nil && !errors.Is(err, state.ErrNotFound):
		l.logger.Warn("marking a server off failed", "server", id, "error", err)
	case err == nil && marked:
		l.logger.Info("the server shut down", "server", id)
	}
}
