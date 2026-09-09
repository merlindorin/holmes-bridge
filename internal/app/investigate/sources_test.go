package investigate

import (
	"strings"
	"testing"

	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
)

func call(url string) holmes.ToolCall {
	return holmes.ToolCall{Result: holmes.ToolCallResult{URL: url}}
}

func TestWithSourcesAppendsTheLinksTheToolsReturned(t *testing.T) {
	t.Parallel()

	got := withSources("**Summary**\nBroken.", []holmes.ToolCall{
		call("https://grafana.example.com/d/abc"),
	})

	if !strings.Contains(got, sourcesHeading) {
		t.Errorf("the analysis should gain a Sources section:\n%s", got)
	}

	if !strings.Contains(got, "https://grafana.example.com/d/abc") {
		t.Errorf("the link should be listed:\n%s", got)
	}

	if !strings.HasPrefix(got, "**Summary**\nBroken.") {
		t.Errorf("the analysis itself must come through unchanged:\n%s", got)
	}
}

func TestWithSourcesLeavesAnalysisAloneWhenNoToolReturnedAURL(t *testing.T) {
	t.Parallel()

	// Most tools return no URL at all — kubectl, bash, logs. An empty Sources
	// heading would be noise on every analysis.
	analysis := "**Summary**\nBroken."
	for name, calls := range map[string][]holmes.ToolCall{
		"no calls":    nil,
		"no urls":     {call(""), call("   ")},
		"only blanks": {{}, {}},
	} {
		if got := withSources(analysis, calls); got != analysis {
			t.Errorf("%s: analysis should be untouched, got:\n%s", name, got)
		}
	}
}

func TestSourcesDedupesAndOrders(t *testing.T) {
	t.Parallel()

	// One investigation calls the same tool repeatedly; the same dashboard URL
	// listed eight times is worse than no list.
	got := sources([]holmes.ToolCall{
		call("https://b.example.com"),
		call("https://a.example.com"),
		call("https://b.example.com"),
	})

	if len(got) != 2 || got[0] != "https://a.example.com" || got[1] != "https://b.example.com" {
		t.Errorf("want two links in a stable order, got %v", got)
	}
}

func TestPlausibleCitationRejectsARewrite(t *testing.T) {
	t.Parallel()

	original := strings.Repeat("a bullet of evidence. ", 20)

	for name, tc := range map[string]struct {
		cited string
		want  bool
	}{
		"markers added":   {"[0] " + original, true},
		"empty":           {"", false},
		"refused":         {"I cannot help with that.", false},
		"summarised away": {"Everything is broken.", false},
		"essay instead":   {strings.Repeat(original, 3), false},
	} {
		if got := plausibleCitation(original, tc.cited); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestDescribeSourceNamesTheTool(t *testing.T) {
	t.Parallel()

	calls := []holmes.ToolCall{{
		ToolName:    "grafana_search_dashboards",
		Description: "Search dashboards for otel",
		Result:      holmes.ToolCallResult{URL: "https://a.example.com"},
	}}

	if got := describeSource("https://a.example.com", calls); got != "Search dashboards for otel" {
		t.Errorf("got %q", got)
	}

	// A URL from no known call still needs a label rather than an empty one.
	if got := describeSource("https://unknown.example.com", calls); got != "a tool" {
		t.Errorf("unknown url should fall back, got %q", got)
	}
}

func TestUnknownURLsCatchesAnInventedLink(t *testing.T) {
	t.Parallel()

	allowed := []string{"https://grafana.example.com/explore?a=1"}

	original := "- latency rose"

	// The real one, cited correctly, is not flagged.
	text := "- latency rose ([check logs](https://grafana.example.com/explore?a=1))"
	if got := unknownURLs(original, text, allowed); len(got) != 0 {
		t.Errorf("a returned URL should be accepted, got %v", got)
	}

	// A URL the tools never opened looks authoritative and goes nowhere.
	text = "- latency rose ([check logs](https://grafana.example.com/d/made-up))"
	if got := unknownURLs(original, text, allowed); len(got) != 1 {
		t.Errorf("an invented URL should be caught, got %v", got)
	}

	// A URL the analysis itself quoted — the request path from a log line — is
	// the model reporting evidence, not citing a page.
	quoted := "- 403 on `https://www.example.com/ws/listings/`"
	cited := quoted + " ([check logs](https://grafana.example.com/explore?a=1))"

	if got := unknownURLs(quoted, cited, allowed); len(got) != 0 {
		t.Errorf("a URL already in the analysis should not be flagged, got %v", got)
	}
}

func TestLinkLabelNamesWhatTheResponderWillSee(t *testing.T) {
	t.Parallel()

	for tool, want := range map[string]string{
		"fetch_loki_logs":           "check logs",
		"tempo_search_traces_by_id": "view traces",
		"grafana_search_dashboards": "browse dashboards",
		"grafana_get_dashboard":     "open dashboard",
		"something_else":            "open in Grafana",
	} {
		calls := []holmes.ToolCall{{
			ToolName: tool,
			Result:   holmes.ToolCallResult{URL: "https://a.example.com"},
		}}

		if got := linkLabel("https://a.example.com", calls); got != want {
			t.Errorf("%s: got %q, want %q", tool, got, want)
		}
	}
}

func TestPlausibleCitationIgnoresURLLength(t *testing.T) {
	t.Parallel()

	// A Grafana Explore link carries its whole query and time range. Four of
	// them inline can outweigh the analysis itself, which must not look like a
	// rewrite — this is the case the guard exists to allow, not reject.
	original := strings.Repeat("a bullet of evidence. ", 20)
	huge := "https://grafana.example.com/explore?panes=" + strings.Repeat("x", 900)
	cited := original + " ([check logs](" + huge + "))"

	if !plausibleCitation(original, cited) {
		t.Error("adding a very long URL should not look like a rewrite")
	}

	// Prose actually replaced is still caught, however long the URLs are.
	if plausibleCitation(original, "Everything is fine. ([check logs]("+huge+"))") {
		t.Error("a summarised-away analysis should still be rejected")
	}
}
