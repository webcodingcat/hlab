package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resource"
)

// listKey is an SSH key in Hetzner's response shape.
type listKey struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Fingerprint string            `json:"fingerprint"`
	PublicKey   string            `json:"public_key"`
	Labels      map[string]string `json:"labels"`
	Created     string            `json:"created"`
}

// listModule serves GET /ssh_keys through api.ListRequest over n keys with
// IDs 1 to n.
type listModule struct{ n int }

func (listModule) Type() string { return "ssh_keys" }

func (m listModule) Routes(r resource.Registrar) {
	keys := make([]listKey, m.n)
	for i := range keys {
		keys[i] = listKey{
			ID:          int64(i + 1),
			Name:        fmt.Sprintf("key-%03d", i+1),
			Fingerprint: "b7:2f:30:a0:2f:6c:58:6c:21:04:58:61:ba:06:3b:2f",
			PublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIListKeyForTestsOnly list",
			Labels:      map[string]string{},
			Created:     "2026-11-18T00:00:00Z",
		}
	}
	r.Handle("GET /ssh_keys", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		api.ListRequest(w, req, "ssh_keys", keys, api.ListOptions[listKey]{})
	}))
}

// testList is TestAPI/API-05.
func testList(t *testing.T) {
	const total = 120
	env := apitest.New(t, listModule{n: total})

	// lastPage marks the cases on the last page: the description allows
	// last_page to be null there.
	cases := []struct {
		name     string
		query    string
		page     int
		perPage  int
		first    int64 // ID of the first item; 0: no items
		count    int
		previous *int
		next     *int
		lastPage bool
	}{
		{"default", "", 1, 25, 1, 25, nil, ptr(2), false},
		{"maximum", "?per_page=50", 1, 50, 1, 50, nil, ptr(2), false},
		{"per_page=51", "?per_page=51", 1, 25, 1, 25, nil, ptr(2), false},
		{"per_page=0", "?per_page=0", 1, 25, 1, 25, nil, ptr(2), false},
		{"page=0", "?page=0", 1, 25, 1, 25, nil, ptr(2), false},
		{"middle", "?page=3", 3, 25, 51, 25, ptr(2), ptr(4), false},
		{"last", "?page=5", 5, 25, 101, 20, ptr(4), nil, true},
		{"last with 50", "?page=3&per_page=50", 3, 50, 101, 20, ptr(2), nil, true},
		{"beyond the last", "?page=6", 6, 25, 0, 0, nil, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := env.For(t).Do(http.MethodGet, "/ssh_keys"+c.query, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200", resp.StatusCode)
			}
			var body struct {
				SSHKeys []listKey `json:"ssh_keys"`
				Meta    struct {
					Pagination struct {
						Page         int  `json:"page"`
						PerPage      int  `json:"per_page"`
						PreviousPage *int `json:"previous_page"`
						NextPage     *int `json:"next_page"`
						LastPage     *int `json:"last_page"`
						TotalEntries *int `json:"total_entries"`
					} `json:"pagination"`
				} `json:"meta"`
			}
			apitest.Decode(t, resp, &body)
			p := body.Meta.Pagination

			if len(body.SSHKeys) != c.count {
				t.Errorf("%d items, want %d", len(body.SSHKeys), c.count)
			}
			if c.count > 0 && len(body.SSHKeys) > 0 && body.SSHKeys[0].ID != c.first {
				t.Errorf("first item ID %d, want %d", body.SSHKeys[0].ID, c.first)
			}
			if p.Page != c.page || p.PerPage != c.perPage {
				t.Errorf("page %d per_page %d, want %d and %d", p.Page, p.PerPage, c.page, c.perPage)
			}
			if c.name != "beyond the last" && !sameInt(p.PreviousPage, c.previous) {
				t.Errorf("previous_page %s, want %s", show(p.PreviousPage), show(c.previous))
			}
			if !sameInt(p.NextPage, c.next) {
				t.Errorf("next_page %s, want %s", show(p.NextPage), show(c.next))
			}
			wantLast := (total + c.perPage - 1) / c.perPage
			if p.LastPage == nil {
				if !c.lastPage {
					t.Errorf("last_page null, want %d", wantLast)
				}
			} else if *p.LastPage != wantLast {
				t.Errorf("last_page %d, want %d", *p.LastPage, wantLast)
			}
			if p.TotalEntries == nil || *p.TotalEntries != total {
				t.Errorf("total_entries %s, want %d", show(p.TotalEntries), total)
			}
		})
	}
}

func ptr(i int) *int { return &i }

func sameInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func show(p *int) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprint(*p)
}
