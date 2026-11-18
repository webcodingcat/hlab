//go:build windows

package windows_test

import (
	"context"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/webcodingcat/hlab/internal/hostnet"
	"github.com/webcodingcat/hlab/internal/hostnet/windows"
	"github.com/webcodingcat/hlab/internal/profile"
)

const program = `C:\hlab\bin\hlabd.exe`

// recorder is the fake PowerShell runner: it records every script and
// returns empty output (no adapters, rules, NAT instances or DNS servers).
type recorder struct {
	mu      sync.Mutex
	scripts []string
}

func (r *recorder) run(_ context.Context, script string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scripts = append(r.scripts, script)
	return nil, nil
}

func (r *recorder) since(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.scripts[n:], "\n")
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.scripts)
}

func config() hostnet.Config {
	return hostnet.Config{
		PublicRange:   netip.MustParsePrefix("198.19.0.0/16"),
		HostAddr:      netip.MustParsePrefix("198.19.0.2/30"),
		ApplianceAddr: netip.MustParseAddr("198.19.0.1"),
		SwitchName:    profile.Current().Tag,
		BaseZones:     []string{"hetzner.internal", "19.198.in-addr.arpa"},
	}
}

// param reports whether script passes value to -name, quoted or not.
func param(script, name, value string) bool {
	re := regexp.MustCompile(`(?i)-` + regexp.QuoteMeta(name) + `\s+['"]?` + regexp.QuoteMeta(value) + `['"]?(\s|;|$|\)|,)`)
	return re.MatchString(script)
}

// commands returns the lines of script that call cmdlet.
func commands(script, cmdlet string) []string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(cmdlet)) {
			out = append(out, line)
		}
	}
	return out
}

func TestHostNet(t *testing.T) {
	t.Run("SRV-06", func(t *testing.T) {
		rec := &recorder{}
		hn := windows.New(windows.Options{
			Run:                  rec.run,
			OtherInstanceRunning: func(context.Context) (bool, error) { return false, nil },
			StateDir:             t.TempDir(),
			Program:              program,
		})
		ctx := context.Background()
		cfg := config()
		group := profile.Current().Tag
		alias := "vEthernet (" + cfg.SwitchName + ")"

		check := func(t *testing.T, script string) {
			t.Helper()
			for _, forbidden := range []string{"Add-NetNatStaticMapping", "portproxy", "netsh advfirewall", "route add", "-Publish"} {
				if strings.Contains(strings.ToLower(script), strings.ToLower(forbidden)) {
					t.Errorf("Up issues %q, which exposes the public range beyond the host", forbidden)
				}
			}
			for _, line := range commands(script, "New-NetRoute") {
				if !param(line, "DestinationPrefix", cfg.PublicRange.String()) || !param(line, "NextHop", "198.19.0.1") {
					t.Errorf("route other than the public range via 198.19.0.1: %s", line)
				}
			}
			rules := commands(script, "New-NetFirewallRule")
			if len(rules) != 1 {
				t.Fatalf("Up creates %d firewall rules, want 1:\n%s", len(rules), strings.Join(rules, "\n"))
			}
			rule := rules[0]
			for _, p := range [][2]string{
				{"Direction", "Inbound"},
				{"Protocol", "TCP"},
				{"LocalPort", "8780"},
				{"LocalAddress", "198.19.0.2"},
				{"RemoteAddress", "198.19.0.1"},
				{"InterfaceAlias", alias},
				{"Program", program},
				{"Group", group},
			} {
				if !param(rule, p[0], p[1]) {
					t.Errorf("firewall rule lacks -%s %s: %s", p[0], p[1], rule)
				}
			}
		}

		if err := hn.Up(ctx, cfg); err != nil {
			t.Fatalf("first Up: %v", err)
		}
		check(t, rec.since(0))

		n := rec.count()
		if err := hn.Up(ctx, cfg); err != nil {
			t.Fatalf("second Up: %v", err)
		}
		second := rec.since(n)
		check(t, second)
		removeAt := -1
		for _, line := range commands(second, "Remove-NetFirewallRule") {
			if param(line, "Group", group) {
				removeAt = strings.Index(second, line)
				break
			}
		}
		createAt := strings.Index(strings.ToLower(second), strings.ToLower("New-NetFirewallRule"))
		if removeAt < 0 || removeAt > createAt {
			t.Errorf("second Up does not remove the rules of group %s before creating the rule", group)
		}
	})

	t.Run("LIFE-10", func(t *testing.T) {
		ctx := context.Background()
		for _, running := range []bool{true, false} {
			hn := windows.New(windows.Options{
				Run:                  (&recorder{}).run,
				OtherInstanceRunning: func(context.Context) (bool, error) { return running, nil },
				StateDir:             t.TempDir(),
				Program:              program,
			})
			var found *hostnet.Problem
			for _, p := range hn.Check(ctx, config()) {
				if p.Code == "other_instance_running" {
					found = &p
				}
			}
			switch {
			case running && found == nil:
				t.Errorf("other profile running: no other_instance_running problem")
			case running && (!found.Fatal || !strings.Contains(found.Message, profile.Other().Name)):
				t.Errorf("other_instance_running = %+v, want fatal and naming profile %q", *found, profile.Other().Name)
			case !running && found != nil:
				t.Errorf("other profile stopped: got problem %+v", *found)
			}
		}
	})
}
