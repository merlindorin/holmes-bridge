package alertmanager_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/merlindorin/holmes-bridge/internal/domain/alertmanager"
)

// A trimmed but faithful Alertmanager v4 notification.
const sample = `{
  "version": "4",
  "groupKey": "{}:{alertname=\"KubePodCrashLooping\", namespace=\"checkout-demo\"}",
  "status": "firing",
  "receiver": "holmes",
  "groupLabels": {"alertname": "KubePodCrashLooping"},
  "commonLabels": {"alertname": "KubePodCrashLooping", "service": "checkout-api", "severity": "critical"},
  "commonAnnotations": {"summary": "checkout-api is crash looping"},
  "externalURL": "http://alertmanager.example.com",
  "alerts": [
    {"status": "firing", "labels": {"alertname": "KubePodCrashLooping", "pod": "checkout-api-1"},
     "annotations": {"summary": "pod 1 is looping"}, "startsAt": "2026-09-07T00:20:00Z",
     "generatorURL": "http://prometheus/graph", "fingerprint": "abc"},
    {"status": "resolved", "labels": {"alertname": "KubePodCrashLooping", "pod": "checkout-api-2"},
     "annotations": {}, "startsAt": "2026-09-07T00:21:00Z", "fingerprint": "def"}
  ]
}`

func decode(t *testing.T) *alertmanager.Payload {
	t.Helper()

	var p alertmanager.Payload
	if err := json.Unmarshal([]byte(sample), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	return &p
}

func TestPayloadDecodesTheRealShape(t *testing.T) {
	t.Parallel()

	p := decode(t)

	if p.Version != "4" || p.Status != alertmanager.StatusFiring {
		t.Errorf("version/status: %q %q", p.Version, p.Status)
	}

	if len(p.Alerts) != 2 {
		t.Fatalf("want 2 alerts, got %d", len(p.Alerts))
	}

	if p.Alerts[0].StartsAt.IsZero() {
		t.Error("startsAt should parse as a timestamp")
	}

	if p.CommonLabels["service"] != "checkout-api" {
		t.Error("common labels should decode")
	}
}

func TestPayloadKeyIsAlertmanagersGroupKey(t *testing.T) {
	t.Parallel()

	// groupKey is stable for a group across notifications, which is exactly the
	// property the cooldown needs to avoid re-investigating a group that keeps
	// firing.
	p := decode(t)
	if !strings.Contains(p.Key(), "KubePodCrashLooping") {
		t.Errorf("key should be the group key, got %q", p.Key())
	}

	// Without one, the key is derived from the group labels — and must not
	// depend on Go's map ordering, or a group would be investigated afresh on
	// every notification.
	one := (&alertmanager.Payload{GroupLabels: map[string]string{"b": "2", "a": "1"}}).Key()
	two := (&alertmanager.Payload{GroupLabels: map[string]string{"a": "1", "b": "2"}}).Key()

	if one != two {
		t.Errorf("the fallback key must not depend on map ordering: %q vs %q", one, two)
	}

	if !strings.Contains(one, "a=1,b=2") {
		t.Errorf("fallback key should be sorted, got %q", one)
	}
}

func TestPayloadFiringExcludesResolved(t *testing.T) {
	t.Parallel()

	firing := decode(t).Firing()
	if len(firing) != 1 || firing[0].Labels["pod"] != "checkout-api-1" {
		t.Errorf("want only the firing alert, got %+v", firing)
	}
}

func TestPayloadTitle(t *testing.T) {
	t.Parallel()

	// alertname plus the most specific scope label reads like something a
	// person would say.
	if got := decode(t).Title(); got != "KubePodCrashLooping (checkout-api)" {
		t.Errorf("got %q", got)
	}

	// Falling back through annotations, then the key.
	only := &alertmanager.Payload{CommonAnnotations: map[string]string{"summary": "everything is on fire"}}
	if got := only.Title(); got != "everything is on fire" {
		t.Errorf("annotation fallback: got %q", got)
	}
}

func TestValidateRejectsWhatIsNotAlertmanager(t *testing.T) {
	t.Parallel()

	// An empty group is not an error on Alertmanager's side, but there is
	// nothing to investigate.
	empty := &alertmanager.Payload{Version: "4"}
	if err := empty.Validate(); !errors.Is(err, alertmanager.ErrNoAlerts) {
		t.Errorf("want ErrNoAlerts, got %v", err)
	}

	// A future or foreign format should say so rather than be guessed at.
	future := decode(t)
	future.Version = "5"

	if err := future.Validate(); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("want a version error, got %v", err)
	}

	// A payload with no version but real alerts is accepted: hand-rolled
	// senders omit it, and the alerts are what matter.
	noVersion := decode(t)
	noVersion.Version = ""

	if err := noVersion.Validate(); err != nil {
		t.Errorf("a missing version should be tolerated, got %v", err)
	}
}

func TestAlertSummaryFallsThroughAnnotations(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"summary", "description", "message"} {
		a := alertmanager.Alert{Annotations: map[string]string{key: "the text"}}
		if a.Summary() != "the text" {
			t.Errorf("%s: got %q", key, a.Summary())
		}
	}

	if (alertmanager.Alert{}).Summary() != "" {
		t.Error("no annotations should give no summary")
	}
}
