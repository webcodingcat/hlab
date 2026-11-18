//go:build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// scsiDevices lists the SCSI devices as H:C:T:L entries.
const scsiDevices = "/sys/bus/scsi/devices"

// diskWait bounds the wait for a hot-added disk to appear.
const diskWait = 30 * time.Second

// findDisk returns /dev/sdX of the disk (SCSI type 0) at lun. The SCSI host
// number varies between boots (E7), so only the LUN is compared; the
// appliance has disks on one controller only. A freshly hot-added disk may
// take a moment to appear, so findDisk waits for it with a deadline.
func findDisk(ctx context.Context, lun int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, diskWait)
	defer cancel()
	for {
		dev, err := lookupDisk(lun)
		if err == nil {
			return dev, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("disk at LUN %d: %w", lun, err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func lookupDisk(lun int) (string, error) {
	entries, err := os.ReadDir(scsiDevices)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		parts := strings.Split(e.Name(), ":")
		if len(parts) != 4 {
			continue
		}
		if l, err := strconv.Atoi(parts[3]); err != nil || l != lun {
			continue
		}
		dir := filepath.Join(scsiDevices, e.Name())
		typ, err := os.ReadFile(filepath.Join(dir, "type"))
		if err != nil || strings.TrimSpace(string(typ)) != "0" {
			continue
		}
		blocks, err := os.ReadDir(filepath.Join(dir, "block"))
		if err != nil || len(blocks) == 0 {
			continue
		}
		found = append(found, "/dev/"+blocks[0].Name())
	}
	switch len(found) {
	case 0:
		return "", errors.New("not found")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("several disks: %s", strings.Join(found, ", "))
	}
}

// run executes a command and returns its output; on failure the error
// carries the command's output.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

// mkfs formats the whole disk at lun with fsType (ext4 or xfs).
func mkfs(ctx context.Context, lun int, fsType string) error {
	dev, err := findDisk(ctx, lun)
	if err != nil {
		return err
	}
	switch fsType {
	case "ext4":
		_, err = run(ctx, "mkfs.ext4", "-F", dev)
	case "xfs":
		if _, lerr := exec.LookPath("mkfs.xfs"); lerr != nil {
			return errors.New("mkfs.xfs is not installed on the appliance (package xfsprogs missing)")
		}
		_, err = run(ctx, "mkfs.xfs", "-f", dev)
	default:
		err = fmt.Errorf("unsupported filesystem %q", fsType)
	}
	return err
}

// partition is one partition of a disk as blkid reports it.
type partition struct {
	dev      string
	typ      string // filesystem type, "ext4", "vfat", "" if none
	uuid     string // filesystem UUID
	partUUID string
}

// partitions lists the partitions of disk dev (/dev/sdX) with their
// identifiers.
func partitions(ctx context.Context, dev string) ([]partition, error) {
	name := filepath.Base(dev)
	entries, err := os.ReadDir(filepath.Join("/sys/block", name))
	if err != nil {
		return nil, err
	}
	var parts []partition
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), name) {
			continue
		}
		p := partition{dev: "/dev/" + e.Name()}
		// No cache: the identifiers change during the job.
		out, err := exec.CommandContext(ctx, "blkid", "-c", "/dev/null", "-o", "export", p.dev).Output()
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 { // 2: nothing found
				return nil, fmt.Errorf("blkid %s: %w", p.dev, err)
			}
		}
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			k, v, _ := strings.Cut(sc.Text(), "=")
			switch k {
			case "TYPE":
				p.typ = v
			case "UUID":
				p.uuid = v
			case "PARTUUID":
				p.partUUID = v
			}
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%s has no partitions", dev)
	}
	return parts, nil
}

// newUUID returns a random (version 4) UUID in lower case.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// rescuePrepare gives the copy of the Debian image at lun new partition
// GUIDs and a new root filesystem UUID and rewrites fstab and every
// grub.cfg to them, so the rescue disk never finds the server's disk.
func rescuePrepare(ctx context.Context, lun int) error {
	dev, err := findDisk(ctx, lun)
	if err != nil {
		return err
	}
	before, err := partitions(ctx, dev)
	if err != nil {
		return err
	}
	var root, esp *partition
	for i := range before {
		switch before[i].typ {
		case "ext4":
			root = &before[i]
		case "vfat":
			esp = &before[i]
		}
	}
	if root == nil || esp == nil {
		return fmt.Errorf("%s: no ext4 root or no vfat ESP partition", dev)
	}

	if _, err := run(ctx, "sgdisk", "-G", dev); err != nil {
		return err
	}
	if _, err := run(ctx, "blockdev", "--rereadpt", dev); err != nil {
		return err
	}
	after, err := partitions(ctx, dev)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, a := range after {
		for _, b := range before {
			if a.dev == b.dev && b.partUUID != "" && a.partUUID != "" && a.partUUID != b.partUUID {
				ids[b.partUUID] = a.partUUID
			}
		}
	}
	if _, ok := ids[root.partUUID]; !ok {
		return fmt.Errorf("%s: root PARTUUID unchanged after sgdisk -G", root.dev)
	}

	// A metadata_csum filesystem must be clean before tune2fs changes its
	// UUID. Exit code 1 means errors were corrected.
	if out, err := exec.CommandContext(ctx, "e2fsck", "-f", "-p", root.dev).CombinedOutput(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() > 1 {
			return fmt.Errorf("e2fsck %s: %w: %s", root.dev, err, bytes.TrimSpace(out))
		}
	}
	fsUUID, err := newUUID()
	if err != nil {
		return err
	}
	if _, err := run(ctx, "tune2fs", "-U", fsUUID, root.dev); err != nil {
		return err
	}
	ids[root.uuid] = fsUUID

	return withMounts(ctx, root.dev, esp.dev, func(rootDir, espDir string) error {
		files := []string{filepath.Join(rootDir, "etc", "fstab")}
		for _, dir := range []string{filepath.Join(rootDir, "boot", "grub"), espDir} {
			found, err := grubConfigs(dir)
			if err != nil {
				return err
			}
			files = append(files, found...)
		}
		for _, f := range files {
			if err := rewriteFile(f, ids); err != nil {
				return err
			}
		}
		return nil
	})
}

// withMounts mounts root and ESP on temporary folders, calls fn and
// unmounts both again.
func withMounts(ctx context.Context, rootDev, espDev string, fn func(rootDir, espDir string) error) (err error) {
	base, err := os.MkdirTemp("", "rescue-prepare-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(base) }()
	rootDir, espDir := filepath.Join(base, "root"), filepath.Join(base, "esp")
	for _, d := range []string{rootDir, espDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return err
		}
	}
	for _, m := range [][2]string{{rootDev, rootDir}, {espDev, espDir}} {
		if _, err := run(ctx, "mount", m[0], m[1]); err != nil {
			return err
		}
		dir := m[1]
		defer func() {
			// Unmount even when ctx has ended, so the disk can be detached.
			if _, uerr := run(context.Background(), "umount", dir); uerr != nil && err == nil {
				err = uerr
			}
		}()
	}
	return fn(rootDir, espDir)
}

// grubConfigs returns every file named grub.cfg below dir.
func grubConfigs(dir string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), "grub.cfg") {
			found = append(found, path)
		}
		return nil
	})
	return found, err
}

// rewriteFile applies RewriteIDs to the file at path, keeping its mode.
func rewriteFile(path string, ids map[string]string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := RewriteIDs(string(data), ids)
	if out == string(data) {
		return nil
	}
	return os.WriteFile(path, []byte(out), info.Mode().Perm())
}
