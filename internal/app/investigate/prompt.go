// Package investigate turns an incident.io event into a HolmesGPT
// investigation, and the answer back into an incident.io record.
package investigate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
)

// systemPrompt shapes what HolmesGPT produces. It is deliberately prescriptive
// about length and structure: the answer is posted into a live incident channel
// where responders are busy, and an unbounded essay is worse than nothing.
const systemPrompt = `You are assisting an on-call engineer during a live incident.

Investigate using the tools available to you, then answer in GitHub-flavoured
Markdown with exactly these sections:

**Summary** — two sentences at most, stating what is broken and the blast radius.
**Evidence** — bullets, each citing a concrete observation from a tool call
(metric, log line, event, resource state). No speculation in this section.
**Most likely cause** — one paragraph. Say plainly if the evidence does not
support a conclusion; a well-supported "not yet determined" is more useful than
a confident guess.
**Suggested next steps** — a numbered list of specific, safe, reversible actions.

Rules:
- Never claim something you did not observe through a tool call.
- Link the evidence. When a tool returns a URL — a dashboard, a log query, a
  trace — cite it inline as a Markdown link beside the observation it supports,
  so a responder can click through instead of reconstructing the query.
- Never invent a URL, and never guess at one from a pattern. Cite only links a
  tool actually returned; a plausible-looking dead link costs more time during
  an incident than no link at all.
- Prefer naming the exact resource (pod, node, query, deploy) over describing it.
- Do not restate the incident description back to the reader; they wrote it.
- If a step would be destructive or irreversible, say so explicitly.`

// Prompt is the question the bridge poses to HolmesGPT, assembled from
// everything incident.io knows about the incident.
type Prompt struct {
	Ask    string
	System string
}

// BuildPrompt assembles the investigation question.
//
// HolmesGPT has no incident.io integration of its own, so everything it should
// know has to be written into the prompt: the incident, the alerts that opened
// it, and what responders have already found.
func BuildPrompt(
	incident incidentio.IncidentV2,
	alerts []incidentio.IncidentAlertV2,
	updates []incidentio.IncidentUpdateV2,
) Prompt {
	var b strings.Builder

	fmt.Fprintf(&b, "Investigate incident %s: %s\n\n", incident.Reference, incident.Name)

	b.WriteString("## Incident\n")
	fmt.Fprintf(&b, "- Status: %s\n", incident.IncidentStatus.Name)

	if incident.Severity != nil {
		fmt.Fprintf(&b, "- Severity: %s\n", incident.Severity.Name)
	}

	fmt.Fprintf(&b, "- Declared: %s (%s ago)\n",
		incident.CreatedAt.Format(time.RFC3339), since(incident.CreatedAt))

	if incident.Summary != nil && *incident.Summary != "" {
		fmt.Fprintf(&b, "- Summary: %s\n", collapse(*incident.Summary))
	}

	if len(incident.IncidentRoleAssignments) > 0 {
		b.WriteString("- Responders: ")
		b.WriteString(strings.Join(responders(incident.IncidentRoleAssignments), ", "))
		b.WriteString("\n")
	}

	if len(alerts) > 0 {
		b.WriteString("\n## Alerts that opened this incident\n")

		for _, a := range alerts {
			fmt.Fprintf(&b, "\n### %s\n", a.Alert.Title)
			fmt.Fprintf(&b, "- Status: %s\n", a.Alert.Status)
			fmt.Fprintf(&b, "- Fired: %s (%s ago)\n",
				a.Alert.CreatedAt.Format(time.RFC3339), since(a.Alert.CreatedAt))

			if a.Alert.Description != nil && *a.Alert.Description != "" {
				fmt.Fprintf(&b, "- Detail: %s\n", collapse(*a.Alert.Description))
			}

			if a.Alert.SourceUrl != nil && *a.Alert.SourceUrl != "" {
				fmt.Fprintf(&b, "- Source: %s\n", *a.Alert.SourceUrl)
			}
		}
	}

	// Oldest first, so the feed reads as a narrative.
	if len(updates) > 0 {
		sorted := make([]incidentio.IncidentUpdateV2, len(updates))
		copy(sorted, updates)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
		})

		b.WriteString("\n## What responders have established so far\n")

		for _, u := range sorted {
			if u.Message == nil || *u.Message == "" {
				continue
			}

			fmt.Fprintf(&b, "- [%s] %s\n",
				u.CreatedAt.Format("15:04"), collapse(*u.Message))
		}

		b.WriteString("\nDo not repeat what is already known above; build on it.\n")
	}

	return Prompt{Ask: b.String(), System: systemPrompt}
}

func responders(assignments []incidentio.IncidentRoleAssignmentV2) []string {
	out := make([]string, 0, len(assignments))

	for _, a := range assignments {
		if a.Assignee == nil {
			continue
		}

		out = append(out, fmt.Sprintf("%s (%s)", a.Assignee.Name, a.Role.Name))
	}

	return out
}

// since renders an age the way a responder would say it out loud.
func since(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// collapse flattens the multi-line text incident.io stores into something that
// survives being put on a single bullet.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
