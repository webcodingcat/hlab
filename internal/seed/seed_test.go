package seed

import (
	"flag"
	"maps"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write the golden files anew")

var resolvers = []netip.Addr{netip.MustParseAddr("198.18.0.2"), netip.MustParseAddr("2001:db8:0:ffff::2")}

// TestFilesMatchTheGoldenFiles covers a server with both addresses and SSH
// keys, one with IPv4 only, a password and user data, one with IPv6 only,
// which takes IPv4 by DHCP too (as on Hetzner), and one without public
// addresses.
func TestFilesMatchTheGoldenFiles(t *testing.T) {
	servers := map[string]Server{
		"web": {
			ID: 1000001, Name: "web", MAC: "96:00:00:0f:42:41", DHCP4: true,
			IPv6: netip.MustParsePrefix("2001:db8:1:1::/64"), Resolvers: resolvers,
			PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUser user@example"},
		},
		"db": {
			ID: 1000002, Name: "db", MAC: "96:00:00:0f:42:42", DHCP4: true, Resolvers: resolvers,
			RootPassword: `p"ss`, UserData: "#cloud-config\npackages: [nginx]\n",
		},
		"only-six": {
			ID: 1000004, Name: "only-six", MAC: "96:00:00:0f:42:44", DHCP4: true,
			IPv6: netip.MustParsePrefix("2001:db8:1:3::/64"), Resolvers: resolvers,
			PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUser user@example"},
		},
		"private": {
			ID: 1000003, Name: "private", MAC: "96:00:00:0f:42:43", Resolvers: resolvers,
			PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUser user@example"},
		},
	}
	for name, server := range servers {
		files := Files(server)
		var text strings.Builder
		for _, file := range slices.Sorted(maps.Keys(files)) {
			text.WriteString("### " + file + "\n")
			text.Write(files[file])
		}
		path := filepath.Join("testdata", name+".golden")
		if *update {
			if err := os.WriteFile(path, []byte(text.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (go test -update writes it)", err)
		}
		if text.String() != string(want) {
			t.Errorf("%s differs from %s:\n%s", name, path, text.String())
		}
	}
}

// TestNameServersAreAlwaysInTheSeed: without them Ubuntu's cloud-init
// waits 120 s (measured on Hyper-V).
func TestNameServersAreAlwaysInTheSeed(t *testing.T) {
	config := string(Files(Server{ID: 1, Name: "x", MAC: "96:00:00:00:00:01", DHCP4: true, Resolvers: resolvers})["network-config"])
	if !strings.Contains(config, "198.18.0.2") || !strings.Contains(config, "2001:db8:0:ffff::2") {
		t.Errorf("network-config without the resolvers:\n%s", config)
	}
}

// The guest's helper reads a volume's ID and its automount flag from the
// WWN that hlabd made (system.VolumeDiskID); other disks get nothing.
func TestVolumeHelperReadsTheWWN(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh here; the guest's helper is a shell script")
	}
	_, rest, _ := strings.Cut(volumesScript, "cat > /usr/local/lib/hlab/hc-volume <<'SCRIPT'\n")
	helper, _, _ := strings.Cut(rest, "SCRIPT\n")
	path := filepath.Join(t.TempDir(), "hc-volume")
	if err := os.WriteFile(path, []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	for wwn, want := range map[string]string{
		"0x60022480484c41420100000001000005": "HC_VOLUME_ID=1000005\nHC_VOLUME_AUTOMOUNT=1\n",
		"0x60022480484c41420000000107411521": "HC_VOLUME_ID=107411521\nHC_VOLUME_AUTOMOUNT=0\n",
		"0x60022480e0557c23836128478a6b4488": "",
		"0x60022480484c4142":                 "",
	} {
		output, err := exec.Command("sh", path, wwn).Output()
		if err != nil || string(output) != want {
			t.Errorf("hc-volume %s = %q, %v; want %q", wwn, output, err, want)
		}
	}
}
