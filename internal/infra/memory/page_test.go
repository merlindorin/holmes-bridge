package memory

import (
	"testing"

	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

type rec struct{ id string }

func recs(ids ...string) []rec {
	out := make([]rec, 0, len(ids))
	for _, id := range ids {
		out = append(out, rec{id})
	}

	return out
}

func ids(items []rec) []string {
	out := make([]string, 0, len(items))
	for _, r := range items {
		out = append(out, r.id)
	}

	return out
}

func key(r rec) string { return r.id }

func TestPaginateWalksEveryRecordExactlyOnce(t *testing.T) {
	t.Parallel()

	all := recs("01A", "01B", "01C", "01D", "01E")

	var (
		seen  []string
		after string
	)

	for range 10 { // bounded so a cursor bug fails instead of hanging
		page, next := paginate(all, key, incidents.Page{After: after, PageSize: 2})
		seen = append(seen, ids(page)...)

		if next == "" {
			break
		}

		after = next
	}

	want := []string{"01A", "01B", "01C", "01D", "01E"}
	if len(seen) != len(want) {
		t.Fatalf("walked %v, want %v", seen, want)
	}

	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("walked %v, want %v", seen, want)
		}
	}
}

func TestPaginateStopsOnTheLastPage(t *testing.T) {
	t.Parallel()

	// Exactly one page of records must not advertise a cursor, or a client
	// loops forever fetching an empty final page.
	_, after := paginate(recs("01A", "01B"), key, incidents.Page{PageSize: 2})
	if after != "" {
		t.Fatalf("a full final page should not advertise a cursor, got %q", after)
	}
}

func TestPaginateEmptyCollection(t *testing.T) {
	t.Parallel()

	page, after := paginate(nil, key, incidents.Page{PageSize: 25})
	if len(page) != 0 || after != "" {
		t.Fatalf("empty collection: got %d records and cursor %q", len(page), after)
	}
}

func TestPaginateUnknownCursorYieldsEmptyPage(t *testing.T) {
	t.Parallel()

	// A cursor past the end (its record was deleted, say) must terminate the
	// walk rather than restart it from the beginning.
	page, after := paginate(recs("01A", "01B"), key, incidents.Page{After: "01Z", PageSize: 2})
	if len(page) != 0 || after != "" {
		t.Fatalf("cursor past the end: got %v and cursor %q", ids(page), after)
	}
}

func TestPaginateClampsPageSize(t *testing.T) {
	t.Parallel()

	all := make([]rec, 0, 300)
	for i := range 300 {
		all = append(all, rec{id: string(rune('A'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))})
	}

	page, _ := paginate(all, key, incidents.Page{PageSize: 9999})
	if len(page) != incidents.MaxPageSize {
		t.Errorf("oversized page_size should clamp to %d, got %d", incidents.MaxPageSize, len(page))
	}

	page, _ = paginate(all, key, incidents.Page{PageSize: 0})
	if len(page) != incidents.DefaultPageSize {
		t.Errorf("omitted page_size should default to %d, got %d", incidents.DefaultPageSize, len(page))
	}

	page, _ = paginate(all, key, incidents.Page{PageSize: -5})
	if len(page) != incidents.DefaultPageSize {
		t.Errorf("negative page_size should default to %d, got %d", incidents.DefaultPageSize, len(page))
	}
}
