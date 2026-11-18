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

// vendorBoundary separates the parts of the vendor-data MIME document.
const vendorBoundary = "==hlab-vendor-data=="

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
	sh.WriteString("systemctl daemon-reload\n")
	sh.WriteString("systemctl enable --now hlab-guest.timer\n")
	sh.WriteString("systemctl try-reload-or-restart ssh.service sshd.service || true\n")

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
