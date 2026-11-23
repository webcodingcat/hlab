//go:build linux

package storageagent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// snapshotDir is the folder below the root that holds the snapshots,
// <root>/.snapshots/<user>/<name>, outside every chroot; its name is no box
// user.
const snapshotDir = ".snapshots"

// snapshotFolder returns <root>/.snapshots/<box>, created when missing:
// owned by root with the box's group, so that only the box account, through
// its .zfs/snapshot bind, can list it.
func (a *Agent) snapshotFolder(box string, n int) (string, error) {
	base := filepath.Join(a.o.Root, snapshotDir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	dir := filepath.Join(base, box)
	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if err := os.Chown(dir, 0, n); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o750)
}

// snapshot takes the read-only snapshot job.Snapshot of the box's data in
// the box's level-1 qgroup (E18), checks the accounting and returns the
// snapshot's exclusive and referenced bytes.
func (a *Agent) snapshot(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	n, err := boxUID(job.Box)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	data := filepath.Join(a.o.Root, job.Box)
	if _, err := os.Lstat(data); errors.Is(err, fs.ErrNotExist) {
		return agentapi.StorageJobResult{}, errNotFound
	}
	dir, err := a.snapshotFolder(job.Box, n)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	snap := filepath.Join(dir, job.Snapshot)
	if _, err := run(ctx, "btrfs", "subvolume", "snapshot", "-r", "-i", "1/"+strconv.Itoa(n), data, snap); err != nil {
		return agentapi.StorageJobResult{}, err
	}
	if err := a.checkQuota(ctx); err != nil {
		return agentapi.StorageJobResult{}, err
	}
	id, err := run(ctx, "btrfs", "inspect-internal", "rootid", snap)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	qg, err := a.qgroups(ctx)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	q := qg["0/"+strings.TrimSpace(id)]
	return agentapi.StorageJobResult{
		SnapshotSizes: map[string]int64{job.Snapshot: q.excl},
		SnapshotFS:    map[string]int64{job.Snapshot: q.rfer},
	}, nil
}

// deleteSnapshotJob removes snapshot job.Snapshot of the box; a missing one
// is not_found.
func (a *Agent) deleteSnapshotJob(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	if _, err := os.Lstat(filepath.Join(a.o.Root, snapshotDir, job.Box, job.Snapshot)); errors.Is(err, fs.ErrNotExist) {
		return agentapi.StorageJobResult{}, errNotFound
	}
	return agentapi.StorageJobResult{}, a.deleteSnapshot(ctx, job.Box, job.Snapshot)
}

// deleteSnapshot removes the snapshot subvolume name of box and, after the
// deletion finished, the stale qgroups, then checks the accounting (E18).
func (a *Agent) deleteSnapshot(ctx context.Context, box, name string) error {
	if _, err := run(ctx, "btrfs", "subvolume", "delete", filepath.Join(a.o.Root, snapshotDir, box, name)); err != nil {
		return err
	}
	if _, err := run(ctx, "btrfs", "subvolume", "sync", a.o.Root); err != nil {
		return err
	}
	if _, err := run(ctx, "btrfs", "qgroup", "clear-stale", a.o.Root); err != nil {
		return err
	}
	return a.checkQuota(ctx)
}

// applyZFS shows the box's snapshot folder read-only as .zfs/snapshot in
// the box account's chroot home when b.Access.ZFS is set, and removes it
// otherwise. Subaccounts never get it. The home mount is made private
// first so that the bind does not propagate into the box's data or a
// subaccount's home. The account controls the path below home, so the
// agent never mounts or unmounts by that path: it acts on descriptors that
// os.Root opened, and on a mount only when its ID matches.
func (a *Agent) applyZFS(b agentapi.Box, n int) error {
	home := filepath.Join(chrootBase, b.User, "home")
	target := filepath.Join(home, ".zfs", "snapshot")
	mounts, err := readMounts()
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	m, mounted := mounts[target]
	want := "/" + snapshotDir + "/" + b.User
	if mounted && (!b.Access.ZFS || m.root != want || !m.readOnly) {
		if err := unmountAt(r, ".zfs/snapshot", m.id, unix.MNT_DETACH); err != nil {
			return fmt.Errorf("unmount %s: %w", target, err)
		}
		mounted = false
	}
	if !b.Access.ZFS {
		// Only empty folders go; data the account put there stays.
		for _, p := range []string{".zfs/snapshot", ".zfs"} {
			if st, err := r.Lstat(p); err == nil && st.IsDir() {
				_ = r.Remove(p)
			}
		}
		return nil
	}
	if mounted {
		return nil
	}
	src, err := a.snapshotFolder(b.User, n)
	if err != nil {
		return err
	}
	for _, p := range []string{".zfs", ".zfs/snapshot"} {
		if err := r.Mkdir(p, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if st, err := r.Lstat(p); err != nil || !st.IsDir() {
			// The account's own file or link of that name: no .zfs for it.
			slog.Warn("storage box .zfs skipped", "box", b.User, "path", p)
			return nil
		}
	}
	f, err := r.Open(".zfs/snapshot")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := unix.Mount("", home, "", unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make %s private: %w", home, err)
	}
	return bindReadOnlyAt(src, int(f.Fd()))
}

// bindReadOnlyAt clones src as a detached bind mount, makes it read-only
// and attaches it on the directory descriptor dst, which pins the folder
// os.Root resolved: no path the account controls is resolved, and the bind
// is never writable.
func bindReadOnlyAt(src string, dst int) error {
	tree, err := unix.OpenTree(unix.AT_FDCWD, src, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open_tree %s: %w", src, err)
	}
	defer func() { _ = unix.Close(tree) }()
	if err := unix.MountSetattr(tree, "", unix.AT_EMPTY_PATH, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}); err != nil {
		return fmt.Errorf("mount_setattr %s: %w", src, err)
	}
	if err := unix.MoveMount(tree, "", dst, "", unix.MOVE_MOUNT_F_EMPTY_PATH|unix.MOVE_MOUNT_T_EMPTY_PATH); err != nil {
		return fmt.Errorf("move_mount %s: %w", src, err)
	}
	return nil
}

// unmountAt unmounts with flags the mount with ID id that p below r leads
// to; p is opened through os.Root, and the mount is left alone when its ID
// differs.
func unmountAt(r *os.Root, p string, id uint64, flags int) error {
	f, err := r.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var stx unix.Statx_t
	if err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &stx); err != nil {
		return err
	}
	if stx.Mnt_id != id {
		return fmt.Errorf("%s leads to mount %d, want %d", p, stx.Mnt_id, id)
	}
	return unix.Unmount(fmt.Sprintf("/proc/self/fd/%d", f.Fd()), flags)
}
