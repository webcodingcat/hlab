package web_test

import (
	"cmp"
	"errors"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's pagination: page 1 and 25 per page by default and for invalid
// values, at most 50 per page, null where there is no page.
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
		"?page=9":                 {0, 0, 9, 25, 3, ptr(8), nil},
	} {
		page, meta := web.Paginate(httptest.NewRequest("GET", "/v1/servers"+query, nil), items)
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
	page, meta := web.Paginate(httptest.NewRequest("GET", "/v1/servers", nil), []int{})
	if len(page) != 0 || meta.Pagination.LastPage != 1 || meta.Pagination.NextPage != nil {
		t.Errorf("the page is %v, the pagination %+v", page, meta.Pagination)
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
		if err := web.Sort(httptest.NewRequest("GET", "/v1/servers"+query, nil), items, sortKeys); err != nil {
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

func TestSortRefusesUnknownFields(t *testing.T) {
	for _, query := range []string{"?sort=created", "?sort=name:up"} {
		err := web.Sort(httptest.NewRequest("GET", "/v1/servers"+query, nil), []item{}, sortKeys)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
			t.Errorf("%q: %v, want invalid_input", query, err)
		}
	}
}
