package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/labels"
)

// maxPerPage is Hetzner's largest page size.
const maxPerPage = 50

// ListOptions names the parameters one list endpoint offers besides
// page and per_page.
type ListOptions[T any] struct {
	Name    func(T) string                    // nil: endpoint has no name filter
	Labels  func(T) map[string]string         // nil: no label_selector
	Sort    map[string]func(a, b T) int       // documented sort fields ("id", "name", "created")
	Filters map[string]func(T, []string) bool // other documented filters ("status", "type", "fingerprint")
}

// ListRequest filters items by the request's name, label_selector and
// other documented filters, sorts them by the repeated sort=<field>[:asc|:desc]
// parameters in order, pages them (page default 1, per_page default 25,
// at most 50; invalid values fall back to the defaults) and writes the
// page under key with meta.pagination. An unknown sort field or a bad
// selector answers 422 invalid_input. items arrive ordered by ID.
func ListRequest[T any](w http.ResponseWriter, req *http.Request, key string, items []T, opt ListOptions[T]) {
	q := req.URL.Query()
	out, err := filterList(q, items, opt)
	if err != nil {
		Fail(w, err)
		return
	}
	if err := sortList(q["sort"], out, opt.Sort); err != nil {
		Fail(w, err)
		return
	}
	page := queryInt(q, "page", 1, 1, 0)
	perPage := queryInt(q, "per_page", defaultPerPage, 1, maxPerPage)
	total := len(out)
	lastPage := max(1, (total+perPage-1)/perPage)
	start := total // a page beyond the last is empty
	if page <= lastPage {
		start = (page - 1) * perPage
	}
	end := min(start+perPage, total)

	var previous, next any
	if page > 1 {
		previous = page - 1
	}
	if page < lastPage {
		next = page + 1
	}
	JSON(w, http.StatusOK, map[string]any{
		key: out[start:end],
		"meta": map[string]any{"pagination": map[string]any{
			"page":          page,
			"per_page":      perPage,
			"previous_page": previous,
			"next_page":     next,
			"last_page":     lastPage,
			"total_entries": total,
		}},
	})
}

// ListAllRequest is ListRequest for an endpoint without paging: it filters
// and sorts items alike and writes all of them under key, without meta.
func ListAllRequest[T any](w http.ResponseWriter, req *http.Request, key string, items []T, opt ListOptions[T]) {
	q := req.URL.Query()
	out, err := filterList(q, items, opt)
	if err != nil {
		Fail(w, err)
		return
	}
	if err := sortList(q["sort"], out, opt.Sort); err != nil {
		Fail(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{key: out})
}

// filterList returns a new slice with the items that pass every filter
// present in q.
func filterList[T any](q url.Values, items []T, opt ListOptions[T]) ([]T, error) {
	var sel *labels.Selector
	if s := q.Get("label_selector"); opt.Labels != nil && s != "" {
		parsed, err := labels.ParseSelector(s)
		if err != nil {
			return nil, &FieldError{Field: "label_selector", Message: err.Error()}
		}
		sel = &parsed
	}
	name, hasName := q.Get("name"), q.Has("name") && opt.Name != nil
	out := make([]T, 0, len(items))
	for _, it := range items {
		if hasName && opt.Name(it) != name {
			continue
		}
		if sel != nil && !sel.Matches(opt.Labels(it)) {
			continue
		}
		ok := true
		for param, match := range opt.Filters {
			if values, present := q[param]; present && !match(it, values) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// sortList sorts items stably by the sort parameters in order; a repeated
// field is ignored after its first use. Endpoints without sort fields
// ignore the parameter.
func sortList[T any](params []string, items []T, fields map[string]func(a, b T) int) error {
	if fields == nil || len(params) == 0 {
		return nil
	}
	var cmps []func(a, b T) int
	seen := map[string]bool{}
	for _, p := range params {
		field, dir, _ := strings.Cut(p, ":")
		cmp, ok := fields[field]
		if !ok || (dir != "" && dir != "asc" && dir != "desc") {
			return &FieldError{Field: "sort", Message: fmt.Sprintf("invalid sort %q", p)}
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		if dir == "desc" {
			asc := cmp
			cmp = func(a, b T) int { return -asc(a, b) }
		}
		cmps = append(cmps, cmp)
	}
	slices.SortStableFunc(items, func(a, b T) int {
		for _, c := range cmps {
			if r := c(a, b); r != 0 {
				return r
			}
		}
		return 0
	})
	return nil
}

// queryInt reads the integer parameter name; a missing or invalid value
// (not a number, below lo, above hi when hi > 0) gives def.
func queryInt(q url.Values, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(q.Get(name))
	if err != nil || v < lo || (hi > 0 && v > hi) {
		return def
	}
	return v
}
