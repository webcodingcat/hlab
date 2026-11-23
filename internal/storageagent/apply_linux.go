//go:build linux

package storageagent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// homeDir returns the cleaned subaccount home below the box data, "." for
// the data itself; an absolute path or one with a ".." element is refused.
func homeDir(dir string) (string, error) {
	if dir == "" {
		return ".", nil
	}
	if !filepath.IsLocal(dir) || slices.Contains(strings.Split(filepath.ToSlash(dir), "/"), "..") {
		return "", fmt.Errorf("home directory %q is absolute or escapes the box", dir)
	}
	return filepath.Clean(dir), nil
}

// run executes a command and returns its output; on failure the error
// carries the command's output. Never pass a secret as an argument.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

// apply brings the storage VM to doc: the steps of Plan, then the
// passwords, then the quota accounting check of E18. A box whose step
// fails is logged and left out of its later steps and the passwords; the
// others are applied, and the failures are returned together at the end.
func (a *Agent) apply(ctx context.Context, doc agentapi.Storage) error {
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	boxes := map[string]agentapi.Box{}
	for _, b := range doc.Boxes {
		boxes[b.User] = b
	}
	skipped := map[string]bool{} // accounts left out of the passwords
	var errs []error
	for _, s := range Plan(st, doc) {
		if s.Kind == StepFormat {
			if err := a.format(ctx); err != nil {
				return err
			}
			continue
		}
		if skipped[s.Box] {
			continue
		}
		switch s.Kind {
		case StepRemoveBox:
			err = a.removeBox(ctx, s.Box)
		case StepCreateBox:
			err = a.createBox(ctx, boxes[s.Box])
		case StepUpdateBox:
			err = a.updateBox(ctx, boxes[s.Box], skipped)
		}
		if err != nil {
			slog.Error("storage box step failed", "box", s.Box, "step", s.Kind, "err", err)
			errs = append(errs, fmt.Errorf("box %s: %w", s.Box, err))
			skipped[s.Box] = true
		}
	}
	if err := setPasswords(ctx, doc, skipped); err != nil {
		errs = append(errs, err)
	}
	if err := a.applySSHD(ctx, sshdConfig(doc, skipped)); err != nil {
		errs = append(errs, err)
	}
	if err := a.checkQuota(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// state mounts the data disk when it holds a filesystem and returns what
// the VM holds.
func (a *Agent) state(ctx context.Context) (State, error) {
	var st State
	users, err := ids("/etc/passwd")
	if err != nil {
		return st, err
	}
	for u, id := range users {
		if n, err := boxUID(u); err == nil && n == id {
			st.Boxes = append(st.Boxes, u)
		}
	}
	slices.Sort(st.Boxes)
	if !a.mounted {
		if err := os.MkdirAll(a.o.Root, 0o755); err != nil {
			return st, err
		}
		mounted, err := isMounted(a.o.Root)
		if err != nil {
			return st, err
		}
		if !mounted {
			has, err := hasFilesystem(ctx, a.o.DataDisk)
			if err != nil || !has {
				return st, err
			}
		}
		if err := a.mount(ctx, mounted); err != nil {
			return st, err
		}
	}
	st.Filesystem = true
	out, err := run(ctx, "btrfs", "subvolume", "list", a.o.Root)
	if err != nil {
		return st, err
	}
	for line := range strings.Lines(out) {
		_, p, ok := strings.Cut(strings.TrimSpace(line), " path ")
		if ok && boxUserRE.MatchString(p) {
			st.Subvolumes = append(st.Subvolumes, p)
		}
	}
	return st, nil
}

// hasFilesystem reports whether dev carries any signature blkid knows;
// blkid exits with 2 when it finds none.
func hasFilesystem(ctx context.Context, dev string) (bool, error) {
	out, err := exec.CommandContext(ctx, "blkid", "-p", "-o", "value", "-s", "TYPE", dev).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blkid %s: %w", dev, err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// format makes the btrfs filesystem without the TRIM of the whole device
// (RESEARCH 4) and mounts it. mkfs.btrfs without -f refuses a device that
// holds a filesystem.
func (a *Agent) format(ctx context.Context) error {
	if _, err := run(ctx, "mkfs.btrfs", "-K", a.o.DataDisk); err != nil {
		return err
	}
	return a.mount(ctx, false)
}

// mount mounts the data disk at Root unless it is mounted already and
// enables the quota, which is a no-op when it is on.
func (a *Agent) mount(ctx context.Context, mounted bool) error {
	if !mounted {
		if _, err := run(ctx, "mount", "-t", "btrfs", "-o", "discard=async", a.o.DataDisk, a.o.Root); err != nil {
			return err
		}
	}
	if _, err := run(ctx, "btrfs", "quota", "enable", a.o.Root); err != nil {
		return err
	}
	a.mounted = true
	return nil
}

// createBox makes what a new box needs and is idempotent: group and user
// with UID n, the level-1 qgroup 1/<n> and the subvolume in it; the
// authorized_keys only together with a new subvolume.
func (a *Agent) createBox(ctx context.Context, b agentapi.Box) error {
	n, err := boxUID(b.User)
	if err != nil {
		return err
	}
	id := strconv.Itoa(n)
	groups, err := ids("/etc/group")
	if err != nil {
		return err
	}
	if _, ok := groups[b.User]; !ok {
		if _, err := run(ctx, "groupadd", "-g", id, b.User); err != nil {
			return err
		}
	}
	users, err := ids("/etc/passwd")
	if err != nil {
		return err
	}
	if _, ok := users[b.User]; !ok {
		if _, err := run(ctx, "useradd", "-M", "-u", id, "-g", id, "-d", "/home", "-s", "/bin/sh", b.User); err != nil {
			return err
		}
	}
	qg, err := a.qgroups(ctx)
	if err != nil {
		return err
	}
	level1 := "1/" + id
	if _, ok := qg[level1]; !ok {
		if _, err := run(ctx, "btrfs", "qgroup", "create", level1, a.o.Root); err != nil {
			return err
		}
	}
	data := filepath.Join(a.o.Root, b.User)
	if _, err := os.Lstat(data); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := run(ctx, "btrfs", "subvolume", "create", "-i", level1, data); err != nil {
		return err
	}
	if err := os.Chown(data, n, n); err != nil {
		return err
	}
	if err := os.Chmod(data, 0o750); err != nil {
		return err
	}
	if len(b.SSHKeys) == 0 {
		return nil
	}
	ssh := filepath.Join(data, ".ssh")
	if err := os.Mkdir(ssh, 0o700); err != nil {
		return err
	}
	keys := filepath.Join(ssh, "authorized_keys")
	if err := os.WriteFile(keys, []byte(strings.Join(b.SSHKeys, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	for _, p := range []string{ssh, keys} {
		if err := os.Chown(p, n, n); err != nil {
			return err
		}
	}
	return nil
}

// updateBox brings the accounts of an existing box to the document without
// touching its data: the quota limit, the subaccounts and their homes, and
// the chroots of all accounts.
// A subaccount whose home or chroot cannot be set up is logged, loses its
// chroot and is added to skipped.
func (a *Agent) updateBox(ctx context.Context, b agentapi.Box, skipped map[string]bool) error {
	n, err := boxUID(b.User)
	if err != nil {
		return err
	}
	id := strconv.Itoa(n)
	if _, err := run(ctx, "btrfs", "qgroup", "limit", strconv.FormatInt(b.QuotaBytes, 10), "1/"+id, a.o.Root); err != nil {
		return err
	}
	if err := a.ensureChroot(b.User, n, b.User, ".", false); err != nil {
		return err
	}
	users, err := ids("/etc/passwd")
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, s := range b.Subaccounts {
		wanted[s.User] = true
		// A home outside the box's data is refused for this subaccount
		// only; os.Root below also stops symlinks that lead out of it.
		// Without a chroot the account cannot log in.
		home, err := homeDir(s.HomeDir)
		if err == nil {
			err = a.ensureHome(b.User, n, home)
		}
		if err == nil {
			if _, ok := users[s.User]; !ok {
				_, err = run(ctx, "useradd", "-o", "-M", "-N", "-u", id, "-g", id, "-d", "/home", "-s", "/bin/sh", s.User)
			}
		}
		if err == nil {
			err = a.ensureChroot(s.User, n, b.User, home, s.Access.ReadOnly)
		}
		if err != nil {
			slog.Error("subaccount skipped", "user", s.User, "err", err)
			skipped[s.User] = true
			if err := removeChroot(s.User); err != nil {
				return err
			}
		}
	}
	for u := range users {
		if m := subUserRE.FindStringSubmatch(u); m != nil && m[1] == b.User && !wanted[u] {
			if err := removeAccount(ctx, u); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureHome creates the missing directories of home below the box's
// data, owned by the box, and requires home to be a directory; symlinks
// cannot lead out of the data.
func (a *Agent) ensureHome(box string, n int, home string) error {
	r, err := os.OpenRoot(filepath.Join(a.o.Root, box))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	p := ""
	for _, part := range strings.Split(home, "/") {
		if part == "." {
			continue
		}
		p = filepath.Join(p, part)
		if _, err := r.Stat(p); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := r.Mkdir(p, 0o750); err != nil {
			return err
		}
		if err := r.Lchown(p, n, n); err != nil {
			return err
		}
	}
	st, err := r.Stat(home)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("home %s is no directory", home)
	}
	return nil
}

// ensureChroot builds the chroot of account user (E17): /usr bound
// read-only, the usual links, device nodes, no tmp, a minimal passwd and
// group, and home bound from home below the box's data, read-only for
// read-only subaccounts.
func (a *Agent) ensureChroot(user string, n int, box, home string, readOnly bool) error {
	dir := filepath.Join(chrootBase, user)
	for _, d := range []string{"", "usr", "dev", "etc", "home"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	// The chroot offers no writable place outside the home: a /tmp on the
	// system disk would escape the box quota. Earlier chroots had one;
	// it goes with its content (RemoveAll does not follow symlinks).
	if err := os.RemoveAll(filepath.Join(dir, "tmp")); err != nil {
		return err
	}
	for _, l := range []string{"bin", "sbin", "lib", "lib64"} {
		p := filepath.Join(dir, l)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			if err := os.Symlink("usr/"+l, p); err != nil {
				return err
			}
		}
	}
	for name, minor := range map[string]uint32{"null": 3, "zero": 5, "urandom": 9} {
		p := filepath.Join(dir, "dev", name)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			if err := unix.Mknod(p, unix.S_IFCHR|0o666, int(unix.Mkdev(1, minor))); err != nil {
				return fmt.Errorf("mknod %s: %w", p, err)
			}
			if err := os.Chmod(p, 0o666); err != nil {
				return err
			}
		}
	}
	passwd := fmt.Sprintf("root:x:0:0::/:/bin/sh\n%s:x:%d:%d::/home:/bin/sh\n", user, n, n)
	group := fmt.Sprintf("root:x:0:\n%s:x:%d:\n", box, n)
	if err := os.WriteFile(filepath.Join(dir, "etc", "passwd"), []byte(passwd), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "group"), []byte(group), 0o644); err != nil {
		return err
	}

	mounts, err := readMounts()
	if err != nil {
		return err
	}
	usr := filepath.Join(dir, "usr")
	if _, ok := mounts[usr]; !ok {
		if err := bind("/usr", usr, true); err != nil {
			return err
		}
	}
	target := filepath.Join(dir, "home")
	want := "/" + filepath.Join(box, home)
	if m, ok := mounts[target]; ok {
		if m.root == want {
			if m.readOnly == readOnly {
				return nil
			}
			return remount(target, readOnly)
		}
		if err := unix.Unmount(target, unix.MNT_DETACH); err != nil {
			return fmt.Errorf("unmount %s: %w", target, err)
		}
	}
	r, err := os.OpenRoot(filepath.Join(a.o.Root, box))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(home)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// The descriptor pins the directory os.Root resolved, so a symlink
	// swapped in later cannot redirect the bind mount.
	return bind(fmt.Sprintf("/proc/self/fd/%d", f.Fd()), target, readOnly)
}

// bind bind-mounts src at dst, read-only when asked.
func bind(src, dst string, readOnly bool) error {
	if err := unix.Mount(src, dst, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s on %s: %w", src, dst, err)
	}
	if readOnly {
		return remount(dst, true)
	}
	return nil
}

// remount switches the bind mount at dst to read-only or read-write.
func remount(dst string, readOnly bool) error {
	flags := uintptr(unix.MS_REMOUNT | unix.MS_BIND)
	if readOnly {
		flags |= unix.MS_RDONLY
	}
	if err := unix.Mount("none", dst, "", flags, ""); err != nil {
		return fmt.Errorf("remount %s: %w", dst, err)
	}
	return nil
}

// removeChroot unmounts everything below the chroot of user and only then
// deletes it, so box data bound into it are never removed.
func removeChroot(user string) error {
	dir := filepath.Join(chrootBase, user)
	mounts, err := readMounts()
	if err != nil {
		return err
	}
	for p := range mounts {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			if err := unix.Unmount(p, unix.MNT_DETACH); err != nil {
				return fmt.Errorf("unmount %s: %w", p, err)
			}
		}
	}
	if mounts, err = readMounts(); err != nil {
		return err
	}
	for p := range mounts {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return fmt.Errorf("chroot %s still has mount %s", dir, p)
		}
	}
	return os.RemoveAll(dir)
}

// removeAccount ends the account's chroot and deletes the user.
func removeAccount(ctx context.Context, user string) error {
	if err := removeChroot(user); err != nil {
		return err
	}
	_, err := run(ctx, "userdel", "-f", user)
	return err
}

// removeBox deletes a box absent from the document: its processes, the
// chroots and users of the box and its subaccounts, the group, the
// subvolume and the qgroup.
func (a *Agent) removeBox(ctx context.Context, box string) error {
	n, err := boxUID(box)
	if err != nil {
		return err
	}
	id := strconv.Itoa(n)
	// pkill exits with 1 when no process matched.
	if _, err := run(ctx, "pkill", "-KILL", "-U", id); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return err
		}
	}
	users, err := ids("/etc/passwd")
	if err != nil {
		return err
	}
	for u := range users {
		if m := subUserRE.FindStringSubmatch(u); m != nil && m[1] == box {
			if err := removeAccount(ctx, u); err != nil {
				return err
			}
		}
	}
	if err := removeChroot(box); err != nil {
		return err
	}
	if _, ok := users[box]; ok {
		if _, err := run(ctx, "userdel", "-f", box); err != nil {
			return err
		}
	}
	groups, err := ids("/etc/group")
	if err != nil {
		return err
	}
	if _, ok := groups[box]; ok {
		if _, err := run(ctx, "groupdel", box); err != nil {
			return err
		}
	}
	data := filepath.Join(a.o.Root, box)
	if _, err := os.Lstat(data); err == nil {
		if _, err := run(ctx, "btrfs", "subvolume", "delete", data); err != nil {
			return err
		}
		// E18: the deletion must finish before stale qgroups are cleared.
		if _, err := run(ctx, "btrfs", "subvolume", "sync", a.o.Root); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	qg, err := a.qgroups(ctx)
	if err != nil {
		return err
	}
	if _, ok := qg["1/"+id]; ok {
		if _, err := run(ctx, "btrfs", "qgroup", "destroy", "1/"+id, a.o.Root); err != nil {
			return err
		}
	}
	_, err = run(ctx, "btrfs", "qgroup", "clear-stale", a.o.Root)
	return err
}

// setPasswords sets the crypt hash of every account whose shadow entry
// differs, through the standard input of chpasswd -e so that no hash
// appears in an argument list or an error.
// Skipped accounts, and accounts without a shadow entry, are left out, so
// that one of them cannot fail the passwords of the others.
func setPasswords(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error {
	shadow, err := os.ReadFile("/etc/shadow")
	if err != nil {
		return err
	}
	have := map[string]string{}
	for line := range strings.Lines(string(shadow)) {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) > 1 {
			have[f[0]] = f[1]
		}
	}
	var in strings.Builder
	set := func(user, hash string) {
		old, ok := have[user]
		if ok && !skipped[user] && hash != "" && old != hash {
			in.WriteString(user + ":" + hash + "\n")
		}
	}
	for _, b := range doc.Boxes {
		if skipped[b.User] {
			continue
		}
		set(b.User, b.Hashes.Crypt)
		for _, s := range b.Subaccounts {
			set(s.User, s.Hashes.Crypt)
		}
	}
	if in.Len() == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, "chpasswd", "-e")
	cmd.Stdin = strings.NewReader(in.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chpasswd: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// applySSHD writes conf as the sshd configuration and reloads sshd when
// conf differs from what this agent applied last; sshd checks the new
// file before it replaces the old one.
func (a *Agent) applySSHD(ctx context.Context, conf string) error {
	if conf == a.sshd {
		return nil
	}
	tmp := sshdConfigFile + ".hlab"
	if err := os.WriteFile(tmp, []byte(conf), 0o644); err != nil {
		return err
	}
	if _, err := run(ctx, "/usr/sbin/sshd", "-t", "-f", tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, sshdConfigFile); err != nil {
		return err
	}
	if _, err := run(ctx, "systemctl", "reload-or-restart", "ssh.service"); err != nil {
		return err
	}
	a.sshd = conf
	return nil
}

// checkQuota rescans the quota when btrfs reports the accounting
// inconsistent, which would let writes pass the limit (E18).
func (a *Agent) checkQuota(ctx context.Context) error {
	out, err := run(ctx, "btrfs", "qgroup", "show", a.o.Root)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(out), "inconsistent") {
		return nil
	}
	_, err = run(ctx, "btrfs", "quota", "rescan", "-w", a.o.Root)
	return err
}

// qgroups returns the referenced bytes of every qgroup by its ID ("0/256",
// "1/100000") from btrfs qgroup show --raw.
func (a *Agent) qgroups(ctx context.Context) (map[string]int64, error) {
	out, err := run(ctx, "btrfs", "qgroup", "show", "--raw", "--sync", a.o.Root)
	if err != nil {
		return nil, err
	}
	qg := map[string]int64{}
	for line := range strings.Lines(out) {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.Contains(f[0], "/") {
			continue
		}
		if rfer, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			qg[f[0]] = rfer
		}
	}
	return qg, nil
}

// ids returns name to ID (third field) of a passwd or group file.
func ids(file string) (map[string]int, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 3 {
			continue
		}
		if id, err := strconv.Atoi(fields[2]); err == nil {
			out[fields[0]] = id
		}
	}
	return out, sc.Err()
}

// mount is one entry of /proc/self/mountinfo.
type mount struct {
	root     string // path within the mounted filesystem
	readOnly bool
}

// readMounts returns the mounts by mount point.
func readMounts() (map[string]mount, error) {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	out := map[string]mount{}
	for line := range strings.Lines(string(raw)) {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		opts := strings.Split(f[5], ",")
		out[unescape(f[4])] = mount{root: unescape(f[3]), readOnly: slices.Contains(opts, "ro")}
	}
	return out, nil
}

// isMounted reports whether dir is a mount point.
func isMounted(dir string) (bool, error) {
	mounts, err := readMounts()
	if err != nil {
		return false, err
	}
	_, ok := mounts[filepath.Clean(dir)]
	return ok, nil
}

// unescape decodes the octal escapes (\040) of mountinfo paths.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
