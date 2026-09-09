package investigate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"go.uber.org/zap"

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

// citePrompt asks for the links to be woven in, and nothing else.
//
// The model cannot do this on the first pass: HolmesGPT never shows it a tool's
// URL, so while it writes the analysis it does not know these pages exist.
//
// Deliberately not given the tool output again — only the analysis and the
// links. Deciding which page backs which claim needs the prose and the labels,
// not the payloads, and resending those would cost more than the investigation.
const citePrompt = `Below is an analysis you wrote, and the pages the tools opened while producing it.

Return the analysis again, unchanged except that each claim a page supports
gains an inline Markdown link to it, using the given label.

Rules:
- Change nothing else. Same sections, same wording, same order.
- Put the link at the END of the claim it supports, in parentheses:
  ` + "`- p99 latency crossed 2s at 14:03 ([check logs](https://...))`" + `
- A claim may carry more than one link, comma separated.
- Only link a page that genuinely backs that specific claim. A claim no page
  supports gets no link; guessing is worse than leaving it bare.
- Use ONLY the URLs listed below, character for character. Never edit one,
  never build a new one, never guess a path. A dead link during an incident
  costs more time than no link.
- Do not add a trailing list of links. They belong beside the claims.
- Return only the analysis. No preamble, no explanation of what you changed.`

// cite weaves the pages the tools opened into the analysis as inline links.
//
// Returns ok=false when the analysis should be left as written, in which case
// the caller appends a plain source list instead.
func (s *Service) cite(
	ctx context.Context, log *zap.Logger, analysis string, calls []holmes.ToolCall,
) (string, bool) {
	links := sources(calls)
	if len(links) == 0 || !s.cfg.CiteSources {
		return analysis, false
	}

	var b strings.Builder

	b.WriteString(citePrompt)
	b.WriteString("\n\n## Pages\n")

	for _, l := range links {
		fmt.Fprintf(&b, "- label %q, opened by %s: %s\n", linkLabel(l, calls), describeSource(l, calls), l)
	}

	b.WriteString("\n## Analysis\n")
	b.WriteString(analysis)

	answer, err := s.askWith(ctx, log, "", Prompt{Ask: b.String()})
	if err != nil {
		log.Warn("could not add source links; keeping the analysis as written", zap.Error(err))
		return analysis, false
	}

	cited := strings.TrimSpace(answer.Analysis)
	if !plausibleCitation(analysis, cited) {
		log.Warn("the citation pass returned something other than the analysis; discarding it")
		return analysis, false
	}

	// A link the tools never opened is worse than none: it looks authoritative
	// and goes nowhere. One invented URL discards the whole rewrite, because
	// there is no way to tell which of the others were also embellished.
	if invented := unknownURLs(cited, links); len(invented) > 0 {
		log.Warn("the citation pass invented URLs; discarding it",
			zap.Strings("invented", invented))

		return analysis, false
	}

	return cited, true
}

// linkLabel is the words a responder clicks. It comes from the tool that opened
// the page, because the tool is what decides whether this is a log query, a
// trace, or a dashboard.
func linkLabel(url string, calls []holmes.ToolCall) string {
	name := ""

	for _, c := range calls {
		if strings.TrimSpace(c.Result.URL) == url {
			name = strings.ToLower(c.ToolName)
			break
		}
	}

	switch {
	case strings.Contains(name, "loki"), strings.Contains(name, "log"):
		return "check logs"
	case strings.Contains(name, "tempo"), strings.Contains(name, "trace"):
		return "view traces"
	case strings.Contains(name, "search_dashboards"):
		return "browse dashboards"
	case strings.Contains(name, "dashboard"):
		return "open dashboard"
	case strings.Contains(name, "metric"), strings.Contains(name, "prometheus"):
		return "see metrics"
	default:
		return "open in Grafana"
	}
}

// urlPattern finds the http(s) URLs in a block of Markdown.
var urlPattern = regexp.MustCompile(`https?://[^\s)\]]+`)

// unknownURLs are the URLs in text that no tool actually returned.
func unknownURLs(text string, allowed []string) []string {
	known := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		known[a] = true
	}

	var out []string

	seen := map[string]bool{}

	for _, u := range urlPattern.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".,;")
		if known[u] || seen[u] {
			continue
		}

		seen[u] = true

		out = append(out, u)
	}

	return out
}

// describeSource names the tool a page came from, so the model can tell two
// dashboards apart without opening them.
func describeSource(url string, calls []holmes.ToolCall) string {
	for _, c := range calls {
		if strings.TrimSpace(c.Result.URL) != url {
			continue
		}

		if c.Description != "" {
			return c.Description
		}

		return c.ToolName
	}

	return "a tool"
}

// plausibleCitation rejects a rewrite that is not recognisably the original.
//
// The pass is asked to add links and change nothing else; a model that instead
// summarises, refuses, or answers a different question would otherwise silently
// replace a good analysis with a worse one.
//
// The comparison ignores URLs. A Grafana Explore link carries its whole query
// and time range — the better part of a kilobyte each — so counting them
// measured the links rather than the prose, and rejected exactly the specific,
// useful citations this is for.
func plausibleCitation(original, cited string) bool {
	if strings.TrimSpace(cited) == "" {
		return false
	}

	prose := func(s string) int { return len(urlPattern.ReplaceAllString(s, "")) }

	before := prose(original)
	if before == 0 {
		return true
	}

	// A label and a pair of brackets per claim, not a rewrite.
	ratio := float64(prose(cited)) / float64(before)

	return ratio >= 0.7 && ratio <= 1.6
}

// finalAnalysis is the text a responder reads: links woven in beside the claims
// they support where the citation pass worked, and a plain list of the pages
// appended where it did not.
func (s *Service) finalAnalysis(
	ctx context.Context, log *zap.Logger, answer *holmes.ChatResponse,
) string {
	if cited, ok := s.cite(ctx, log, answer.Analysis, answer.ToolCalls); ok {
		return cited
	}

	return withSources(answer.Analysis, answer.ToolCalls)
}
