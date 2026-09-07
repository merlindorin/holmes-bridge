package investigate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/domain/alertmanager"
)

// BuildAlertPrompt assembles the question for an Alertmanager group.
//
// There is no incident here — no summary somebody wrote, no update feed, no
// responders. All the context there is comes from the labels and annotations
// the alerting rules attach, so the prompt leans on those and says plainly that
// nothing else is known.
func BuildAlertPrompt(p *alertmanager.Payload) Prompt {
	var b strings.Builder

	firing := p.Firing()

	fmt.Fprintf(&b, "Investigate this Prometheus alert: %s\n\n", p.Title())
	fmt.Fprintf(&b, "## Alert group\n")
	fmt.Fprintf(&b, "- Status: %s\n", p.Status)
	fmt.Fprintf(&b, "- Alerts: %d firing of %d in the group\n", len(firing), len(p.Alerts))

	if p.TruncatedAlerts > 0 {
		fmt.Fprintf(&b, "- %d further alerts were truncated by Alertmanager\n", p.TruncatedAlerts)
	}

	if labels := alertmanager.SortedLabels(p.CommonLabels); len(labels) > 0 {
		fmt.Fprintf(&b, "- Common labels: %s\n", strings.Join(labels, ", "))
	}

	for _, key := range []string{"summary", "description", "runbook_url"} {
		if v := p.CommonAnnotations[key]; v != "" {
			fmt.Fprintf(&b, "- %s: %s\n", key, collapse(v))
		}
	}

	// Individual alerts, bounded: a group can carry hundreds, and a prompt made
	// mostly of near-identical label sets crowds out the reasoning.
	shown := firing
	if len(shown) == 0 {
		shown = p.Alerts
	}

	truncated := 0
	if len(shown) > maxAlertsInPrompt {
		truncated = len(shown) - maxAlertsInPrompt
		shown = shown[:maxAlertsInPrompt]
	}

	b.WriteString("\n## Alerts\n")

	for _, a := range shown {
		fmt.Fprintf(&b, "\n### %s\n", orFallback(a.Name(), "unnamed alert"))
		fmt.Fprintf(&b, "- Status: %s\n", a.Status)

		if !a.StartsAt.IsZero() {
			fmt.Fprintf(&b, "- Firing since: %s (%s)\n",
				a.StartsAt.Format(time.RFC3339), since(a.StartsAt))
		}

		if summary := a.Summary(); summary != "" {
			fmt.Fprintf(&b, "- Detail: %s\n", collapse(summary))
		}

		if labels := alertmanager.SortedLabels(a.Labels); len(labels) > 0 {
			fmt.Fprintf(&b, "- Labels: %s\n", strings.Join(labels, ", "))
		}

		if a.GeneratorURL != "" {
			fmt.Fprintf(&b, "- Query: %s\n", a.GeneratorURL)
		}
	}

	if truncated > 0 {
		fmt.Fprintf(&b, "\n(%d further alerts in this group are not listed.)\n", truncated)
	}

	b.WriteString("\nNobody has triaged this yet — there is no incident, no summary and no " +
		"prior investigation. Work from the labels above and what you can inspect.\n")

	return Prompt{Ask: b.String(), System: systemPrompt}
}

// maxAlertsInPrompt bounds how many alerts of a group are spelled out. A large
// group is usually the same failure repeated, and the labels of the first few
// carry the same information as all of them.
const maxAlertsInPrompt = 10

// AlertSubject is an Alertmanager group presented as something to investigate.
type AlertSubject struct {
	Payload *alertmanager.Payload
}

// Key deduplicates on Alertmanager's group key, so a group that keeps firing is
// investigated once rather than on every notification.
func (s AlertSubject) Key() string { return s.Payload.Key() }

// Investigate runs an investigation for an Alertmanager group and pushes the
// result to the configured notifier.
//
// Nothing is written back: this pipeline has no incident.io in it, so the
// notification is the whole output.
func (s *Service) InvestigateAlerts(ctx context.Context, payload *alertmanager.Payload) (*Result, error) {
	if err := payload.Validate(); err != nil {
		return nil, err
	}

	subject := AlertSubject{Payload: payload}

	return s.guarded(ctx, subject.Key(), func(ctx context.Context, started time.Time) (*Result, error) {
		// A group that has fully resolved needs no root cause; whatever it was,
		// it is over, and spending on a model to say so is waste.
		if payload.Status == alertmanager.StatusResolved {
			return &Result{
				Reference: payload.Key(),
				Name:      payload.Title(),
				Skipped:   "the alert group has resolved",
				Duration:  time.Since(started),
			}, nil
		}

		log := s.logger.With(
			zap.String("group_key", payload.Key()),
			zap.String("alert", payload.Title()))

		prompt := BuildAlertPrompt(payload)

		log.Info("starting investigation",
			zap.Int("alerts", len(payload.Alerts)),
			zap.Int("prompt_bytes", len(prompt.Ask)))

		s.metrics.InvestigationsStarted.Add(ctx, 1)

		answer, err := s.askWith(ctx, log, "alertmanager-"+payload.Key(), prompt)
		if err != nil {
			return nil, err
		}

		result := &Result{
			IncidentID: payload.Key(),
			Reference:  payload.Key(),
			Name:       payload.Title(),
			Permalink:  payload.ExternalURL,
			Analysis:   answer.Analysis,
			ToolCalls:  len(answer.ToolCalls),
			Duration:   time.Since(started),
			WrittenTo:  "notification only",
		}

		log.Info("investigation complete",
			zap.Int("tool_calls", result.ToolCalls),
			zap.Duration("took", result.Duration))

		return result, nil
	})
}

func orFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
