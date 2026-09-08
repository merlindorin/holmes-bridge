package investigate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
)

// sourcesHeading is the section appended to an analysis. It is a separate
// section rather than inline links because the bridge cannot know which claim a
// given URL supports — only that the investigation looked there.
const sourcesHeading = "**Sources**"

// withSources appends the browsable URLs the investigation actually opened.
//
// HolmesGPT computes these — a Grafana dashboard, a trace, a log query — but
// never shows them to the model: the tool output an LLM sees is the stringified
// data alone, and the URL travels beside it in a field only the API client
// receives. So no amount of prompting can make the model cite them, and asking
// it to would invite invented links. They are recovered here instead, where
// they are known to be real.
func withSources(analysis string, calls []holmes.ToolCall) string {
	links := sources(calls)
	if len(links) == 0 {
		return analysis
	}

	var b strings.Builder

	b.WriteString(strings.TrimRight(analysis, "\n"))
	b.WriteString("\n\n")
	b.WriteString(sourcesHeading)
	b.WriteString("\n\n")

	// The visible list. Bare URLs autolink in GitHub-flavoured Markdown, which
	// is what incident.io renders, so the entry is clickable as it stands.
	for i, l := range links {
		fmt.Fprintf(&b, "- [%d] %s\n", i, l)
	}

	// Link definitions, so a bare [0] anywhere in the prose resolves to the
	// same URL. Renderers hide these lines; they only exist to make the
	// reference syntax work.
	b.WriteString("\n")

	for i, l := range links {
		fmt.Fprintf(&b, "[%d]: %s\n", i, l)
	}

	return strings.TrimRight(b.String(), "\n")
}

// sources are the distinct URLs across a set of tool calls, in a stable order.
//
// One investigation calls the same tool repeatedly — several dashboard
// searches, a trace lookup per service — and a list repeating one URL eight
// times is worse than no list.
func sources(calls []holmes.ToolCall) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(calls))

	for _, c := range calls {
		url := strings.TrimSpace(c.Result.URL)
		if url == "" || seen[url] {
			continue
		}

		seen[url] = true

		out = append(out, url)
	}

	sort.Strings(out)

	return out
}
