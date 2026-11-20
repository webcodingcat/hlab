//go:build windows

package windows

import (
	"context"
	"fmt"
	"strings"

	"github.com/webcodingcat/hlab/internal/hostnet"
)

// wslSettings are the networking keys of section [wsl2] in .wslconfig,
// lower-cased; empty when the file does not set them.
type wslSettings struct {
	networkingMode string
	dnsTunneling   string
}

// parseWSLConfig reads the [wsl2] section of a .wslconfig. Section and key
// names match case-insensitively; other sections are ignored.
func parseWSLConfig(data []byte) wslSettings {
	var s wslSettings
	inWSL2 := false
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf") // UTF-8 BOM
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inWSL2 = strings.EqualFold(strings.TrimSpace(line[1:len(line)-1]), "wsl2")
			continue
		}
		if !inWSL2 {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "networkingmode":
			s.networkingMode = value
		case "dnstunneling":
			s.dnsTunneling = value
		}
	}
	return s
}

// wslProblems maps the settings to non-fatal problems. Unset values and
// values WSL does not know mean the defaults: NAT with dnsTunneling on.
func wslProblems(s wslSettings) []hostnet.Problem {
	var out []hostnet.Problem
	switch s.networkingMode {
	case "mirrored":
		out = append(out, hostnet.Problem{Code: hostnet.ProblemWSLMirrored,
			Message: "WSL2 runs in mirrored networking mode, which HLab does not support; set networkingMode=nat in .wslconfig and run wsl --shutdown"})
	case "consomme", "virtioproxy":
		out = append(out, hostnet.Problem{Code: hostnet.ProblemWSLVirtioProxy,
			Message: fmt.Sprintf("WSL2 runs in %s networking mode, which HLab does not support; set networkingMode=nat in .wslconfig and run wsl --shutdown", s.networkingMode)})
	case "bridged", "none":
		out = append(out, hostnet.Problem{Code: hostnet.ProblemWSLModeUnsupported,
			Message: fmt.Sprintf("WSL2 networking mode %s is not supported by HLab; set networkingMode=nat in .wslconfig and run wsl --shutdown", s.networkingMode)})
	}
	if s.dnsTunneling == "false" {
		out = append(out, hostnet.Problem{Code: hostnet.ProblemWSLDNSTunnelingOff,
			Message: "WSL2 has dnsTunneling turned off, so WSL cannot resolve HLab names; set dnsTunneling=true in .wslconfig and run wsl --shutdown"})
	}
	return out
}

// checkWSL reads the console user's .wslconfig and reports what hlab does
// not support. It never changes the file. A file that cannot be read
// leaves the mode unknown and gives no problem.
func (h *HostNet) checkWSL(ctx context.Context) []hostnet.Problem {
	data, found, err := h.opts.WSLConfig(ctx)
	if err != nil || !found {
		return nil
	}
	return wslProblems(parseWSLConfig(data))
}
