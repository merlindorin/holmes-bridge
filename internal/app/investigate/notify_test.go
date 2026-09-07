package investigate

import "testing"

// headline is unexported, and it is the piece that decides what a responder
// actually sees on a lock screen — worth testing directly.
func TestHeadlineExtractsTheSummarySection(t *testing.T) {
	t.Parallel()

	analysis := `**Summary**
The connection pool is exhausted; roughly 7% of requests are failing.

**Evidence**
- pg_stat_activity shows 98 of 100 connections in use.
- checkout-api v2.31.0 rolled out 8 minutes before the alert.

**Most likely cause**
The deploy lowered the pool ceiling.`

	got := headline(analysis)

	if got != "The connection pool is exhausted; roughly 7% of requests are failing." {
		t.Errorf("got %q", got)
	}
}

func TestHeadlineFallsBackWhenTheModelIgnoredTheStructure(t *testing.T) {
	t.Parallel()

	// The system prompt asks for a Summary section, but a model can always
	// ignore it. A truncated answer beats an empty notification.
	got := headline("Everything is on fire and I could not work out why.")
	if got != "Everything is on fire and I could not work out why." {
		t.Errorf("got %q", got)
	}
}

func TestHeadlineHandlesEmptyAnalysis(t *testing.T) {
	t.Parallel()

	if got := headline("   \n  "); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestHeadlineCollapsesBlankLines(t *testing.T) {
	t.Parallel()

	got := headline("**Summary**\n\n\nLine one.\n\n\n\nLine two.\n\n**Evidence**\n- x")
	if got != "Line one.\n\nLine two." {
		t.Errorf("got %q", got)
	}
}
