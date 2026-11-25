//go:build linux

package storageagent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

func init() { registerService(&samba{}) }

// samba configures smbd: the configuration files, the tdbsam entries with
// the accounts' NT hashes, and a reload.
type samba struct {
	started bool // smbd was enabled and (re)started by this agent
}

func (*samba) Name() string { return "samba" }

// ntHashRE matches an NT hash as hlabd sends it.
var ntHashRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// Apply writes the configuration, sets the tdbsam entries and reloads
// smbd. smbd binds its addresses only at start, so a changed smb.conf (the
// box IPs) restarts it; changed shares are reloaded, which keeps sessions.
func (s *samba) Apply(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error {
	files := SambaConfig(doc, skipped)
	shares, err := writeSambaIncludes(ctx, files.PerIP)
	if err != nil {
		return err
	}
	confFile := filepath.Join(sambaIncludeDir, "smb.conf")
	globalChanged, err := writeSambaConf(ctx, confFile, files.Global)
	if err != nil {
		return err
	}
	hashes := map[string]string{}
	uids := map[string]int{}
	for _, b := range doc.Boxes {
		n, err := boxUID(b.User)
		if err != nil {
			continue
		}
		hashes[b.User], uids[b.User] = b.Hashes.NT, n
		for _, sub := range b.Subaccounts {
			hashes[sub.User], uids[sub.User] = sub.Hashes.NT, n
		}
	}
	var errs []error
	if err := setSambaAccounts(ctx, files.Accounts, hashes, uids); err != nil {
		errs = append(errs, err)
	}
	if err := pinSambaOwners(ctx, files.Accounts, uids); err != nil {
		errs = append(errs, err)
	}
	switch {
	case !s.started || globalChanged:
		if _, err := run(ctx, "systemctl", "enable", "smbd.service"); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if _, err := run(ctx, "systemctl", "restart", "smbd.service"); err != nil {
			return errors.Join(append(errs, err)...)
		}
		s.started = true
	case shares:
		if _, err := run(ctx, "smbcontrol", "smbd", "reload-config"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// writeSambaIncludes writes the per-IP files and the empty file of
// 0.0.0.0 that tools without a client address read (E16), each checked
// with testparm, and removes the files of addresses not in perIP. It
// reports whether a file changed.
func writeSambaIncludes(ctx context.Context, perIP map[netip.Addr]string) (bool, error) {
	want := map[string]string{"ip-0.0.0.0.conf": "# Read by tools without a client address.\n"}
	for ip, conf := range perIP {
		want["ip-"+ip.String()+".conf"] = conf
	}
	changed := false
	for name, conf := range want {
		c, err := writeSambaConf(ctx, filepath.Join(sambaIncludeDir, name), conf)
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	old, err := filepath.Glob(filepath.Join(sambaIncludeDir, "ip-*.conf"))
	if err != nil {
		return changed, err
	}
	for _, f := range old {
		if _, ok := want[filepath.Base(f)]; !ok {
			if err := os.Remove(f); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

// writeSambaConf replaces file atomically with conf when it differs,
// after testparm accepted conf; it reports whether the file changed.
func writeSambaConf(ctx context.Context, file, conf string) (bool, error) {
	if old, err := os.ReadFile(file); err == nil && string(old) == conf {
		return false, nil
	}
	tmp := file + ".hlab"
	if err := os.WriteFile(tmp, []byte(conf), 0o644); err != nil {
		return false, err
	}
	out, err := run(ctx, "testparm", "-s", tmp)
	if err == nil && strings.Contains(strings.ToLower(out), "unknown parameter") {
		err = fmt.Errorf("testparm %s: %s", file, strings.TrimSpace(out))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, os.Rename(tmp, file)
}

// sambaEntry is an account in tdbsam.
type sambaEntry struct{ sid, nt string }

// setSambaAccounts gives every account in accounts a tdbsam entry with its
// NT hash and removes the entries of all other accounts. A failing
// account is joined into the error; the others are still set.
//
// pdbedit takes an NT hash only as an argument, so the hash comes in
// through an import of a 0600 smbpasswd file instead. The import gives the
// entry the RID of its UID, which the box and its subaccounts share, so the
// entry gets its own SID back right after it.
func setSambaAccounts(ctx context.Context, accounts []string, hashes map[string]string, uids map[string]int) error {
	have, err := sambaEntries(ctx)
	if err != nil {
		return err
	}
	var errs []error
	wanted := map[string]bool{}
	for _, user := range accounts {
		wanted[user] = true
		nt := hashes[user]
		e, ok := have[user]
		if ok && strings.EqualFold(e.nt, nt) {
			continue
		}
		if err := setSambaHash(ctx, user, uids[user], nt, e.sid); err != nil {
			errs = append(errs, fmt.Errorf("samba account %s: %w", user, err))
		}
	}
	for user := range have {
		if !wanted[user] {
			if _, err := pdbedit(ctx, nil, "-x", "-u", user); err != nil {
				errs = append(errs, fmt.Errorf("remove samba account %s: %w", user, err))
			}
		}
	}
	return errors.Join(errs...)
}

// setSambaHash sets the NT hash of user, creating its entry with a random
// password when sid is empty.
func setSambaHash(ctx context.Context, user string, uid int, nt, sid string) error {
	if !ntHashRE.MatchString(nt) {
		return errors.New("no valid NT hash")
	}
	if sid == "" {
		pw := make([]byte, 24)
		if _, err := rand.Read(pw); err != nil {
			return err
		}
		p := hex.EncodeToString(pw)
		out, err := pdbedit(ctx, strings.NewReader(p+"\n"+p+"\n"), "-a", "-t", "-u", user)
		if err != nil {
			return err
		}
		if sid = verboseSIDs(out)[user]; sid == "" {
			return errors.New("pdbedit -a showed no user SID")
		}
	}
	f, err := os.CreateTemp("/run", "hlab-smbpasswd-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	line := fmt.Sprintf("%s:%d:%s:%s:[U          ]:LCT-%08X:\n",
		user, uid, strings.Repeat("X", 32), strings.ToUpper(nt), time.Now().Unix())
	_, err = f.WriteString(line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	out, err := pdbedit(ctx, nil, "-i", "smbpasswd:"+f.Name(), "-u", user)
	if err == nil && !strings.Contains(out, "Importing account for "+user+"...ok") {
		err = errors.New("pdbedit import failed")
	}
	if err != nil {
		return err
	}
	_, err = pdbedit(ctx, nil, "-r", "-u", user, "-U", sid)
	return err
}

// sambaIDCacheTime is how long the pinned ID mappings of pinSambaOwners
// last; every apply sets them again.
const sambaIDCacheTime = "520w"

// pinSambaOwners makes smbd see every file of a box as owned by the Unix
// user SID of the box's UID, which the token of each of its accounts holds.
//
// smbd checks access against an ACL made from the mode bits, whose owner is
// the SID it maps the file's UID to. It caches that mapping in gencache
// whenever it maps an account's SID to its UID at a login, so with the box
// and its subaccounts sharing one UID (E19) the owner became the account
// that logged in first, and all others lost the owner's write access. The
// SID-to-UID mappings of the accounts are pinned in the cache, so that a
// login does not look them up and overwrite the owner, and the UID maps to
// S-1-22-1-<uid>. The cache keys are Samba's (source3/lib/idmap_cache.c).
func pinSambaOwners(ctx context.Context, accounts []string, uids map[string]int) error {
	have, err := sambaEntries(ctx)
	if err != nil {
		return err
	}
	pinned := map[int]bool{}
	var errs []error
	for _, user := range accounts {
		e, ok := have[user]
		if !ok {
			continue
		}
		uid := uids[user]
		if _, err := run(ctx, "net", "cache", "add", "IDMAP/SID2XID/"+e.sid, fmt.Sprintf("%d:U", uid), sambaIDCacheTime); err != nil {
			errs = append(errs, err)
		}
		if !pinned[uid] {
			pinned[uid] = true
			if _, err := run(ctx, "net", "cache", "add", fmt.Sprintf("IDMAP/UID2SID/%d", uid), fmt.Sprintf("S-1-22-1-%d", uid), sambaIDCacheTime); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// sambaEntries returns the tdbsam entries by account name.
func sambaEntries(ctx context.Context) (map[string]sambaEntry, error) {
	verbose, err := pdbedit(ctx, nil, "-L", "-v")
	if err != nil {
		return nil, err
	}
	smbpw, err := pdbedit(ctx, nil, "-L", "-w")
	if err != nil {
		return nil, err
	}
	out := map[string]sambaEntry{}
	for user, sid := range verboseSIDs(verbose) {
		out[user] = sambaEntry{sid: sid}
	}
	sc := bufio.NewScanner(strings.NewReader(smbpw))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if e, ok := out[f[0]]; ok && len(f) > 3 {
			e.nt = f[3]
			out[f[0]] = e
		}
	}
	return out, nil
}

// verboseSIDs returns the user SIDs by account name from the verbose
// output of pdbedit.
func verboseSIDs(out string) map[string]string {
	sids := map[string]string{}
	user := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Unix username":
			user = strings.TrimSpace(v)
		case "User SID":
			if user != "" {
				sids[user] = strings.TrimSpace(v)
			}
		}
	}
	return sids
}

// pdbedit runs pdbedit with stdin. Its output may hold hashes, so it is
// only returned, and an error carries only the standard error.
func pdbedit(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	out, _, err := runIO(ctx, stdin, "pdbedit", args...)
	if err != nil {
		return "", err
	}
	return out, nil
}
