package servers

import (
	"crypto/rand"
	"strings"

	"github.com/webcodingcat/hlab/internal/shacrypt"
)

// guestHook is the hlab-guest script: it asks the metadata service for a
// pending root password reset, applies its hash with `chpasswd -e`,
// confirms it and remembers the ID, so a reset is applied once.
const guestHook = `#!/usr/bin/python3
# hlab guest hook: applies a pending root password reset.
import json
import os
import subprocess
import urllib.request

BASE = "http://169.254.169.254/hlab/v1/password-reset"
STATE = "/var/lib/hlab-guest/last-reset"


def main():
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
Description=hlab guest hook (root password reset)
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

// vendorData returns the server's vendor-data: root may log in with its
// keys and, by the sshd drop-in, with a password, as on Hetzner's images
// (cloud images lock root and allow no root password login); rootHash, if
// set, becomes root's password without expiry; the hlab-guest timer applies
// later password resets. user_data overrides any of it.
func vendorData(rootHash string) string {
	var b strings.Builder
	b.WriteString("#cloud-config\ndisable_root: false\nssh_pwauth: true\n")
	if rootHash != "" {
		b.WriteString("chpasswd:\n  expire: false\n  users:\n    - name: root\n      type: hash\n      password: \"" + rootHash + "\"\n")
	}
	b.WriteString("write_files:\n")
	writeFile(&b, "/etc/ssh/sshd_config.d/00-hlab-root.conf", "0644", "PermitRootLogin yes\n")
	writeFile(&b, "/usr/local/sbin/hlab-guest", "0755", guestHook)
	writeFile(&b, "/etc/systemd/system/hlab-guest.service", "0644", guestService)
	writeFile(&b, "/etc/systemd/system/hlab-guest.timer", "0644", guestTimer)
	b.WriteString("runcmd:\n")
	b.WriteString("  - [systemctl, daemon-reload]\n")
	b.WriteString("  - [systemctl, enable, --now, hlab-guest.timer]\n")
	b.WriteString("  - [sh, -c, \"systemctl try-reload-or-restart ssh.service sshd.service || true\"]\n")
	return b.String()
}

// writeFile appends one write_files entry with content as a literal block.
func writeFile(b *strings.Builder, path, mode, content string) {
	b.WriteString("  - path: " + path + "\n    permissions: '" + mode + "'\n    owner: root:root\n    content: |\n")
	for _, line := range strings.SplitAfter(strings.TrimSuffix(content, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("      " + line)
	}
	b.WriteString("\n")
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
