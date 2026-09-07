package fixtures

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dayPattern matches a leading day component, which time.ParseDuration does not
// understand but which is the natural unit for anything older than a shift.
var dayPattern = regexp.MustCompile(`^(\d+)d`)

// parseTime resolves a fixture timestamp.
//
// Absolute RFC3339 values are taken as written. Relative values like "-15m",
// "-2h30m" or "-3d12h" are resolved against the moment the fixture loads, which
// keeps a scenario's incidents recent however long ago the file was written —
// the alternative is fixtures that silently drift into looking stale.
func parseTime(value string, base time.Time) (time.Time, error) {
	if value == "" {
		return base, nil
	}

	if sign, rest, ok := relative(value); ok {
		d, err := parseDuration(rest)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid relative time %q: %w", value, err)
		}

		return base.Add(time.Duration(sign) * d), nil
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"invalid timestamp %q (want RFC3339 or a relative offset like -15m or -3d): %w", value, err)
	}

	return t.UTC(), nil
}

//nolint:nonamedreturns // the three unnamed results would be unreadable here
func relative(value string) (sign int, rest string, ok bool) {
	switch {
	case strings.HasPrefix(value, "-"):
		return -1, value[1:], true
	case strings.HasPrefix(value, "+"):
		return 1, value[1:], true
	default:
		return 0, "", false
	}
}

// parseDuration extends time.ParseDuration with a leading day component, so
// "3d", "3d12h" and "90m" all work.
func parseDuration(value string) (time.Duration, error) {
	var days time.Duration

	if m := dayPattern.FindStringSubmatch(value); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: %w", m[1], err)
		}

		days = time.Duration(n) * 24 * time.Hour
		value = value[len(m[0]):]
	}

	if value == "" {
		return days, nil
	}

	rest, err := time.ParseDuration(value)
	if err != nil {
		return 0, err //nolint:wrapcheck // the caller adds the offending value
	}

	return days + rest, nil
}
