// Package alertmanager models the webhook Prometheus Alertmanager sends.
//
// This is the entry point of the pipeline that has nothing to do with
// incident.io: Alertmanager fires, HolmesGPT investigates, and the conclusion
// is pushed to a phone. No incident is declared and nothing is written back.
package alertmanager

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status is the state of an alert or a whole group.
type Status string

const (
	StatusFiring   Status = "firing"
	StatusResolved Status = "resolved"
)

// Payload is the body Alertmanager POSTs to a webhook receiver (version 4).
type Payload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            Status            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

// Alert is one alert within a group.
type Alert struct {
	Status       Status            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ErrNoAlerts is returned for a payload carrying no alerts, which there is
// nothing to investigate.
var ErrNoAlerts = errors.New("alertmanager payload contains no alerts")

// Validate rejects a body that is syntactically JSON but not an Alertmanager
// notification — usually something else pointed at the wrong URL.
func (p *Payload) Validate() error {
	if len(p.Alerts) == 0 {
		return ErrNoAlerts
	}

	// Alertmanager has sent version 4 since 0.7. Anything else is either a
	// future format or not Alertmanager at all, and guessing is worse than
	// saying so.
	if p.Version != "" && p.Version != "4" {
		return fmt.Errorf("unsupported alertmanager webhook version %q (expected 4)", p.Version)
	}

	return nil
}

// Key identifies this alert group for deduplication.
//
// Alertmanager's groupKey is stable for a group across notifications, which is
// exactly the property needed to avoid investigating the same thing twice while
// it keeps firing.
func (p *Payload) Key() string {
	if p.GroupKey != "" {
		return p.GroupKey
	}

	// Older senders and hand-rolled callers may omit it; fall back to the
	// group labels, which are what groupKey is derived from.
	return "alertmanager/" + labelString(p.GroupLabels)
}

// Firing returns only the alerts still firing.
func (p *Payload) Firing() []Alert {
	out := make([]Alert, 0, len(p.Alerts))

	for _, a := range p.Alerts {
		if a.Status == StatusFiring {
			out = append(out, a)
		}
	}

	return out
}

// Title names the group the way a human would.
func (p *Payload) Title() string {
	if name := p.CommonLabels["alertname"]; name != "" {
		if scope := p.scope(); scope != "" {
			return name + " (" + scope + ")"
		}

		return name
	}

	if summary := p.CommonAnnotations["summary"]; summary != "" {
		return summary
	}

	return "Alertmanager group " + p.Key()
}

// scope is the most specific label that says where the alert applies.
func (p *Payload) scope() string {
	for _, key := range []string{"service", "job", "namespace", "instance", "pod"} {
		if v := p.CommonLabels[key]; v != "" {
			return v
		}
	}

	return ""
}

// Name returns the alertname, when there is one.
func (a Alert) Name() string { return a.Labels["alertname"] }

// Summary is the human description Prometheus rules conventionally attach.
func (a Alert) Summary() string {
	for _, key := range []string{"summary", "description", "message"} {
		if v := a.Annotations[key]; v != "" {
			return v
		}
	}

	return ""
}

// labelString renders labels deterministically, so a key derived from them is
// stable across notifications.
func labelString(labels map[string]string) string {
	if len(labels) == 0 {
		return "ungrouped"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}

	return strings.Join(parts, ",")
}

// SortedLabels renders a label set deterministically for display.
func SortedLabels(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+labels[k])
	}

	return out
}
