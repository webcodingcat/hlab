//go:build linux

package storageagent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Deadlines of the rollback's waits.
const (
	rollbackIdleWait = 30 * time.Second // until no process uses the box's mounts
	rollbackSyncWait = 2 * time.Minute  // btrfs subvolume sync (cleaner wakes about every 30 s)
)

// fenceTable is the nftables table that refuses new and open connections
// to a box IP during its rollback; only one rollback runs at a time.
const fenceTable = "hlab_rollback"

// fencePorts are the protocol ports of a box IP; fencePassive the passive
// FTP range.
var (
	fencePorts   = []uint16{21, 22, 23, 443, 445}
	fencePassive = [2]uint16{50000, 50099}
)

// ssPID finds the processes in the output of ss -p.
var ssPID = regexp.MustCompile(`pid=([0-9]+)`)

// rollback brings the box's data back to snapshot job.Snapshot and removes
// the snapshots job.Newer (RESEARCH.md 6): the box IP is fenced, the
// sessions of the box and its subaccounts end, the box's mounts go, the
// data is swapped for a writable snapshot of the target in the box's
// qgroup, the old data and the newer snapshots are deleted and the
// accounting checked (E18); then the mounts are made again and the fence
// goes, also after a failure. A failure before the swap leaves the data
// untouched.
func (a *Agent) rollback(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	n, err := boxUID(job.Box)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	i := slices.IndexFunc(a.doc.Boxes, func(b agentapi.Box) bool { return b.User == job.Box })
	if i < 0 {
		return agentapi.StorageJobResult{}, errNotFound
	}
	b := a.doc.Boxes[i]
	for _, p := range []string{filepath.Join(a.o.Root, job.Box), filepath.Join(a.o.Root, snapshotDir, job.Box, job.Snapshot)} {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return agentapi.StorageJobResult{}, errNotFound
		} else if err != nil {
			return agentapi.StorageJobResult{}, err
		}
	}

	if err := fence(b.IP); err != nil {
		return agentapi.StorageJobResult{}, err
	}
	err = a.rollbackData(ctx, b, n, job)
	// The mounts come back on the new data, or on the old after a failure
	// before the swap; missing subaccount homes are created again.
	if rerr := a.updateBox(ctx, b, map[string]bool{}); rerr != nil {
		err = errors.Join(err, fmt.Errorf("rebind: %w", rerr))
	}
	if ferr := unfence(); ferr != nil {
		err = errors.Join(err, ferr)
	}
	return agentapi.StorageJobResult{}, err
}

// rollbackData ends the box's sessions, unmounts its homes and swaps its
// data for the snapshot.
func (a *Agent) rollbackData(ctx context.Context, b agentapi.Box, n int, job agentapi.StorageJob) error {
	if err := endSessions(ctx, b.IP, n); err != nil {
		return err
	}
	if err := clearDavLocks(b.User); err != nil {
		return err
	}
	mounts, err := boxMounts(b.User)
	if err != nil {
		return err
	}
	var homes []string
	for _, p := range mounts {
		if filepath.Base(p) == "home" {
			homes = append(homes, p)
		}
	}
	if err := waitIdle(ctx, homes); err != nil {
		return err
	}
	if err := unmountBox(mounts); err != nil {
		return err
	}

	data := filepath.Join(a.o.Root, b.User)
	old := filepath.Join(a.o.Root, ".rollback-"+b.User)
	snap := filepath.Join(a.o.Root, snapshotDir, b.User, job.Snapshot)
	if err := os.Rename(data, old); err != nil {
		return err
	}
	if _, err := run(ctx, "btrfs", "subvolume", "snapshot", "-i", "1/"+strconv.Itoa(n), snap, data); err != nil {
		if rerr := os.Rename(old, data); rerr != nil {
			return errors.Join(err, fmt.Errorf("restore %s: %w", data, rerr))
		}
		return err
	}

	// Swapped: from here on the box shows the snapshot's data.
	for _, name := range job.Newer {
		p := filepath.Join(a.o.Root, snapshotDir, b.User, name)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := run(ctx, "btrfs", "subvolume", "delete", p); err != nil {
			return err
		}
	}
	if _, err := run(ctx, "btrfs", "subvolume", "delete", old); err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, rollbackSyncWait)
	defer cancel()
	if _, err := run(sctx, "btrfs", "subvolume", "sync", a.o.Root); err != nil {
		return err
	}
	if _, err := run(ctx, "btrfs", "qgroup", "clear-stale", a.o.Root); err != nil {
		return err
	}
	return a.checkQuota(ctx)
}

// endSessions ends every connection to the box IP and every process of the
// box UID, which the box shares with its subaccounts. The processes ss
// shows on the sockets (smbd children run as root and are found only that
// way) and those of the UID are killed first: their connections close with
// a FIN, which TCP retransmits until the client has it. ss -K then resets
// what no process holds; its single RST, if lost, would leave an idle
// client unaware, and kernels without socket destroy ignore it.
func endSessions(ctx context.Context, ip netip.Addr, n int) error {
	addr := ip.String()
	out, err := run(ctx, "ss", "-tnpH", "src", addr)
	if err != nil {
		return err
	}
	for _, m := range ssPID.FindAllStringSubmatch(out, -1) {
		pid, err := strconv.Atoi(m[1])
		if err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		if err := unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("kill %d: %w", pid, err)
		}
	}
	if err := killUser(ctx, n); err != nil {
		return err
	}
	_, err = run(ctx, "ss", "-K", "-tn", "src", addr)
	return err
}

// clearDavLocks deletes the WebDAV lock databases of the box; their
// entries name paths of the old data.
func clearDavLocks(box string) error {
	dir := filepath.Join(davLockDir, box)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// boxMounts returns the mounts at and below the chroot homes of the box
// account and its subaccounts, innermost first.
func boxMounts(box string) ([]string, error) {
	mounts, err := readMounts()
	if err != nil {
		return nil, err
	}
	var out []string
	for p := range mounts {
		rel, ok := strings.CutPrefix(p, chrootBase+"/")
		if !ok {
			continue
		}
		user, rest, _ := strings.Cut(rel, "/")
		if m := subUserRE.FindStringSubmatch(user); user != box && (m == nil || m[1] != box) {
			continue
		}
		if rest == "home" || strings.HasPrefix(rest, "home/") {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(x, y string) int { return len(y) - len(x) })
	return out, nil
}

// unmountBox unmounts mounts, innermost first, without a lazy detach, so a
// busy mount stops the rollback before the swap. A home is unmounted by its
// path, which the account cannot change; a mount inside a home
// (.zfs/snapshot) lies on a path the account controls and is unmounted
// through a descriptor of os.Root whose mount ID matches.
func unmountBox(mounts []string) error {
	info, err := readMounts()
	if err != nil {
		return err
	}
	for _, p := range mounts {
		home, rel, inner := strings.Cut(p, "/home/")
		if !inner {
			if err := unix.Unmount(p, 0); err != nil {
				return fmt.Errorf("unmount %s: %w", p, err)
			}
			continue
		}
		r, err := os.OpenRoot(home + "/home")
		if err != nil {
			return err
		}
		err = unmountAt(r, rel, info[p].id, 0)
		_ = r.Close()
		if err != nil {
			return fmt.Errorf("unmount %s: %w", p, err)
		}
	}
	return nil
}

// waitIdle waits until no process uses the filesystems of paths (fuser -m
// exits 1), at most rollbackIdleWait.
func waitIdle(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, rollbackIdleWait)
	defer cancel()
	for {
		err := runBounded(ctx, nil, io.Discard, io.Discard, func() []byte { return nil }, "fuser", append([]string{"-s", "-m"}, paths...)...)
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil
		}
		if err != nil && !errors.As(err, &ee) {
			return fmt.Errorf("fuser: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("box mounts still in use after %s", rollbackIdleWait)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// fence refuses TCP to the box IP's protocol ports with a reset, new and
// open connections alike, in its own table before every other input chain.
func fence(ip netip.Addr) error {
	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("fence: %w", err)
	}
	table := &nftables.Table{Family: nftables.TableFamilyIPv4, Name: fenceTable}
	// Adding before deleting removes a table an interrupted run left.
	c.AddTable(table)
	c.DelTable(table)
	c.AddTable(table)
	accept := nftables.ChainPolicyAccept
	chain := c.AddChain(&nftables.Chain{
		Table:    table,
		Name:     "input",
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityRef(*nftables.ChainPriorityFilter - 10),
		Policy:   &accept,
	})
	// ip daddr <ip> meta l4proto tcp tcp dport <port> reject with tcp reset
	match := []expr.Any{
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip.AsSlice()},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
	}
	reject := &expr.Reject{Type: unix.NFT_REJECT_TCP_RST}
	for _, port := range fencePorts {
		c.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: append(slices.Clone(match),
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)}, reject)})
	}
	c.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: append(slices.Clone(match),
		&expr.Range{
			Op:       expr.CmpOpEq,
			Register: 1,
			FromData: binaryutil.BigEndian.PutUint16(fencePassive[0]),
			ToData:   binaryutil.BigEndian.PutUint16(fencePassive[1]),
		}, reject)})
	if err := c.Flush(); err != nil {
		return fmt.Errorf("fence: %w", err)
	}
	return nil
}

// unfence removes the fence table.
func unfence() error {
	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("unfence: %w", err)
	}
	c.DelTable(&nftables.Table{Family: nftables.TableFamilyIPv4, Name: fenceTable})
	if err := c.Flush(); err != nil {
		return fmt.Errorf("unfence: %w", err)
	}
	return nil
}
