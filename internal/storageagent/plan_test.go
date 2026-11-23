package storageagent_test

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/storageagent"
)

func TestStorageAgent(t *testing.T) {
	t.Run("SBOX-03/keep_data", testPlanKeepsData)
	t.Run("SBOX-03/keys", testAuthorizedKeys)
	t.Run("SBOX-13/commands", testPort23)
}

func box(user, ip string) agentapi.Box {
	return agentapi.Box{
		User:       user,
		IP:         netip.MustParseAddr(ip),
		QuotaBytes: 1 << 30,
	}
}

// boxesOf returns the sorted box users of the steps of kind.
func boxesOf(steps []storageagent.Step, kind storageagent.StepKind) []string {
	var out []string
	for _, s := range steps {
		if s.Kind == kind {
			out = append(out, s.Box)
		}
	}
	slices.Sort(out)
	return out
}

func testPlanKeepsData(t *testing.T) {
	two := agentapi.Storage{
		Schema:      agentapi.StorageSchema,
		Generation:  1,
		PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
		HostNet:     netip.MustParsePrefix("198.19.0.0/30"),
		Boxes:       []agentapi.Box{box("u100000", "198.19.0.20"), box("u100001", "198.19.0.21")},
	}
	one := two
	one.Generation = 2
	one.Boxes = []agentapi.Box{box("u100001", "198.19.0.21")}
	found := []string{"u100000", "u100001"}

	cases := []struct {
		name             string
		have             storageagent.State
		want             agentapi.Storage
		format           bool
		created, removed []string
	}{
		{
			name:    "first_apply",
			have:    storageagent.State{},
			want:    two,
			format:  true,
			created: []string{"u100000", "u100001"},
		},
		{
			name: "repeat_apply",
			have: storageagent.State{Filesystem: true, Boxes: found, Subvolumes: found},
			want: two,
		},
		{
			name:    "one_box_removed",
			have:    storageagent.State{Filesystem: true, Boxes: found, Subvolumes: found},
			want:    one,
			removed: []string{"u100000"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps := storageagent.Plan(c.have, c.want)
			formats := 0
			for i, s := range steps {
				if s.Kind == storageagent.StepFormat {
					formats++
					if i != 0 {
						t.Errorf("format is step %d, want it first: %+v", i, steps)
					}
				}
			}
			want := 0
			if c.format {
				want = 1
			}
			if formats != want {
				t.Errorf("format steps = %d, want %d: %+v", formats, want, steps)
			}
			if got := boxesOf(steps, storageagent.StepCreateBox); !slices.Equal(got, c.created) {
				t.Errorf("created boxes = %v, want %v", got, c.created)
			}
			if got := boxesOf(steps, storageagent.StepRemoveBox); !slices.Equal(got, c.removed) {
				t.Errorf("removed boxes = %v, want %v", got, c.removed)
			}
		})
	}
}
