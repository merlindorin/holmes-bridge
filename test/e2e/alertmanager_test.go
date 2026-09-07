package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap/zaptest"

	alertsV1 "github.com/merlindorin/holmes-bridge/api/alerts/v1"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/middleware"
)

// pushed records the notifications the pipeline produced. In this pipeline the
// notification IS the output — nothing is written back anywhere.
type pushed struct {
	mu     sync.Mutex
	events []investigate.Notification
}

func (p *pushed) Notify(_ context.Context, n investigate.Notification) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, n)
}

func (p *pushed) all() []investigate.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]investigate.Notification, len(p.events))
	copy(out, p.events)

	return out
}

// alertPipeline is the Alertmanager → Holmes → notification path, with no
// incident.io client at all.
func alertPipeline(t *testing.T, tokens ...string) (string, *stubHolmes, *pushed) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	logger := zaptest.NewLogger(t)

	m, err := metrics.New()
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	stub := newStubHolmes(t, "**Summary**\nThe pod cannot reach its database.")
	notifier := &pushed{}

	// nil incident.io client: this pipeline never reads or writes one.
	service := investigate.New(logger, nil, holmes.New(stub.server.URL), m,
		investigate.Config{MaxConcurrent: 2, Cooldown: time.Hour}, notifier)

	router := gin.New()
	router.Use(middleware.ErrorHandler(logger))
	alertsV1.NewServer(logger, service, m, tokens...).Mount(router)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return srv.URL, stub, notifier
}

const firingGroup = `{
  "version": "4",
  "groupKey": "{}:{alertname=\"KubePodCrashLooping\"}",
  "status": "firing",
  "commonLabels": {"alertname": "KubePodCrashLooping", "service": "checkout-api"},
  "commonAnnotations": {"summary": "checkout-api is crash looping"},
  "externalURL": "http://alertmanager.example.com",
  "alerts": [{"status": "firing", "labels": {"alertname": "KubePodCrashLooping", "pod": "checkout-api-1"},
              "annotations": {"summary": "pod 1 is looping"}, "startsAt": "2026-09-07T00:20:00Z"}]
}`

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		url+"/webhooks/alertmanager", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}

	return resp
}

func TestAlertmanagerWebhookDrivesAnInvestigationAndNotifies(t *testing.T) {
	url, stub, notifier := alertPipeline(t)

	resp := post(t, url, "", firingGroup)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	eventually(t, "HolmesGPT to be asked", func() bool { return stub.askCount() > 0 })

	// The prompt is built from labels alone — no incident exists.
	ask := stub.lastAsk()
	for _, want := range []string{"KubePodCrashLooping", "checkout-api", "pod 1 is looping"} {
		if !strings.Contains(ask, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}

	eventually(t, "the notification to be pushed", func() bool { return len(notifier.all()) > 0 })

	event := notifier.all()[0]
	if event.Failed() {
		t.Errorf("should not be a failure: %v", event.Err)
	}

	if !strings.Contains(event.Headline, "cannot reach its database") {
		t.Errorf("the notification should carry the summary, got %q", event.Headline)
	}
}

func TestAlertmanagerWebhookRequiresItsToken(t *testing.T) {
	url, stub, _ := alertPipeline(t, "secret")

	// Alertmanager cannot sign its webhooks, so the token is all that stands
	// between a reachable endpoint and anyone able to spend the model budget.
	for name, token := range map[string]string{"none": "", "wrong": "nope"} {
		resp := post(t, url, token, firingGroup)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s token: want 401, got %d", name, resp.StatusCode)
		}
	}

	time.Sleep(300 * time.Millisecond)

	if stub.askCount() != 0 {
		t.Error("an unauthenticated webhook must not reach HolmesGPT")
	}

	resp := post(t, url, "secret", firingGroup)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("the right token should be accepted, got %d", resp.StatusCode)
	}
}

func TestAlertmanagerResolvedGroupIsSkipped(t *testing.T) {
	url, stub, notifier := alertPipeline(t)

	resolved := strings.Replace(firingGroup, `"status": "firing"`, `"status": "resolved"`, 1)

	resp := post(t, url, "", resolved)
	_ = resp.Body.Close()

	time.Sleep(700 * time.Millisecond)

	// Whatever it was, it is over. Spending on a model to say so is waste.
	if stub.askCount() != 0 {
		t.Errorf("a resolved group should not be investigated, got %d asks", stub.askCount())
	}

	if n := len(notifier.all()); n != 0 {
		t.Errorf("a skip should not notify, got %d", n)
	}
}

func TestAlertmanagerEmptyGroupIsAcknowledged(t *testing.T) {
	url, stub, _ := alertPipeline(t)

	// Not an error on Alertmanager's side; answering 4xx would make it retry
	// something that will never differ.
	resp := post(t, url, "", `{"version":"4","status":"firing","alerts":[]}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}

	time.Sleep(300 * time.Millisecond)

	if stub.askCount() != 0 {
		t.Error("an empty group should not be investigated")
	}
}

func TestAlertmanagerRepeatedNotificationsCollapse(t *testing.T) {
	url, stub, _ := alertPipeline(t)

	// Alertmanager re-notifies for a group that keeps firing. groupKey is
	// stable across those, so they must collapse into one investigation.
	for range 3 {
		resp := post(t, url, "", firingGroup)
		_ = resp.Body.Close()
	}

	eventually(t, "the first investigation", func() bool { return stub.askCount() > 0 })
	time.Sleep(800 * time.Millisecond)

	if got := stub.askCount(); got != 1 {
		t.Errorf("three notifications for one group should investigate once, got %d", got)
	}
}
