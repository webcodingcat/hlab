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

// rebootTimeout bounds how long a reboot waits for the guest to shut down
// before it turns the VM off.
const rebootTimeout = time.Minute

// CheckDisk fails with ErrNoRoom if the data folder's drive has less than
// room.disk_free_percent of its size free (hlab.yaml). It counts the space free now, not
// the servers' disks at their full size: dynamic disks take only what the
// guests wrote, so the disks may be larger together than the drive. It
// calls the system, so the API calls it before its Change.
func (m *Machine) CheckDisk(ctx context.Context) error {
	space, err := m.system.DiskSpace(ctx, m.lab.dataDir)
	if err != nil {
		return err
	}
	if space.FreeMB*100 < space.TotalMB*int64(m.room.DiskFreePercent) {
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
func (m *Machine) serverFiles(id int64) (disk, seedISO string) {
	return filepath.Join(m.lab.dataDir, ServerDiskDir, fmt.Sprint(id)+".vhdx"), m.ServerSeed(id)
}

// ServerSeed returns the path of a server's seed ISO.
func (m *Machine) ServerSeed(id int64) string {
	return filepath.Join(m.lab.dataDir, SeedDir, fmt.Sprint(id)+".iso")
}

// CheckRoom fails with ErrNoRoom if the host cannot give the servers that
// want to run and one more server of serverType. serverID is the server
// that is to start: the sum leaves it out, so that it is not counted twice.
// A new server, which has no ID yet, passes 0.
// Before Start (the API's unit tests) there is always room.
func (m *Machine) CheckRoom(tx *state.Tx, serverID int64, serverType catalog.ServerType) error {
	if m.host.MemoryMB == 0 {
		return nil
	}
	memoryMB, cores := int(serverType.Memory*1024), serverType.Cores
	for _, server := range state.All(tx, state.Servers) {
		if server.ID == serverID || !server.WantRunning {
			continue
		}
		other, found := m.lab.catalog.ServerTypeByName(server.Type)
		if !found {
			m.lab.logger.Warn("the catalog lacks a server's type; the room check counts the server as nothing",
				"server", server.ID, "type", server.Type)
			continue
		}
		memoryMB += int(other.Memory * 1024)
		cores += other.Cores
	}
	// memory is static, so it is never overcommitted; the appliance takes its own
	reserve := max(m.host.MemoryMB*m.room.MemoryReservePercent/100, m.room.MemoryReserveMinMB)
	if memoryMB > m.host.MemoryMB-reserve-system.ApplianceMemoryMB || cores > m.host.Threads*m.room.CoresPerThread {
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

// CreateServer is the create action's steps: it builds the server
// (BuildServer); a server that is not to start is off then, one that is to start is left to the poweron queued behind.
// If it fails, it removes the VM, the disk and the seed, and the action's
// undo removes the record. If it is canceled, it leaves them: a stopping
// lab keeps the record, and its next start builds the server anew; a
// deleted project's servers go with it (DeleteProject).
// A delete queued during the create removes the record at once; the build
// then fails on the missing record and cleans up the same way, and the
// delete's steps find nothing left to delete (system.DeleteServer takes
// what is there).
func (m *Machine) CreateServer(ctx context.Context, id int64, start bool) error {
	err := m.BuildServer(ctx, id)
	switch {
	case err != nil && ctx.Err() == nil:
		return errors.Join(err, m.DeleteServer(ctx, id))
	case err != nil:
		return err
	case !start:
		return m.lab.SetServerStatus(id, state.ServerOff)
	}
	return nil
}

// BuildServer makes a new server's VM, off: the system image in the
// cache, the disk, the seed, the VM; then the appliance gets the pictures
// with the server.
func (m *Machine) BuildServer(ctx context.Context, id int64) error {
	server, err := Current(m.lab, state.Servers, id)
	if err != nil {
		return err
	}
	image, found := m.lab.catalog.ImageByID(server.ImageID)
	if !found {
		return fmt.Errorf("server %d: image %d is not in the catalog", id, server.ImageID)
	}
	base, err := m.cachedImage(ctx, image)
	if err != nil {
		return err
	}
	if err := m.lab.setServer(id, func(server *state.Server) { server.BaseImage = base }); err != nil {
		return err
	}
	if _, err := m.makeServerVM(ctx, id); err != nil {
		return err
	}
	return m.UpdatePictures(ctx)
}

// makeServerVM makes a server's VM, off, with its disk on the base image
// its record names, and its seed. A disk that is there already is kept, and
// keptDisk says so (system.CreateServer).
func (m *Machine) makeServerVM(ctx context.Context, id int64) (keptDisk bool, err error) {
	var facts picture.Server
	var server *state.Server
	var private []system.PrivateNIC
	err = m.lab.View(func(tx *state.Tx) error {
		found, getErr := state.Get(tx, state.Servers, id)
		if getErr != nil {
			return getErr
		}
		server, facts, private = found, serverFacts(tx, found), systemNICs(tx, found)
		return nil
	})
	if err != nil {
		return false, err
	}
	serverType, found := m.lab.catalog.ServerTypeByName(server.Type)
	if !found {
		return false, fmt.Errorf("server %d: server type %s is not in the catalog", id, server.Type)
	}
	disk, seedISO := m.serverFiles(id)
	spec := system.ServerSpec{
		VM:        ServerVM(id),
		CPUs:      serverType.Cores,
		MemoryMB:  int(serverType.Memory * 1024),
		Disk:      disk,
		DiskGB:    server.DiskGB,
		BaseImage: server.BaseImage,
		Seed:      seedISO,
		SeedFiles: seed.Files(seedServer(facts, m.basePictureLab())),
		MAC:       server.MAC,
		Private:   private,
	}
	return m.system.CreateServer(ctx, spec)
}

// cachedImage returns the cached file of a system image, and loads it
// first if the cache lacks it. One load per image runs at a time; others
// wait for it. The cache trusts its record: a file deleted by hand makes
// creating disks on it fail.
func (m *Machine) cachedImage(ctx context.Context, image catalog.Image) (string, error) {
	m.lab.mu.Lock()
	load, found := m.imageLoads[image.Name]
	if !found {
		load = &sync.Mutex{}
		m.imageLoads[image.Name] = load
	}
	m.lab.mu.Unlock()
	load.Lock()
	defer load.Unlock()

	var cached state.CachedImage
	var inCache bool
	_ = m.lab.View(func(tx *state.Tx) error {
		cached, inCache = tx.Lab().Images[image.Name]
		return nil
	})
	if inCache && cached.Source == image.Source.URL {
		return cached.File, nil
	}
	started := time.Now()
	m.lab.logger.Info("loading a system image", "image", image.Name, "source", image.Source.URL)
	source := system.ImageSource{URL: image.Source.URL, Checksums: image.Source.Checksums}
	loaded, err := m.system.LoadImage(ctx, source, filepath.Join(m.lab.dataDir, ImageCacheDir))
	if err != nil {
		return "", fmt.Errorf("loading the image %s: %w", image.Name, err)
	}
	m.lab.logger.Info("the system image is cached", "image", image.Name, "file", loaded.File,
		"took", time.Since(started).Round(time.Second))
	err = m.lab.Change(func(tx *state.Tx) error {
		images := tx.Lab().Images
		if images == nil {
			images = map[string]state.CachedImage{}
			tx.Lab().Images = images
		}
		images[image.Name] = state.CachedImage{Source: image.Source.URL, SHA256: loaded.SHA256, File: loaded.File, Loaded: m.lab.Now()}
		return nil
	})
	return loaded.File, err
}

// DeleteServer removes what a deleted server had on the machine: its VM,
// disk and seed; then the appliance gets the pictures without it. It is
// the delete action's steps (its plan removed the record), and a failed
// create and a project's delete call it too. The pictures go even when
// the VM's removal fails: the record is gone either way.
func (m *Machine) DeleteServer(ctx context.Context, id int64) error {
	disk, seedISO := m.serverFiles(id)
	vmErr := m.system.DeleteServer(ctx, ServerVM(id), disk, seedISO)
	return errors.Join(vmErr, m.UpdatePictures(ctx))
}

// PowerOnServer starts a server, as Hetzner's poweron: starting, then
// running; a server that runs already stays as it is.
func (m *Machine) PowerOnServer(ctx context.Context, id int64) error {
	server, err := Current(m.lab, state.Servers, id)
	if err != nil {
		return err
	}
	status := server.Status // the record is not kept across the system's call
	runs, err := m.system.ServerRuns(ctx, ServerVM(id))
	if err != nil {
		return err
	}
	if runs && status == state.ServerRunning {
		return nil
	}
	if err := m.lab.SetServerStatus(id, state.ServerStarting); err != nil {
		return err
	}
	if !runs { // a VM that runs while its status says otherwise only needs the status
		if err := m.system.PowerServer(ctx, ServerVM(id), system.PowerOn); err != nil {
			return err
		}
	}
	return m.lab.SetServerStatus(id, state.ServerRunning)
}

// PowerOffServer turns a server off at once, as Hetzner's poweroff:
// stopping, then off. Like every power action's steps it leaves
// want_running alone: the action's plan set it when the action was asked
// for, and an action queued behind may have changed it since.
func (m *Machine) PowerOffServer(ctx context.Context, id int64) error {
	if err := m.lab.SetServerStatus(id, state.ServerStopping); err != nil {
		return err
	}
	if err := m.system.PowerServer(ctx, ServerVM(id), system.PowerOff); err != nil {
		return err
	}
	return m.lab.SetServerStatus(id, state.ServerOff)
}

// ShutdownServer asks the guest to shut down, as Hetzner's shutdown, which
// ends once the button is pressed: the watchdog sees the VM go off.
func (m *Machine) ShutdownServer(ctx context.Context, id int64) error {
	return m.system.PowerServer(ctx, ServerVM(id), system.PowerShutdown)
}

// RebootServer restarts a server through its guest: Hyper-V has no ACPI
// reboot, so the guest shuts down (turned off after a minute) and the VM
// starts again. A server that is off starts, as on Hetzner (measured
// 2026-11-08).
func (m *Machine) RebootServer(ctx context.Context, id int64) error {
	vm := ServerVM(id)
	runs, err := m.system.ServerRuns(ctx, vm)
	if err != nil {
		return err
	}
	if err := m.lab.SetServerStatus(id, state.ServerStarting); err != nil {
		return err
	}
	if runs {
		if err := m.system.PowerServer(ctx, vm, system.PowerShutdown); err != nil {
			return err
		}
		off, err := m.system.WaitServerOff(ctx, vm, rebootTimeout)
		if err != nil {
			return err
		}
		if !off {
			m.lab.logger.Warn("the guest did not shut down for its reboot; turning it off", "server", id)
			if err := m.system.PowerServer(ctx, vm, system.PowerOff); err != nil {
				return err
			}
		}
	}
	return m.finishRestart(ctx, id, system.PowerOn)
}

// ResetServer resets a server, as Hetzner's reset; one that is off starts
// (measured 2026-11-08).
func (m *Machine) ResetServer(ctx context.Context, id int64) error {
	if err := m.lab.SetServerStatus(id, state.ServerStarting); err != nil {
		return err
	}
	return m.finishRestart(ctx, id, system.PowerReset)
}

// finishRestart runs the power command of a reboot or reset: starting,
// then running.
func (m *Machine) finishRestart(ctx context.Context, id int64, power system.Power) error {
	if err := m.system.PowerServer(ctx, ServerVM(id), power); err != nil {
		return err
	}
	return m.lab.SetServerStatus(id, state.ServerRunning)
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
	if !facts.IPv4.IsValid() && facts.IPv6.IsValid() {
		facts.ServiceIPv4 = serviceAddress(facts.IPv6)
	}
	return facts
}

// seedServer returns what a server's seed is built from.
func seedServer(facts picture.Server, lab picture.Lab) seed.Server {
	return seed.Server{
		ID:           facts.ID,
		Name:         facts.Name,
		MAC:          facts.MAC,
		DHCP4:        facts.IPv4.IsValid() || facts.ServiceIPv4.IsValid(),
		IPv6:         facts.IPv6,
		Resolvers:    lab.ApplianceAddresses(),
		PublicKeys:   picture.ServerKeys(lab, facts),
		RootPassword: facts.RootPassword,
		UserData:     facts.UserData,
	}
}
