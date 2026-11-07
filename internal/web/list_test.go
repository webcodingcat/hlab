package web_test

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's pagination (measured 2026-11-07): page 1 and 25 per page by
// default and for invalid values, at most 50 per page, null where there is no
// page, and a page beyond the last has no previous page either.
func TestPaginate(t *testing.T) {
	items := make([]int, 60)
	for i := range items {
		items[i] = i + 1
	}
	for query, want := range map[string]struct {
		first, length, page, perPage, last int
		previous, next                     *int
	}{
		"":                        {1, 25, 1, 25, 3, nil, ptr(2)},
		"?page=2&per_page=10":     {11, 10, 2, 10, 6, ptr(1), ptr(3)},
		"?page=3":                 {51, 10, 3, 25, 3, ptr(2), nil},
		"?page=0&per_page=-5":     {1, 25, 1, 25, 3, nil, ptr(2)},
		"?page=abc&per_page=1000": {1, 50, 1, 50, 2, nil, ptr(2)},
		"?page=9":                 {0, 0, 9, 25, 3, nil, nil},
		// not measured: the lab only must not overflow
		"?page=9223372036854775807": {0, 0, 9223372036854775807, 25, 3, nil, nil},
	} {
		page, meta := web.Paginate(newRequest("/v1/servers"+query), items)
		pagination := meta.Pagination
		if len(page) != want.length || (want.length > 0 && page[0] != want.first) {
			t.Errorf("%q: the page is %v", query, page)
		}
		if pagination.Page != want.page || pagination.PerPage != want.perPage || pagination.LastPage != want.last ||
			pagination.TotalEntries != 60 || !equalPtr(pagination.PreviousPage, want.previous) ||
			!equalPtr(pagination.NextPage, want.next) {
			t.Errorf("%q: the pagination is %+v", query, pagination)
		}
	}
}

func TestPaginateAnEmptyList(t *testing.T) {
	page, meta := web.Paginate(newRequest("/v1/servers"), []int{})
	if len(page) != 0 || meta.Pagination.LastPage != 1 || meta.Pagination.NextPage != nil {
		t.Errorf("the page is %v, the pagination %+v", page, meta.Pagination)
	}
}

// Behind the lab control, http.StripPrefix cuts the path; the links keep
// the path the client sent.
func TestPaginateLinksThePathTheClientSent(t *testing.T) {
	req := newRequest("/_lab/projects/default/cloud/v1/servers?page=2")
	req.URL.Path = "/v1/servers" // as http.StripPrefix leaves it
	web.Paginate(req, make([]int, 60))
	want := `<http://example.com/_lab/projects/default/cloud/v1/servers?page=1>; rel="prev"`
	if link := req.Header.Get("Link"); !strings.HasPrefix(link, want) {
		t.Errorf("Link is\n%s\nwant it to start with\n%s", link, want)
	}
}

// Hetzner still sends the Link header it announced to drop: the previous,
// next and last page, with the rest of the query.
func TestPaginateSetsTheLinkHeader(t *testing.T) {
	for query, want := range map[string]string{
		"?page=2&per_page=20&sort=id%3Adesc": `<http://example.com/v1/servers?page=1&per_page=20&sort=id%3Adesc>; rel="prev", ` +
			`<http://example.com/v1/servers?page=3&per_page=20&sort=id%3Adesc>; rel="next", ` +
			`<http://example.com/v1/servers?page=3&per_page=20&sort=id%3Adesc>; rel="last"`,
		"?page=0&per_page=1000": `<http://example.com/v1/servers?page=2&per_page=1000>; rel="next", ` +
			`<http://example.com/v1/servers?page=2&per_page=1000>; rel="last"`,
		"?page=99": `<http://example.com/v1/servers?page=3>; rel="last"`,
	} {
		req := newRequest("/v1/servers" + query)
		web.Paginate(req, make([]int, 60))
		if link := req.Header.Get("Link"); link != want {
			t.Errorf("%q: Link is\n%s\nwant\n%s", query, link, want)
		}
	}
}

func ptr(n int) *int { return &n }

func equalPtr(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// item is a listed resource of the sort tests.
type item struct {
	id   int
	name string
}

var sortKeys = web.SortKeys[item]{
	"id":   func(a, b item) int { return cmp.Compare(a.id, b.id) },
	"name": func(a, b item) int { return cmp.Compare(a.name, b.name) },
}

// Hetzner sorts by several fields in the order given and ignores contrary
// orders of one field.
func TestSort(t *testing.T) {
	for query, want := range map[string][]int{
		"":                                  {1, 2, 3, 4},
		"?sort=name":                        {2, 4, 1, 3},
		"?sort=name:desc":                   {1, 3, 2, 4},
		"?sort=name:desc&sort=id:desc":      {3, 1, 4, 2},
		"?sort=id:asc&sort=id:desc":         {1, 2, 3, 4},
		"?sort=name&sort=name&sort=id:desc": {4, 2, 3, 1},
	} {
		items := []item{{1, "web"}, {2, "db"}, {3, "web"}, {4, "db"}}
		if err := web.Sort(newRequest("/v1/servers"+query), items, sortKeys); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		var ids []int
		for _, sorted := range items {
			ids = append(ids, sorted.id)
		}
		if !slices.Equal(ids, want) {
			t.Errorf("%q sorts to %v, want %v", query, ids, want)
		}
	}
}

// Hetzner's answer to a sort value it does not know (measured 2026-11-07).
func TestSortRefusesUnknownFields(t *testing.T) {
	for query, value := range map[string]string{"?sort=created": "created", "?sort=name:up": "name:up"} {
		err := web.Sort(newRequest("/v1/servers"+query), []item{}, sortKeys)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" || apiErr.Message != "invalid sort value" {
			t.Fatalf("%q: %v, want invalid_input", query, err)
		}
		details, _ := json.Marshal(apiErr.Details)
		if want := `{"fields":[{"name":"sort","messages":["value '` + value + `' is not allowed"]}]}`; string(details) != want {
			t.Errorf("%q: the details are %s, want %s", query, details, want)
		}
	}
}
