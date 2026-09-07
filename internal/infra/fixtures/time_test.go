package fixtures

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		"empty defaults to base":  {"", base, false},
		"relative past":           {"-15m", base.Add(-15 * time.Minute), false},
		"relative compound":       {"-2h30m", base.Add(-150 * time.Minute), false},
		"relative future":         {"+1h", base.Add(time.Hour), false},
		"absolute rfc3339":        {"2021-08-17T13:28:57Z", time.Date(2021, 8, 17, 13, 28, 57, 0, time.UTC), false},
		"relative days":           {"-3d", base.Add(-72 * time.Hour), false},
		"relative days and hours": {"-3d12h", base.Add(-84 * time.Hour), false},
		"garbage":                 {"yesterday", time.Time{}, true},
		"bad duration":            {"-15 minutes", time.Time{}, true},
	} {
		got, err := parseTime(tc.in, base)

		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: want an error for %q, got %v", name, tc.in, got)
			}

			continue
		}

		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}

		if !got.Equal(tc.want) {
			t.Errorf("%s: got %s, want %s", name, got, tc.want)
		}
	}
}

func TestNewIDsSortChronologically(t *testing.T) {
	t.Parallel()

	// The store pages by ID order and calls it chronological order, so minted
	// IDs must actually sort that way.
	first := NewID()
	time.Sleep(2 * time.Millisecond)
	second := NewID()

	if first >= second {
		t.Fatalf("IDs must sort by creation time: %q should sort before %q", first, second)
	}

	if len(first) != 26 {
		t.Errorf("want a 26-character ULID, got %d chars (%q)", len(first), first)
	}
}
