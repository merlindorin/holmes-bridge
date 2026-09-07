package ntfy

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
)

// Notification length caps. A push notification is read on a lock screen, so
// the useful part is the first few lines; the full analysis is on the incident.
const (
	maxTitle   = 120
	maxMessage = 900
)

// Notifier pushes each finished investigation to an ntfy topic.
type Notifier struct {
	client *Client
	logger *zap.Logger

	// notifyOnFailure controls whether a failed investigation is pushed too.
	// On by default: a bridge that quietly stops working is worse than a noisy
	// one, and a failure is exactly what you want to know about.
	notifyOnFailure bool
}

// NotifierOption configures a Notifier.
type NotifierOption func(*Notifier)

// WithFailureNotifications turns pushes for failed investigations on or off.
func WithFailureNotifications(on bool) NotifierOption {
	return func(n *Notifier) { n.notifyOnFailure = on }
}

// NewNotifier adapts a Client to the investigate.Notifier port.
func NewNotifier(client *Client, logger *zap.Logger, opts ...NotifierOption) *Notifier {
	n := &Notifier{client: client, logger: logger.Named("ntfy"), notifyOnFailure: true}

	for _, opt := range opts {
		opt(n)
	}

	return n
}

var _ investigate.Notifier = (*Notifier)(nil)

// Notify publishes one investigation outcome.
//
// Failures to publish are logged and dropped. A notification is a side effect
// of an investigation, and the analysis is already on the incident either way —
// failing the investigation because a push did not go out would be worse than
// the missing push.
func (n *Notifier) Notify(ctx context.Context, event investigate.Notification) {
	if event.Failed() && !n.notifyOnFailure {
		return
	}

	if err := n.client.Publish(ctx, n.compose(event)); err != nil {
		n.logger.Warn("could not publish the notification",
			zap.String("incident_id", event.IncidentID), zap.Error(err))
	}
}

func (n *Notifier) compose(event investigate.Notification) Notification {
	if event.Failed() {
		return Notification{
			Title:    truncate(strings.TrimSpace(label(event)+" investigation failed"), maxTitle),
			Message:  truncate(event.Headline, maxMessage),
			Priority: PriorityHigh,
			// "rotating_light" renders as 🚨 in the ntfy apps.
			Tags:  []string{"rotating_light"},
			Click: event.Permalink,
		}
	}

	message := event.Headline
	if message == "" {
		message = "HolmesGPT returned no summary. The full analysis is on the incident."
	}

	if event.ToolCalls > 0 {
		message = fmt.Sprintf("%s\n\n_%d tool calls. AI-generated: verify before acting._",
			message, event.ToolCalls)
	}

	return Notification{
		Title:    truncate(label(event), maxTitle),
		Message:  truncate(message, maxMessage),
		Priority: PriorityDefault,
		Tags:     []string{"mag"},
		Click:    event.Permalink,
		Markdown: true,
	}
}

// label names the incident as a responder would recognise it.
func label(event investigate.Notification) string {
	switch {
	case event.Reference != "" && event.Name != "":
		return event.Reference + " · " + event.Name
	case event.Reference != "":
		return event.Reference
	case event.Name != "":
		return event.Name
	default:
		return "Incident " + event.IncidentID
	}
}

// truncate shortens text for a notification, counting runes rather than bytes.
//
// Byte slicing would split a multi-byte character — an accented word or an
// emoji in an analysis — and emit invalid UTF-8 that renders as a replacement
// character on the phone.
func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}

	cut := string(runes[:maxRunes])

	// Prefer a word boundary, when one is close enough that cutting there does
	// not lose most of the text.
	if idx := strings.LastIndexAny(cut, " \n"); idx > len(cut)*3/4 {
		cut = cut[:idx]
	}

	return strings.TrimSpace(cut) + "…"
}
