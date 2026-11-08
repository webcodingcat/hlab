package seed

import (
	"flag"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write the golden files anew")

var resolvers = []netip.Addr{netip.MustParseAddr("198.18.0.2"), netip.MustParseAddr("2001:db8:0:ffff::2")}

// TestFilesMatchTheGoldenFiles covers a server with both addresses and SSH
// keys and one with IPv4 only, a password and user data.
func TestFilesMatchTheGoldenFiles(t *testing.T) {
	servers := map[string]Server{
		"web": {
			ID: 1000001, Name: "web", MAC: "96:00:00:0f:42:41", IPv4: true,
			IPv6: netip.MustParsePrefix("2001:db8:1:1::/64"), Resolvers: resolvers,
			PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUser user@example"},
		},
		"db": {
			ID: 1000002, Name: "db", MAC: "96:00:00:0f:42:42", IPv4: true, Resolvers: resolvers,
			RootPassword: `p"ss`, UserData: "#cloud-config\npackages: [nginx]\n",
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
	config := string(Files(Server{ID: 1, Name: "x", MAC: "96:00:00:00:00:01", IPv4: true, Resolvers: resolvers})["network-config"])
	if !strings.Contains(config, "198.18.0.2") || !strings.Contains(config, "2001:db8:0:ffff::2") {
		t.Errorf("network-config without the resolvers:\n%s", config)
	}
}
