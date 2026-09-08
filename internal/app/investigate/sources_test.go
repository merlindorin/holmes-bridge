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

func TestWithSourcesNumbersFromZeroAndDefinesReferences(t *testing.T) {
	t.Parallel()

	got := withSources("**Summary**\nBroken.", []holmes.ToolCall{
		call("https://a.example.com"),
		call("https://b.example.com"),
	})

	// The visible list a reader scans.
	for _, want := range []string{"- [0] https://a.example.com", "- [1] https://b.example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// The definitions, which renderers hide, so a bare [0] in the prose links.
	for _, want := range []string{"[0]: https://a.example.com", "[1]: https://b.example.com"} {
		if !strings.Contains(got, "\n"+want) {
			t.Errorf("missing link definition %q in:\n%s", want, got)
		}
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
	if got := describeSource("https://unknown.example.com", calls); got != "source" {
		t.Errorf("unknown url should fall back, got %q", got)
	}
}
