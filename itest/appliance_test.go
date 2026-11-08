//go:build live && windows

package itest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows"
	"github.com/webcodingcat/hlab/internal/system"
)

// TestApplianceBootsAnswersAndComesBackAfterItDies starts the appliance of
// bin/dev appliance under Hyper-V, as hlabd does: host link, VM, agent over
// hv_sock, every picture. It checks that the appliance resolves names
// through its uplink, kills the VM and waits for the watchdog to bring it
// back with its pictures. It needs administrator rights and the appliance's
// disk next to the test (bin/dev itest copies it); HLab must not run.
func TestApplianceBootsAnswersAndComesBackAfterItDies(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(filepath.Dir(executable), "hlab-appliance.vhdx")
	if _, err := os.Stat(release); err != nil {
		t.Skipf("no appliance to boot: %v (bin/dev appliance)", err)
	}
	parts := windows.New(quietLogger())
	t.Cleanup(func() { _ = parts.Close() })
	sys := system.New(parts.Platform)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	lab := picture.DefaultLab()
	lab.Generation = 1
	link := system.HostLink{
		Ranges:    []netip.Prefix{lab.Range4, lab.Range6},
		Addresses: lab.HostAddresses(),
		Gateways:  lab.ApplianceAddresses(),
	}
	if err := sys.ConnectHost(ctx, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sys.DisconnectHost(context.Background()); err != nil {
			t.Errorf("DisconnectHost: %v", err)
		}
	})
	spec := system.ApplianceSpec{
		Release:    release,
		SystemDisk: filepath.Join(t.TempDir(), "system-A.vhdx"),
		MACs:       [4]string{lab.MACSet.MAC(1), lab.MACSet.MAC(2), lab.MACSet.MAC(3), lab.MACSet.MAC(4)},
	}
	appliance := sys.NewAppliance(spec, system.DefaultApplianceTimings(), quietLogger())
	t.Cleanup(func() {
		if err := appliance.Stop(context.Background()); err != nil {
			t.Errorf("stopping the appliance: %v", err)
		}
		if err := parts.Hypervisor.DeleteVM(context.Background(), system.ApplianceVM); err != nil && !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("DeleteVM: %v", err)
		}
	})
	pictures := system.Pictures{
		Links:    picture.Links(lab),
		Ruleset:  picture.Ruleset(lab),
		DHCP:     picture.DHCP(lab),
		Metadata: picture.Metadata(lab, catalog.Builtin()),
	}

	started := time.Now()
	if err := appliance.Start(ctx, pictures); err != nil {
		t.Fatal(err)
	}
	t.Logf("the appliance booted and took every picture in %s", time.Since(started).Round(time.Millisecond))
	agent := agentClient(parts.Lifecycle.AgentDialer())
	if missing := missingPictures(ctx, agent, 1); len(missing) > 0 {
		t.Fatalf("the agent lacks the pictures %v", missing)
	}
	state, err := parts.Hypervisor.VMState(ctx, system.ApplianceVM)
	if err != nil || !state.Heartbeat {
		t.Errorf("the appliance's state is %+v, %v; want its heartbeat", state, err)
	}
	resolved := resolveThroughAppliance(ctx, t, lab.ApplianceAddresses()[0].String())
	t.Logf("the appliance resolved hetzner.com after %s", resolved.Round(time.Millisecond))

	// the appliance dies; the watchdog notices within three checks
	killed := time.Now()
	if err := parts.Hypervisor.StopVM(ctx, system.ApplianceVM); err != nil {
		t.Fatal(err)
	}
	for len(missingPictures(ctx, agent, 1)) > 0 {
		if time.Since(killed) > 2*time.Minute {
			t.Fatal("the watchdog did not bring the appliance back within 2 minutes")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("the appliance was back with its pictures %s after it died", time.Since(killed).Round(time.Millisecond))
}

// agentClient returns an HTTP client of the appliance's agent.
func agentClient(dialer platform.AgentDialer) *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.Dial(ctx, system.ApplianceVM, agentproto.Port)
			},
		},
	}
}

// missingPictures returns the pictures the agent has not applied in the
// generation, or every picture if it does not answer.
func missingPictures(ctx context.Context, agent *http.Client, generation uint64) []string {
	every := []string{agentproto.PictureLinks, agentproto.PictureRuleset, agentproto.PictureDHCP, agentproto.PictureMetadata}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent"+agentproto.PathStatus, nil)
	response, err := agent.Do(request)
	if err != nil {
		return every
	}
	defer func() { _ = response.Body.Close() }()
	var status agentproto.Status
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return every
	}
	var missing []string
	for _, name := range every {
		if status.Generations[name] != generation {
			missing = append(missing, name)
		}
	}
	return missing
}

// resolveThroughAppliance asks the appliance's resolver for hetzner.com from
// the host, until it answers (its uplink needs DHCP first), and returns how
// long that took. The answer proves the host link, the ruleset's input rules
// and the appliance's own way out through the uplink's table.
func resolveThroughAppliance(ctx context.Context, t *testing.T, server string) time.Duration {
	t.Helper()
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(server, "53"))
		},
	}
	started := time.Now()
	for {
		lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		addresses, err := resolver.LookupHost(lookupCtx, "hetzner.com")
		cancel()
		if err == nil && len(addresses) > 0 {
			return time.Since(started)
		}
		if time.Since(started) > time.Minute {
			t.Fatalf("the appliance did not resolve hetzner.com within a minute: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
