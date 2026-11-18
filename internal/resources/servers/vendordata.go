package servers

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/webcodingcat/hlab/internal/shacrypt"
)

// guestHook is the hlab-guest script. It rescans each hlab volume disk: a
// volume grown while attached reports its new size only on the disk's next
// SCSI command, which an idle disk never gets. Then it asks the metadata
// service for a pending root password reset, applies its hash with
// `chpasswd -e`, confirms it and remembers the ID, so a reset is applied
// once.
const guestHook = `#!/usr/bin/python3
# hlab guest hook: picks up grown volumes, applies a pending root password reset.
import glob
import json
import os
import subprocess
import urllib.request

BASE = "http://169.254.169.254/hlab/v1/password-reset"
STATE = "/var/lib/hlab-guest/last-reset"


def rescan_volumes():
    for link in glob.glob("/dev/disk/by-id/scsi-0HC_Volume_*"):
        disk = os.path.basename(os.path.realpath(link))
        try:
            with open("/sys/block/%s/device/rescan" % disk, "w") as f:
                f.write("1")
        except OSError:
            pass


def main():
    rescan_volumes()
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
                   text=True, check=True)
    req = urllib.request.Request(BASE + "/" + rid + "/done", data=b"", method="POST")
    urllib.request.urlopen(req, timeout=4).close()
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    with open(STATE, "w") as f:
        f.write(rid + "\n")


main()
`

const guestService = `[Unit]
Description=hlab guest hook (grown volumes, root password reset)
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /usr/local/sbin/hlab-guest
`

const guestTimer = `[Unit]
Description=Run the hlab guest hook every 5 seconds

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
const volumeID = `#!/usr/bin/python3
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
ACTION=="add|change", SUBSYSTEM=="block", ENV{DEVTYPE}=="disk", KERNEL=="sd*", PROGRAM="/usr/bin/python3 /usr/local/sbin/hlab-volume-id %k", ENV{HLAB_VOLUME_ID}="%c"
ENV{HLAB_VOLUME_ID}=="?*", SYMLINK+="disk/by-id/scsi-0HC_Volume_$env{HLAB_VOLUME_ID}", TAG+="systemd", ENV{SYSTEMD_WANTS}+="hlab-volume@$env{HLAB_VOLUME_ID}.service"
`

// volumeHook is the hlab-volume script: it waits until the metadata service
// lists the volume for automount (the list follows the attach), then
// mounts it, if it carries a filesystem, at /mnt/HC_Volume_<id> with an
// fstab line. The lock keeps it apart from the user-data part of server
// creation.
const volumeHook = `#!/usr/bin/python3
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
                        capture_output=True, text=True).stdout.strip()
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
ExecStart=/usr/bin/python3 /usr/local/sbin/hlab-volume %i
`

// vendorBoundary separates the parts of the vendor-data MIME document.
const vendorBoundary = "==hlab-vendor-data=="

// userBoundary separates the parts of the seed user-data MIME document.
const userBoundary = "==hlab-user-data=="

// seedUserData returns the user-data of the server's seed. Without
// automount volumes it is userData; with them, a multipart MIME document
// of userData unchanged (base64, so every byte survives; cloud-init detects
// its type) and a cloud-config part whose runcmd, appended to the user's,
// mounts each volume at /mnt/HC_Volume_<id> with an fstab line. runcmd runs
// after the vendor script, so the udev rule has named the disks by then.
func seedUserData(userData string, volumes []int64) string {
	if len(volumes) == 0 {
		return userData
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

	var b strings.Builder
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + userBoundary + "\"\nMIME-Version: 1.0\n\n")
	if userData != "" {
		b.WriteString("--" + userBoundary + "\nContent-Type: text/plain; charset=\"utf-8\"\nContent-Transfer-Encoding: base64\nMIME-Version: 1.0\n\n")
		enc := base64.StdEncoding.EncodeToString([]byte(userData))
		for len(enc) > 76 {
			b.WriteString(enc[:76] + "\n")
			enc = enc[76:]
		}
		b.WriteString(enc + "\n")
	}
	b.WriteString("--" + userBoundary + "\nContent-Type: text/cloud-config; charset=\"utf-8\"\nMIME-Version: 1.0\n\n")
	b.WriteString(cfg.String())
	b.WriteString("--" + userBoundary + "--\n")
	return b.String()
}

// vendorData returns the server's vendor-data, a multipart MIME document.
// Its cloud-config part lets root log in with its keys, enables password
// login and, if rootHash is set, makes it root's password without expiry;
// user_data may override these keys. Its shell script part, run once per
// instance by cloud-init's scripts_vendor, installs the sshd drop-in that
// allows root password login (cloud images refuse it) and the hlab-guest
// timer that applies later password resets; being a script, a user_data
// with its own write_files or runcmd does not replace it.
func vendorData(rootHash string) string {
	var cfg strings.Builder
	cfg.WriteString("#cloud-config\ndisable_root: false\nssh_pwauth: true\n")
	if rootHash != "" {
		cfg.WriteString("chpasswd:\n  expire: false\n  users:\n    - name: root\n      type: hash\n      password: \"" + rootHash + "\"\n")
	}

	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n# hlab guest hooks: root password login and password resets.\nset -e\n")
	writeFile(&sh, "/etc/ssh/sshd_config.d/00-hlab-root.conf", "0644", "PermitRootLogin yes\n")
	writeFile(&sh, "/usr/local/sbin/hlab-guest", "0755", guestHook)
	writeFile(&sh, "/etc/systemd/system/hlab-guest.service", "0644", guestService)
	writeFile(&sh, "/etc/systemd/system/hlab-guest.timer", "0644", guestTimer)
	writeFile(&sh, "/usr/local/sbin/hlab-volume-id", "0755", volumeID)
	writeFile(&sh, "/usr/local/sbin/hlab-volume", "0755", volumeHook)
	writeFile(&sh, "/etc/systemd/system/hlab-volume@.service", "0644", volumeService)
	writeFile(&sh, "/etc/udev/rules.d/61-hlab-volume.rules", "0644", volumeRule)
	writeFile(&sh, "/etc/systemd/system/hlab-sync.service", "0644", syncService)
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
