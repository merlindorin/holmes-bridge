package investigate

import (
	"context"
	"strings"
)

// Notifier is told how each investigation ended.
//
// It is deliberately fire-and-forget: a notification is a side effect of an
// investigation, never a reason to fail one. Implementations must not block for
// long and must not return errors that would change the outcome.
type Notifier interface {
	Notify(ctx context.Context, n Notification)
}

// Notification describes a finished investigation, successful or not.
type Notification struct {
	IncidentID string
	Reference  string
	Name       string
	Permalink  string

	// Headline is the short form worth pushing to a phone: the analysis's
	// Summary section when there is one, or the failure otherwise.
	Headline  string
	Analysis  string
	ToolCalls int

	// Err is set when the investigation failed.
	Err error
}

// Failed reports whether this notification describes a failure.
func (n Notification) Failed() bool { return n.Err != nil }

// summaryHeading is the section the system prompt requires HolmesGPT to open
// with. Pushing the whole analysis to a phone is unreadable; this is the part
// worth waking someone for.
const summaryHeading = "**Summary**"

// headline extracts the Summary section from an analysis.
//
// It falls back to the leading prose when the model did not follow the required
// structure, because a truncated answer is still more useful than none.
func headline(analysis string) string {
	text := strings.TrimSpace(analysis)
	if text == "" {
		return ""
	}

	if idx := strings.Index(text, summaryHeading); idx >= 0 {
		text = text[idx+len(summaryHeading):]

		// Stop at the next section heading, so only the summary comes through.
		if end := strings.Index(text, "\n**"); end >= 0 {
			text = text[:end]
		}
	}

	return collapseBlankLines(strings.TrimSpace(text))
}

// collapseBlankLines squeezes the paragraph breaks a model leaves behind, which
// waste space in a notification that is a few lines tall.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}

		out = append(out, line)
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}

// notify tells the configured Notifier how an investigation ended. It is a
// no-op when none is configured.
func (s *Service) notify(ctx context.Context, n Notification) {
	if s.notifier == nil {
		return
	}

	s.notifier.Notify(ctx, n)
}
