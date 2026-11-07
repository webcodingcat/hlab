package web

import (
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Hetzner's page sizes.
const (
	defaultPerPage = 25
	maxPerPage     = 50
)

// Meta is the "meta" of a list answer.
type Meta struct {
	Pagination Pagination `json:"pagination"`
}

// Pagination is Hetzner's meta.pagination. It is HLab's own type because
// hcloud-go's schema.MetaPagination cannot send null, which the real API
// sends for the pages that are not there.
type Pagination struct {
	Page         int  `json:"page"`
	PerPage      int  `json:"per_page"`
	PreviousPage *int `json:"previous_page"`
	NextPage     *int `json:"next_page"`
	LastPage     int  `json:"last_page"`
	TotalEntries int  `json:"total_entries"`
}

// Paginate returns the page of items that the query's page and per_page ask
// for, and the meta of the answer. As on Hetzner, invalid values fall back to
// page 1 and 25 per page, and per_page is at most 50.
func Paginate[T any](r *http.Request, items []T) ([]T, Meta) {
	query := r.URL.Query()
	page := positiveOr(query.Get("page"), 1)
	perPage := min(positiveOr(query.Get("per_page"), defaultPerPage), maxPerPage)
	total := len(items)
	last := max(1, (total+perPage-1)/perPage)

	pagination := Pagination{Page: page, PerPage: perPage, LastPage: last, TotalEntries: total}
	if page > 1 {
		previous := page - 1
		pagination.PreviousPage = &previous
	}
	if page < last {
		next := page + 1
		pagination.NextPage = &next
	}
	start := min((page-1)*perPage, total)
	end := min(start+perPage, total)
	return items[start:end], Meta{Pagination: pagination}
}

// positiveOr returns a query value as a number of at least 1, else fallback.
func positiveOr(value string, fallback int) int {
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return fallback
	}
	return number
}

// SortKeys are the fields a list may be sorted by (Hetzner's sort values
// without ":asc" or ":desc"), each with a comparison of two items in
// ascending order.
type SortKeys[T any] map[string]func(a, b T) int

// Sort sorts items by the query's sort parameters ("name", "id:desc"),
// several in the order given. A field asked for in both directions is
// ignored, as Hetzner ignores contrary orders. A field that keys lacks is an
// invalid_input error. Items that are equal keep their order, so a list
// sorted by ID stays so among equals.
func Sort[T any](r *http.Request, items []T, keys SortKeys[T]) error {
	type order struct {
		compare    func(a, b T) int
		descending bool
	}
	var fields []string
	directions := map[string]string{}
	for _, value := range r.URL.Query()["sort"] {
		field, direction, _ := strings.Cut(value, ":")
		if direction == "" {
			direction = "asc"
		}
		if _, known := keys[field]; !known || (direction != "asc" && direction != "desc") {
			return InvalidField("sort", "must be one of "+strings.Join(sortValues(keys), ", "))
		}
		switch previous, seen := directions[field]; {
		case !seen:
			fields = append(fields, field)
			directions[field] = direction
		case previous != direction:
			directions[field] = "contrary"
		}
	}
	var orders []order
	for _, field := range fields {
		if directions[field] != "contrary" {
			orders = append(orders, order{compare: keys[field], descending: directions[field] == "desc"})
		}
	}
	slices.SortStableFunc(items, func(a, b T) int {
		for _, o := range orders {
			result := o.compare(a, b)
			if o.descending {
				result = -result
			}
			if result != 0 {
				return result
			}
		}
		return 0
	})
	return nil
}

// sortValues returns the sort values that keys allow, as the spec lists them.
func sortValues[T any](keys SortKeys[T]) []string {
	var values []string
	for field := range keys {
		values = append(values, field, field+":asc", field+":desc")
	}
	sort.Strings(values)
	return values
}
