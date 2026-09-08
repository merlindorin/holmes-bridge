package investigate

import (
	"context"
	"fmt"
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

// citePrompt asks for the markers to be inserted, and nothing else.
//
// The model cannot cite sources on the first pass: HolmesGPT never shows it a
// tool's URL, so at the moment it writes the analysis it does not know a source
// list exists, let alone how it is numbered. This second pass hands it both.
//
// Deliberately not given the tool output again — only the analysis and the
// list. Deciding which source backs which bullet needs the prose and the tool
// names, not the payloads, and resending those would cost more than the
// investigation did.
const citePrompt = `Below is an analysis you wrote, and the numbered sources it drew on.

Return the analysis again, unchanged except that each bullet or claim supported
by a source is prefixed with its reference marker: [0], [1], and so on.

Rules:
- Change nothing else. Same sections, same wording, same order.
- A marker goes at the START of the bullet it supports, before the text.
- Only cite a source that genuinely backs that specific claim. A bullet no
  source supports keeps no marker; guessing is worse than leaving it bare.
- Cite only the numbers listed below. Never invent one.
- Return only the analysis. No preamble, no explanation of what you changed.`

// cite runs the marker-insertion pass, falling back to the original analysis.
//
// Every failure here is non-fatal by design. The analysis is already correct
// and already carries its sources; markers are a readability nicety, and losing
// them is not worth failing an investigation somebody is waiting on.
func (s *Service) cite(
	ctx context.Context, log *zap.Logger, analysis string, calls []holmes.ToolCall,
) string {
	links := sources(calls)
	if len(links) == 0 || !s.cfg.CiteSources {
		return analysis
	}

	var b strings.Builder

	b.WriteString(citePrompt)
	b.WriteString("\n\n## Sources\n")

	for i, l := range links {
		fmt.Fprintf(&b, "[%d] %s — %s\n", i, describeSource(l, calls), l)
	}

	b.WriteString("\n## Analysis\n")
	b.WriteString(analysis)

	answer, err := s.askWith(ctx, log, "", Prompt{Ask: b.String()})
	if err != nil {
		log.Warn("could not add source markers; keeping the analysis as written",
			zap.Error(err))

		return analysis
	}

	cited := strings.TrimSpace(answer.Analysis)
	if !plausibleCitation(analysis, cited) {
		log.Warn("the citation pass returned something other than the analysis; discarding it")

		return analysis
	}

	return cited
}

// describeSource names the tool a URL came from, so the model can tell two
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

	return "source"
}

// plausibleCitation rejects a rewrite that is not recognisably the original.
//
// The pass is asked to add markers and change nothing else; a model that
// instead summarises, refuses, or answers a different question would otherwise
// silently replace a good analysis with a worse one.
func plausibleCitation(original, cited string) bool {
	if cited == "" {
		return false
	}

	// Length is a blunt but effective check: markers add a few characters per
	// bullet, so anything far shorter or longer is a different answer.
	ratio := float64(len(cited)) / float64(len(original))

	return ratio >= 0.6 && ratio <= 1.6
}
