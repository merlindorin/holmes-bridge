// Package webhooks delivers signed events to subscribers, the way incident.io
// delivers them to yours.
package webhooks

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
)

// Subscription is one endpoint the mock delivers to.
//
// Secret is deliberately untagged for JSON: the control plane serialises
// subscriptions for inspection, and a signing secret has no business appearing
// in a response body.
type Subscription struct {
	// Name identifies the subscription in logs and the control plane.
	Name string `json:"name"`
	// URL receives the POST.
	URL string `json:"url"`
	// Secret signs the delivery. When empty, the deliverer's default is used.
	Secret string `json:"-"`
	// Events filters what this endpoint receives. Empty means everything.
	Events []webhooks.EventType `json:"events,omitempty"`
}

// HasOwnSecret reports whether this subscription signs with its own secret
// rather than the deliverer's default, without disclosing the secret itself.
func (s Subscription) HasOwnSecret() bool { return s.Secret != "" }

// Wants reports whether this subscription should receive the given event.
func (s Subscription) Wants(e webhooks.EventType) bool {
	if len(s.Events) == 0 {
		return true
	}

	for _, want := range s.Events {
		if want == e {
			return true
		}
	}

	return false
}

// ParseSubscription reads the compact form used on the command line:
//
//	name=bridge,url=http://localhost:18081/webhooks/incidentio,
//	events=public_incident.incident_created_v2|public_alert.alert_created_v1
//
// (written on one line in real use)
//
// The events field is optional; omitting it subscribes to everything.
func ParseSubscription(spec string) (Subscription, error) {
	var sub Subscription

	for _, part := range strings.Split(spec, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return Subscription{}, fmt.Errorf("subscription field %q is not key=value", part)
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			sub.Name = value
		case "url":
			sub.URL = value
		case "secret":
			sub.Secret = value
		case "events":
			for _, e := range strings.Split(value, "|") {
				event := webhooks.EventType(strings.TrimSpace(e))
				if !event.Valid() {
					return Subscription{}, fmt.Errorf("unknown webhook event %q", e)
				}

				sub.Events = append(sub.Events, event)
			}
		default:
			return Subscription{}, fmt.Errorf("unknown subscription field %q", key)
		}
	}

	if sub.URL == "" {
		return Subscription{}, fmt.Errorf("subscription %q is missing a url", spec)
	}

	u, err := url.Parse(sub.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Subscription{}, fmt.Errorf("subscription url %q is not an absolute http(s) url", sub.URL)
	}

	if sub.Name == "" {
		sub.Name = u.Host
	}

	return sub, nil
}
