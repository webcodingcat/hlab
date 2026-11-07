package fake_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/contract"
	"github.com/webcodingcat/hlab/internal/platform/fake"
)

func TestFakeKeepsTheContracts(t *testing.T) {
	machine := fake.New()
	parts := machine.Platform()
	used := netip.MustParsePrefix("192.168.0.0/16")
	machine.SetUsedRanges(used)
	dir := t.TempDir() // the fake's disks are names only; nothing is written there

	t.Run("Hypervisor", func(t *testing.T) { contract.Hypervisor(t, parts.Hypervisor, dir) })
	t.Run("HostNet", func(t *testing.T) { contract.HostNet(t, parts.HostNet, used) })
	t.Run("HostDNS", func(t *testing.T) { contract.HostDNS(t, parts.HostDNS) })
	t.Run("Lifecycle", func(t *testing.T) { contract.Lifecycle(t, parts.Lifecycle, machine.RequestStop) })
}

func TestFailNextFailsTheNextCallsInOrder(t *testing.T) {
	machine := fake.New()
	hypervisor := machine.Platform().Hypervisor
	first, second := errors.New("first"), errors.New("second")
	machine.FailNext("FindVMs", first)
	machine.FailNext("FindVMs", second)

	for _, want := range []error{first, second, nil} {
		if _, err := hypervisor.FindVMs(t.Context(), "tag"); !errors.Is(err, want) {
			t.Fatalf("FindVMs: got %v, want %v", err, want)
		}
	}
}

func TestDelayEndsWithTheContext(t *testing.T) {
	machine := fake.New()
	machine.Delay("FindVMs", time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := machine.Platform().Hypervisor.FindVMs(ctx, "tag"); !errors.Is(err, context.Canceled) {
		t.Fatalf("FindVMs: got %v, want context.Canceled", err)
	}
}

func TestCallsListsEveryCallWithItsArguments(t *testing.T) {
	machine := fake.New()
	hypervisor := machine.Platform().Hypervisor
	_ = hypervisor.CreateDisk(t.Context(), "disk.vhdx", 10, "")
	_ = hypervisor.StartVM(t.Context(), "web")

	want := []string{"CreateDisk disk.vhdx 10 ", "StartVM web"}
	if got := machine.Calls(); !slices.Equal(got, want) {
		t.Fatalf("Calls() = %q, want %q", got, want)
	}
}

func TestHostPartsKeepWhatTheyWereGiven(t *testing.T) {
	machine := fake.New()
	parts := machine.Platform()
	link := platform.LinkSpec{Switch: "hlab"}
	rules := []platform.DNSRule{{Domain: "hlab.internal"}}
	if err := parts.HostNet.SetUp(t.Context(), link); err != nil {
		t.Fatal(err)
	}
	if err := parts.HostDNS.Apply(t.Context(), rules); err != nil {
		t.Fatal(err)
	}

	if got, up := machine.Link(); !up || got.Switch != "hlab" {
		t.Errorf("Link() = %v, %v; want the switch hlab", got, up)
	}
	if got := machine.DNSRules(); len(got) != 1 || got[0].Domain != "hlab.internal" {
		t.Errorf("DNSRules() = %v, want the rule for hlab.internal", got)
	}
}

func TestServedAgentAnswersADial(t *testing.T) {
	machine := fake.New()
	machine.ServeAgent("appliance", func(conn net.Conn) {
		_, _ = conn.Write([]byte("hello"))
		_ = conn.Close()
	})

	conn, err := machine.Platform().Lifecycle.AgentDialer().Dial(t.Context(), "appliance", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	answer := make([]byte, 5)
	if _, err := conn.Read(answer); err != nil || string(answer) != "hello" {
		t.Fatalf("read %q, %v; want hello", answer, err)
	}
}
