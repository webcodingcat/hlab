package servers

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/shacrypt"
)

// guestHook is the hlab-guest script. It rescans each hlab volume disk: a
// volume grown while attached reports its new size only on the disk's next
// SCSI command, which an idle disk never gets. It configures the private
// adapters from the metadata service's private-networks list (see
// syncNetworks in the script). Then it asks the metadata service for a
// pending root password reset, applies its hash with `chpasswd -e`,
// confirms it and remembers the ID, so a reset is applied once.
//
// Private adapters: per entry whose MAC is one of hlab's (02:48:4c:2x:xx:xx)
// and belongs to a link that does not carry its private IP yet (a link
// hot-added after boot; links the seed's network-config configured at boot
// are left to it, with what the user added), it sets the private IP as /32
// and a route to the network range via the gateway, on-link; Hetzner's
// guests get the same from DHCP, hlab has no DHCP server. With
// systemd-networkd active it writes
// /run/systemd/network/05-hlab-<mac>.network (volatile, gone after a
// reboot) and reconfigures the link; otherwise it uses ip addr/route
// replace. What it set is kept in /run/hlab-guest/networks.json and removed
// again when the entry goes (detach). Alias IPs are left to the user, as at
// Hetzner.
const guestHook = `#!@PYTHON@
# hlab guest hook: picks up grown volumes, configures private adapters,
# applies a pending root password reset.
import glob
import json
import os
import subprocess
import urllib.request

BASE = "http://169.254.169.254/hlab/v1/password-reset"
STATE = "/var/lib/hlab-guest/last-reset"
PRIVATE = "http://169.254.169.254/hetzner/v1/metadata/private-networks"
NETSTATE = "/run/hlab-guest/networks.json"
NETDIR = "/run/systemd/network"
MAC_PREFIX = "02:48:4c:2"


def rescan_volumes():
    for link in glob.glob("/dev/disk/by-id/scsi-0HC_Volume_*"):
        disk = os.path.basename(os.path.realpath(link))
        try:
            with open("/sys/block/%s/device/rescan" % disk, "w") as f:
                f.write("1")
        except OSError:
            pass


def links():
    out = {}
    for path in glob.glob("/sys/class/net/*/address"):
        try:
            with open(path) as f:
                mac = f.read().strip().lower()
        except OSError:
            continue
        if mac.startswith(MAC_PREFIX):
            out[mac] = path.split("/")[-2]
    return out


def networkd_active():
    return subprocess.run(["systemctl", "is-active", "--quiet", "systemd-networkd"]).returncode == 0


def unit_path(mac):
    return "%s/05-hlab-%s.network" % (NETDIR, mac.replace(":", ""))


def run(*args):
    subprocess.run(list(args), check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def quiet(*args):
    subprocess.run(list(args), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


# A link that already carries its private IP was configured by the seed's
# network-config at boot (or by an earlier run): the hook leaves it alone.
def has_ip(dev, ip):
    out = subprocess.run(["ip", "-o", "addr", "show", "dev", dev],
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                         universal_newlines=True).stdout
    return (" %s/" % ip) in out


# The address comes last, so a link carrying it is fully configured.
def apply_net(mac, dev, cfg, networkd):
    if networkd:
        os.makedirs(NETDIR, exist_ok=True)
        with open(unit_path(mac), "w") as f:
            f.write("[Match]\nMACAddress=%s\n\n[Network]\nAddress=%s/32\n\n"
                    "[Route]\nDestination=%s\nGateway=%s\nGatewayOnLink=yes\n"
                    % (mac, cfg["ip"], cfg["network"], cfg["gateway"]))
        run("networkctl", "reload")
        run("networkctl", "reconfigure", dev)
    else:
        run("ip", "link", "set", dev, "up")
        run("ip", "route", "replace", cfg["network"], "via", cfg["gateway"], "dev", dev, "onlink")
        run("ip", "addr", "replace", cfg["ip"] + "/32", "dev", dev)


def remove_net(mac, dev, cfg):
    try:
        os.remove(unit_path(mac))
        quiet("networkctl", "reload")
    except FileNotFoundError:
        pass
    if dev:
        quiet("ip", "route", "del", cfg["network"], "via", cfg["gateway"], "dev", dev)
        quiet("ip", "addr", "del", cfg["ip"] + "/32", "dev", dev)


def sync_networks():
    import yaml
    with urllib.request.urlopen(PRIVATE, timeout=4) as r:
        if r.status != 200:
            return
        entries = yaml.safe_load(r) or []
    want = {}
    for e in entries:
        mac = str(e.get("mac_address") or "").lower()
        if not mac.startswith(MAC_PREFIX) or not e.get("ip") or not e.get("gateway"):
            continue
        want[mac] = {"ip": str(e["ip"]), "gateway": str(e["gateway"]),
                     "network": str(e.get("network") or e.get("subnet"))}
    try:
        with open(NETSTATE) as f:
            have = json.load(f)
    except (OSError, ValueError):
        have = {}
    devs = links()
    done = {}
    for mac, cfg in have.items():
        if want.get(mac) == cfg and mac in devs:
            done[mac] = cfg
        else:
            remove_net(mac, devs.get(mac), cfg)
    networkd = None
    for mac, cfg in want.items():
        if mac in done or mac not in devs or has_ip(devs[mac], cfg["ip"]):
            continue
        if networkd is None:
            networkd = networkd_active()
        try:
            apply_net(mac, devs[mac], cfg, networkd)
        except (OSError, subprocess.CalledProcessError):
            continue
        done[mac] = cfg
    if done != have:
        os.makedirs(os.path.dirname(NETSTATE), exist_ok=True)
        with open(NETSTATE, "w") as f:
            json.dump(done, f)


def main():
    rescan_volumes()
    try:
        sync_networks()
    except Exception:
        pass
    try:
        with urllib.request.urlopen(BASE, timeout=4) as r:
            if r.status != 200:
                return
            reset = json.load(r)
    except Exception:
        return
    rid = str(reset["id"])
    try:
        with open(STATE) as f:
            if f.read().strip() == rid:
                return
    except FileNotFoundError:
        pass
    subprocess.run(["chpasswd", "-e"], input="root:%s\n" % reset["sha512_crypt"],
                   universal_newlines=True, check=True)
    req = urllib.request.Request(BASE + "/" + rid + "/done", data=b"", method="POST")
    urllib.request.urlopen(req, timeout=4).close()
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    with open(STATE, "w") as f:
        f.write(rid + "\n")


main()
`

const guestService = `[Unit]
Description=HLab guest hook (grown volumes, private networks, root password reset)
After=network-online.target

[Service]
Type=oneshot
ExecStart=@PYTHON@ /usr/local/sbin/hlab-guest
`

const guestTimer = `[Unit]
Description=Run the HLab guest hook every 5 seconds

[Timer]
OnBootSec=5s
OnUnitActiveSec=5s
AccuracySec=1s

[Install]
WantedBy=timers.target
`

// syncService flushes the page cache once cloud-init's final stage has
// ended, so files written by user_data's runcmd and scripts (which run after
// the vendor script) survive a hard reset or poweroff soon after the first
// boot instead of coming back empty.
const syncService = `[Unit]
Description=Flush the files of cloud-init's first boot to disk
After=cloud-final.service

[Service]
Type=oneshot
ExecStart=/bin/sync
`

// volumeID is the udev helper of the volume rule: it prints the volume ID
// of the disk named by its argument and fails for other disks. hlab writes
// the page 83 identifier 484C4142-0000-4000-8000-<id> (DECISIONS "Volume
// identifier byte order"); the guest sees its first bytes as 42414c480000
// and the ID as the last 6 bytes, in the T10 and in the NAA designator (E6).
const volumeID = `#!@PYTHON@
# hlab udev helper: prints the volume ID of an hlab volume disk.
import sys

MARK = bytes.fromhex("42414c480000")


def main():
    try:
        with open("/sys/class/block/%s/device/vpd_pg83" % sys.argv[1], "rb") as f:
            page = f.read()
    except OSError:
        return 1
    end = min(len(page), 4 + int.from_bytes(page[2:4], "big"))
    i = 4
    while i + 4 <= end:
        d = page[i + 4:i + 4 + page[i + 3]]
        if len(d) > 6 and MARK in d[:-6]:
            print(int.from_bytes(d[-6:], "big"))
            return 0
        i += 4 + page[i + 3]
    return 1


sys.exit(main())
`

// volumeRule names hlab volume disks scsi-0HC_Volume_<id> like Hetzner's
// and starts the volume hook for each.
const volumeRule = `# hlab volumes: /dev/disk/by-id/scsi-0HC_Volume_<id> and the automount hook.
ACTION=="add|change", SUBSYSTEM=="block", ENV{DEVTYPE}=="disk", KERNEL=="sd*", PROGRAM="@PYTHON@ /usr/local/sbin/hlab-volume-id %k", ENV{HLAB_VOLUME_ID}="%c"
ENV{HLAB_VOLUME_ID}=="?*", SYMLINK+="disk/by-id/scsi-0HC_Volume_$env{HLAB_VOLUME_ID}", TAG+="systemd", ENV{SYSTEMD_WANTS}+="hlab-volume@$env{HLAB_VOLUME_ID}.service"
`

// volumeHook is the hlab-volume script: it waits until the metadata service
// lists the volume for automount (the list follows the attach), then
// mounts it, if it carries a filesystem, at /mnt/HC_Volume_<id> with an
// fstab line. The lock keeps it apart from the user-data part of server
// creation.
const volumeHook = `#!@PYTHON@
# hlab volume hook: mounts an attached volume listed for automount.
import fcntl
import json
import os
import subprocess
import sys
import time
import urllib.request

URL = "http://169.254.169.254/hlab/v1/volumes"
DEADLINE = 60


def listed(vid):
    end = time.monotonic() + DEADLINE
    while True:
        try:
            with urllib.request.urlopen(URL, timeout=4) as r:
                if vid in json.load(r)["volumes"]:
                    return True
        except Exception:
            pass
        if time.monotonic() >= end:
            return False
        time.sleep(2)


def main():
    vid = int(sys.argv[1])
    if not listed(vid):
        return
    dev = "/dev/disk/by-id/scsi-0HC_Volume_%d" % vid
    mnt = "/mnt/HC_Volume_%d" % vid
    fs = subprocess.run(["blkid", "-o", "value", "-s", "TYPE", dev],
                        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                        universal_newlines=True).stdout.strip()
    if not fs:
        return
    with open("/run/hlab-volume.lock", "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        with open("/etc/fstab") as f:
            fstab = f.read()
        if not any(line.split()[1:2] == [mnt] for line in fstab.splitlines()):
            with open("/etc/fstab", "a") as f:
                if fstab and not fstab.endswith("\n"):
                    f.write("\n")
                f.write("%s %s %s discard,nofail,defaults 0 0\n" % (dev, mnt, fs))
        os.makedirs(mnt, exist_ok=True)
        if subprocess.run(["mountpoint", "-q", mnt]).returncode != 0:
            subprocess.run(["mount", mnt], check=True)


main()
`

const volumeService = `[Unit]
Description=hlab volume hook (automount of volume %i)
After=network-online.target

[Service]
Type=oneshot
TimeoutStartSec=120
ExecStart=@PYTHON@ /usr/local/sbin/hlab-volume %i
`

// networkUpdates is the cloud-init drop-in that renders the network-config
// on every boot, not only on the first.
const networkUpdates = `updates:
  network:
    when: [boot-new-instance, boot]
`

// vendorBoundary separates the parts of the vendor-data MIME document.
const vendorBoundary = "==hlab-vendor-data=="

// userBoundary separates the parts of the seed user-data MIME document.
const userBoundary = "==hlab-user-data=="

// seedUserData returns the user-data of the server's seed. It first decodes
// userData like cloud-init's Hetzner datasource: strict base64 (the base64
// alphabet with correct padding, no whitespace) is decoded, anything else is
// kept raw. Without automount volumes that is the result; cloud-init itself
// gunzips a top-level gzip and parses a top-level MIME document. With them,
// it is a multipart MIME document of the user's content and a cloud-config
// part whose runcmd, appended to the user's, mounts each volume at
// /mnt/HC_Volume_<id> with an fstab line. runcmd runs after the vendor
// script, so the udev rule has named the disks by then. The user's content
// is gunzipped if it is gzip; the leaf parts of a multipart MIME document are
// copied unchanged and in order before hlab's part, since cloud-init does
// not unpack a document nested in a text/plain part; anything else becomes
// one base64 text/plain part (every byte survives; cloud-init detects its
// type).
func seedUserData(userData string, volumes []int64) string {
	data := decodeUserData(userData)
	if len(volumes) == 0 {
		return string(data)
	}
	var cfg strings.Builder
	cfg.WriteString("#cloud-config\nmerge_how: \"list(append)+dict(no_replace,recurse_list)+str()\"\nruncmd:\n")
	for _, id := range volumes {
		fmt.Fprintf(&cfg, `  - |
    d=/dev/disk/by-id/scsi-0HC_Volume_%[1]d m=/mnt/HC_Volume_%[1]d
    udevadm settle
    if t=$(blkid -o value -s TYPE "$d"); then
      exec 9>/run/hlab-volume.lock
      flock 9
      mkdir -p "$m"
      awk -v m="$m" '$2 == m {f = 1} END {exit !f}' /etc/fstab || echo "$d $m $t discard,nofail,defaults 0 0" >> /etc/fstab
      mountpoint -q "$m" || mount "$m"
      exec 9>&-
    fi
`, id)
	}

	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		if unzipped, err := gunzip(data); err == nil {
			data = unzipped
		}
	}
	var parts []mimePart
	if leaves, ok := mimeLeaves(data); ok {
		parts = leaves
	} else if len(data) > 0 {
		enc := base64.StdEncoding.EncodeToString(data)
		var body strings.Builder
		for len(enc) > 76 {
			body.WriteString(enc[:76] + "\n")
			enc = enc[76:]
		}
		body.WriteString(enc + "\n")
		parts = append(parts, mimePart{
			header: textproto.MIMEHeader{
				"Content-Type":              {`text/plain; charset="utf-8"`},
				"Content-Transfer-Encoding": {"base64"},
				"Mime-Version":              {"1.0"},
			},
			body: []byte(body.String()),
		})
	}
	parts = append(parts, mimePart{
		header: textproto.MIMEHeader{
			"Content-Type": {`text/cloud-config; charset="utf-8"`},
			"Mime-Version": {"1.0"},
		},
		body: []byte(cfg.String()),
	})
	return writeMultipart(parts)
}

// mimePart is a leaf part of a MIME document: its headers and its body as
// written, not decoded.
type mimePart struct {
	header textproto.MIMEHeader
	body   []byte
}

// decodeUserData decodes userData as cloud-init's maybe_b64decode with
// validate=True: standard base64 of the alphabet only, with correct
// padding; otherwise userData is returned unchanged.
func decodeUserData(userData string) []byte {
	if !strictBase64.MatchString(userData) {
		return []byte(userData)
	}
	dec, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		return []byte(userData)
	}
	return dec
}

// strictBase64 matches the base64 alphabet with at most two padding
// characters at the end; Go's decoder would skip newlines, cloud-init's does
// not.
var strictBase64 = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

// mimeLeaves returns the leaf parts of data in document order, walking
// nested multiparts, if data is a MIME message with a multipart/* type.
func mimeLeaves(data []byte) ([]mimePart, bool) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return nil, false
	}
	leaves, err := walkLeaves(msg.Body, params["boundary"])
	if err != nil || len(leaves) == 0 {
		return nil, false
	}
	return leaves, true
}

func walkLeaves(body io.Reader, boundary string) ([]mimePart, error) {
	r := multipart.NewReader(body, boundary)
	var out []mimePart
	for {
		p, err := r.NextRawPart()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		mt, params, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err == nil && strings.HasPrefix(mt, "multipart/") {
			nested, err := walkLeaves(bytes.NewReader(data), params["boundary"])
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
			continue
		}
		out = append(out, mimePart{header: p.Header, body: data})
	}
}

// writeMultipart writes parts as a multipart/mixed document. Its boundary
// is userBoundary, with a number added while the boundary occurs in a part.
func writeMultipart(parts []mimePart) string {
	boundary := userBoundary
	for n := 1; occursIn(parts, "--"+boundary); n++ {
		boundary = userBoundary + strconv.Itoa(n)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary(boundary); err != nil {
		panic(err) // userBoundary is a valid boundary
	}
	for _, p := range parts {
		pw, err := w.CreatePart(p.header)
		if err == nil {
			_, err = pw.Write(p.body)
		}
		if err != nil {
			panic(err) // writes to a bytes.Buffer do not fail
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return "Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\nMIME-Version: 1.0\r\n\r\n" + body.String()
}

// occursIn reports whether s occurs in a header value or body of parts.
func occursIn(parts []mimePart, s string) bool {
	for _, p := range parts {
		if bytes.Contains(p.body, []byte(s)) {
			return true
		}
		for _, vs := range p.header {
			for _, v := range vs {
				if strings.Contains(v, s) {
					return true
				}
			}
		}
	}
	return false
}

// vendorData returns the server's vendor-data, a multipart MIME document.
// Its cloud-config part lets root log in with its keys, enables password
// login, sets `users: []` so no image creates its default user (servers
// have root only, like Hetzner's), sets hostname and fqdn to name (the seed's
// meta-data has no local-hostname; with hostname alone EL and Fedora keep
// the reverse-DNS name) and, if rootHash is set, makes it root's
// password without expiry; user_data may override these keys, its own
// `users` included. Its shell script part, run once per instance by
// cloud-init's scripts_vendor, installs the sshd drop-in that allows root
// password login (cloud images refuse it) and the hlab-guest timer that
// applies later password resets and private networks, and the cloud-init
// drop-in that renders network-config on every boot; being a script, a
// user_data with its own write_files or runcmd does not replace it. The
// Python hooks run on Python 3.6 and later; the script writes the
// interpreter it finds (/usr/bin/python3, else EL8's
// /usr/libexec/platform-python) for the @PYTHON@ placeholder in their
// shebangs, units and udev rule.
func vendorData(rootHash, name string) string {
	var cfg strings.Builder
	cfg.WriteString("#cloud-config\ndisable_root: false\nssh_pwauth: true\nusers: []\n")
	cfg.WriteString("hostname: " + strconv.Quote(name) + "\nfqdn: " + strconv.Quote(name) + "\n")
	if rootHash != "" {
		cfg.WriteString("chpasswd:\n  expire: false\n  users:\n    - name: root\n      type: hash\n      password: \"" + rootHash + "\"\n")
	}

	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n# HLab guest hooks: root password login and password resets.\nset -e\n")
	// EL8 images have no /usr/bin/python3, only the platform Python (E32).
	sh.WriteString("PY=/usr/bin/python3\n[ -x \"$PY\" ] || PY=/usr/libexec/platform-python\n")
	writeFile(&sh, "/etc/ssh/sshd_config.d/00-hlab-root.conf", "0644", "PermitRootLogin yes\n")
	writeFile(&sh, "/usr/local/sbin/hlab-guest", "0755", guestHook)
	writeFile(&sh, "/etc/systemd/system/hlab-guest.service", "0644", guestService)
	writeFile(&sh, "/etc/systemd/system/hlab-guest.timer", "0644", guestTimer)
	writeFile(&sh, "/usr/local/sbin/hlab-volume-id", "0755", volumeID)
	writeFile(&sh, "/usr/local/sbin/hlab-volume", "0755", volumeHook)
	writeFile(&sh, "/etc/systemd/system/hlab-volume@.service", "0644", volumeService)
	writeFile(&sh, "/etc/udev/rules.d/61-hlab-volume.rules", "0644", volumeRule)
	writeFile(&sh, "/etc/systemd/system/hlab-sync.service", "0644", syncService)
	// Each later boot renders the seed's network-config again, which hlab
	// rewrites when private networks change; cloud-init's default renders it
	// only on a new instance.
	writeFile(&sh, "/etc/cloud/cloud.cfg.d/99-hlab-network.cfg", "0644", networkUpdates)
	// The hooks, their units and the udev rule run the interpreter found above.
	sh.WriteString("sed -i \"s|@PYTHON@|$PY|\" /usr/local/sbin/hlab-guest /usr/local/sbin/hlab-volume-id" +
		" /usr/local/sbin/hlab-volume /etc/systemd/system/hlab-guest.service" +
		" /etc/systemd/system/hlab-volume@.service /etc/udev/rules.d/61-hlab-volume.rules\n")
	sh.WriteString("systemctl daemon-reload\n")
	sh.WriteString("systemctl enable --now hlab-guest.timer\n")
	// Disks attached before this script ran get their names now.
	sh.WriteString("udevadm control --reload\n")
	sh.WriteString("udevadm trigger --action=change --subsystem-match=block\n")
	sh.WriteString("udevadm settle || true\n")
	sh.WriteString("systemctl try-reload-or-restart ssh.service sshd.service || true\n")
	// poweroff and reset cut the power: without a sync, the files of the
	// first boot (SSH host keys, root password, these hooks, user_data's
	// files) can still be only in the page cache when cloud-init reports
	// done, and come back empty. user_data's scripts run after this one, so
	// the sync waits for the end of the final stage; --no-block queues it,
	// as this script itself runs inside cloud-final.service.
	sh.WriteString("systemctl start --no-block hlab-sync.service\n")

	var b strings.Builder
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + vendorBoundary + "\"\nMIME-Version: 1.0\n\n")
	for _, part := range []struct{ typ, body string }{
		{"text/cloud-config", cfg.String()},
		{"text/x-shellscript", sh.String()},
	} {
		b.WriteString("--" + vendorBoundary + "\nContent-Type: " + part.typ + "; charset=\"utf-8\"\nMIME-Version: 1.0\n\n")
		b.WriteString(part.body)
	}
	b.WriteString("--" + vendorBoundary + "--\n")
	return b.String()
}

// writeFile appends shell lines that write content to path with mode; the
// quoted here-document keeps content literal.
func writeFile(b *strings.Builder, path, mode, content string) {
	b.WriteString("mkdir -p \"$(dirname " + path + ")\"\n")
	b.WriteString("cat > " + path + " <<'HLAB_EOF'\n" + content + "HLAB_EOF\n")
	b.WriteString("chmod " + mode + " " + path + "\n")
}

// newRootPassword returns a random root password (26 base32 characters,
// 130 bits) and its SHA-512 crypt.
func newRootPassword() (password, hash string, err error) {
	password = rand.Text()
	hash, err = shacrypt.NewSHA512(password)
	if err != nil {
		return "", "", err
	}
	return password, hash, nil
}
