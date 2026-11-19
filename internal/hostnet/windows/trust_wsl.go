//go:build windows

package windows

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf16"
)

// wslCAFile is where the CA goes in each distro.
const wslCAFile = "/usr/local/share/ca-certificates/hlab-ca.crt"

// exitNoUpdateCA is the exit code of the scripts when the distro has no
// update-ca-certificates.
const exitNoUpdateCA = 3

const (
	wslInstallScript = "command -v update-ca-certificates >/dev/null || exit 3; cat > " + wslCAFile + " && update-ca-certificates"
	wslRemoveScript  = "command -v update-ca-certificates >/dev/null || exit 3; rm -f " + wslCAFile + " && update-ca-certificates --fresh"
)

// wslTrust is a trust switch the WSL distros have not received yet.
type wslTrust struct {
	pem []byte
	on  bool
}

// trustWSL installs (on) or removes (off) the CA in every WSL2 distro of
// the console user. Distros without update-ca-certificates and failing
// distros are logged and skipped. Without a logged-on user the switch
// stays pending until wslChanged applies it. WSL failures never fail the
// switch: the Windows store is the part that must succeed.
func (h *HostNet) trustWSL(ctx context.Context, pem []byte, on bool) error {
	h.wslMu.Lock()
	defer h.wslMu.Unlock()
	h.wslPending = nil
	if errors.Is(h.applyWSL(ctx, pem, on), errNoUser) {
		slog.Info("hostnet: no user logged on; the CA switch for WSL waits for the next WSL change", "on", on)
		h.wslPending = &wslTrust{pem: bytes.Clone(pem), on: on}
	}
	return nil
}

// wslChanged applies a pending trust switch; WatchWSL and Up call it.
func (h *HostNet) wslChanged(ctx context.Context) {
	h.wslMu.Lock()
	defer h.wslMu.Unlock()
	p := h.wslPending
	if p == nil {
		return
	}
	if errors.Is(h.applyWSL(ctx, p.pem, p.on), errNoUser) {
		return
	}
	h.wslPending = nil
}

// applyWSL runs the install or removal script in each distro. It returns
// errNoUser when the commands cannot run for lack of a logged-on user.
func (h *HostNet) applyWSL(ctx context.Context, pem []byte, on bool) error {
	out, code, err := h.opts.RunAsUser(ctx, nil, "wsl.exe", "--list", "--quiet")
	if errors.Is(err, errNoUser) {
		return err
	}
	if err != nil || code != 0 {
		slog.Info("hostnet: no WSL distros to trust the CA in", "exit", code, "err", err, "output", decodeWSLOutput(out))
		return nil
	}
	script, stdin := wslInstallScript, pem
	if !on {
		script, stdin = wslRemoveScript, nil
	}
	for _, d := range wslDistros(out) {
		out, code, err := h.opts.RunAsUser(ctx, stdin, "wsl.exe", "-d", d, "-u", "root", "-e", "sh", "-c", script)
		switch {
		case errors.Is(err, errNoUser):
			return err
		case err != nil:
			slog.Warn("hostnet: CA switch in WSL distro failed", "distro", d, "on", on, "err", err)
		case code == exitNoUpdateCA:
			slog.Info("hostnet: WSL distro has no update-ca-certificates; skipped", "distro", d)
		case code != 0:
			slog.Warn("hostnet: CA switch in WSL distro failed", "distro", d, "on", on, "exit", code, "output", strings.TrimSpace(decodeWSLOutput(out)))
		}
	}
	return nil
}

// wslDistros parses the output of `wsl.exe --list --quiet` without the
// docker-desktop distros.
func wslDistros(out []byte) []string {
	var names []string
	for _, line := range strings.Split(decodeWSLOutput(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(strings.ToLower(name), "docker-desktop") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// decodeWSLOutput decodes wsl.exe output: UTF-16LE when it starts with a
// byte order mark, UTF-8 otherwise.
func decodeWSLOutput(out []byte) string {
	if bytes.HasPrefix(out, []byte{0xFF, 0xFE}) {
		u := make([]uint16, (len(out)-2)/2)
		_ = binary.Read(bytes.NewReader(out[2:2+2*len(u)]), binary.LittleEndian, u)
		return string(utf16.Decode(u))
	}
	return strings.TrimPrefix(string(out), "\xef\xbb\xbf")
}
