package webhooks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	domain "github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/webhooks"
)

const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

// received captures what a subscriber saw, so a test can assert on the wire
// form rather than on the deliverer's internals.
type received struct {
	body      []byte
	id        string
	timestamp string
	signature string
}

func subscriber(t *testing.T, status func() int) (*httptest.Server, func() []received) {
	t.Helper()

	var (
		mu  sync.Mutex
		got []received
		srv *httptest.Server
	)

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		got = append(got, received{
			body:      body,
			id:        r.Header.Get(domain.HeaderID),
			timestamp: r.Header.Get(domain.HeaderTimestamp),
			signature: r.Header.Get(domain.HeaderSignature),
		})
		mu.Unlock()

		w.WriteHeader(status())
	}))
	t.Cleanup(srv.Close)

	return srv, func() []received {
		mu.Lock()
		defer mu.Unlock()

		out := make([]received, len(got))
		copy(out, got)

		return out
	}
}

func run(t *testing.T, d *webhooks.Deliverer) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = d.Start(ctx)()
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func eventually(t *testing.T, want int, get func() []received) []received {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := get(); len(got) >= want {
			return got
		}

		time.Sleep(10 * time.Millisecond)
	}

	got := get()
	t.Fatalf("timed out waiting for %d deliveries, got %d", want, len(got))

	return got
}

func TestDelivererSignsWhatItSends(t *testing.T) {
	t.Parallel()

	srv, got := subscriber(t, func() int { return http.StatusOK })

	d, err := webhooks.NewDeliverer(zaptest.NewLogger(t), secret,
		[]webhooks.Subscription{{Name: "bridge", URL: srv.URL}})
	if err != nil {
		t.Fatalf("NewDeliverer: %v", err)
	}

	run(t, d)

	if _, err = d.Emit(domain.PublicIncidentIncidentCreatedV2, map[string]string{"id": "INC1"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	delivery := eventually(t, 1, got)[0]

	// The receiver must be able to verify using only the headers and body,
	// which is the whole contract with a real subscriber.
	signer, err := domain.NewSigner(secret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	if err = signer.Verify(delivery.id, delivery.timestamp, delivery.signature, delivery.body); err != nil {
		t.Fatalf("a subscriber could not verify the delivery: %v", err)
	}
}

func TestDelivererSendsTheEnvelopeShape(t *testing.T) {
	t.Parallel()

	srv, got := subscriber(t, func() int { return http.StatusOK })

	d, _ := webhooks.NewDeliverer(zaptest.NewLogger(t), secret,
		[]webhooks.Subscription{{Name: "bridge", URL: srv.URL}})
	run(t, d)

	_, _ = d.Emit(domain.PublicAlertAlertCreatedV1, map[string]string{"id": "ALT1"})

	var env domain.Envelope
	if err := jsonUnmarshal(eventually(t, 1, got)[0].body, &env); err != nil {
		t.Fatalf("delivered body is not a webhook envelope: %v", err)
	}

	if env.EventType != domain.PublicAlertAlertCreatedV1 {
		t.Errorf("event_type: got %q, want %q", env.EventType, domain.PublicAlertAlertCreatedV1)
	}

	if env.ResourceJSON() == nil {
		t.Error("the resource should be keyed under the event type")
	}
}

func TestDelivererRespectsEventFilter(t *testing.T) {
	t.Parallel()

	wanted, wantedGot := subscriber(t, func() int { return http.StatusOK })
	other, otherGot := subscriber(t, func() int { return http.StatusOK })

	d, _ := webhooks.NewDeliverer(zaptest.NewLogger(t), secret, []webhooks.Subscription{
		{Name: "alerts-only", URL: wanted.URL, Events: []domain.EventType{domain.PublicAlertAlertCreatedV1}},
		{Name: "incidents-only", URL: other.URL, Events: []domain.EventType{domain.PublicIncidentIncidentCreatedV2}},
	})
	run(t, d)

	_, _ = d.Emit(domain.PublicAlertAlertCreatedV1, map[string]string{"id": "ALT1"})

	eventually(t, 1, wantedGot)

	// Give the wrong subscriber a chance to be wrongly called.
	time.Sleep(200 * time.Millisecond)

	if n := len(otherGot()); n != 0 {
		t.Errorf("a subscription filtered to other events received %d deliveries", n)
	}
}

func TestDelivererRetriesServerErrors(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	srv, got := subscriber(t, func() int {
		// Fail twice, then accept: the deliverer should keep trying.
		if calls.Add(1) <= 2 {
			return http.StatusInternalServerError
		}

		return http.StatusOK
	})

	d, _ := webhooks.NewDeliverer(zaptest.NewLogger(t), secret,
		[]webhooks.Subscription{{Name: "flaky", URL: srv.URL}})
	run(t, d)

	_, _ = d.Emit(domain.PublicIncidentIncidentCreatedV2, map[string]string{"id": "INC1"})

	eventually(t, 3, got)
}

func TestDelivererDoesNotRetryClientErrors(t *testing.T) {
	t.Parallel()

	srv, got := subscriber(t, func() int { return http.StatusBadRequest })

	d, _ := webhooks.NewDeliverer(zaptest.NewLogger(t), secret,
		[]webhooks.Subscription{{Name: "rejecting", URL: srv.URL}})
	run(t, d)

	_, _ = d.Emit(domain.PublicIncidentIncidentCreatedV2, map[string]string{"id": "INC1"})

	eventually(t, 1, got)
	time.Sleep(500 * time.Millisecond)

	// A 4xx means the subscriber will keep rejecting it; retrying is just noise.
	if n := len(got()); n != 1 {
		t.Errorf("a 4xx should not be retried, got %d attempts", n)
	}
}

func TestDelivererRecordsAttempts(t *testing.T) {
	t.Parallel()

	srv, got := subscriber(t, func() int { return http.StatusOK })

	d, _ := webhooks.NewDeliverer(zaptest.NewLogger(t), secret,
		[]webhooks.Subscription{{Name: "bridge", URL: srv.URL}})
	run(t, d)

	id, _ := d.Emit(domain.PublicIncidentIncidentCreatedV2, map[string]string{"id": "INC1"})

	// Wait on the log, not on the subscriber. The attempt is recorded after the
	// POST returns, so a handler that has run does not yet imply an entry —
	// waiting on the wrong signal makes this pass locally and fail on a slower
	// machine.
	_ = got

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, a := range d.Log() {
			if a.WebhookID == id && a.StatusCode == http.StatusOK {
				return
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Errorf("delivery %s never reached the log: %+v", id, d.Log())
}

func TestDelivererPerSubscriptionSecret(t *testing.T) {
	t.Parallel()

	const otherSecret = "whsec_YW5vdGhlci1zZWNyZXQtZm9yLXRlc3Rz"

	srv, got := subscriber(t, func() int { return http.StatusOK })

	d, err := webhooks.NewDeliverer(zaptest.NewLogger(t), secret,
		[]webhooks.Subscription{{Name: "bridge", URL: srv.URL, Secret: otherSecret}})
	if err != nil {
		t.Fatalf("NewDeliverer: %v", err)
	}

	run(t, d)

	_, _ = d.Emit(domain.PublicIncidentIncidentCreatedV2, map[string]string{"id": "INC1"})

	delivery := eventually(t, 1, got)[0]

	own, _ := domain.NewSigner(otherSecret)
	if err = own.Verify(delivery.id, delivery.timestamp, delivery.signature, delivery.body); err != nil {
		t.Fatalf("the subscription's own secret should sign it: %v", err)
	}

	def, _ := domain.NewSigner(secret)
	if err = def.Verify(delivery.id, delivery.timestamp, delivery.signature, delivery.body); err == nil {
		t.Error("the default secret should not verify a per-subscription signature")
	}
}

func TestParseSubscription(t *testing.T) {
	t.Parallel()

	const spec = "name=bridge,url=http://localhost:18081/hook," +
		"events=public_incident.incident_created_v2|public_alert.alert_created_v1"

	sub, err := webhooks.ParseSubscription(spec)
	if err != nil {
		t.Fatalf("ParseSubscription: %v", err)
	}

	if sub.Name != "bridge" || sub.URL != "http://localhost:18081/hook" || len(sub.Events) != 2 {
		t.Errorf("parsed to %+v", sub)
	}

	if !sub.Wants(domain.PublicAlertAlertCreatedV1) {
		t.Error("should want a listed event")
	}

	if sub.Wants(domain.ScheduleCreatedV1) {
		t.Error("should not want an unlisted event")
	}
}

func TestParseSubscriptionDefaultsAndErrors(t *testing.T) {
	t.Parallel()

	sub, err := webhooks.ParseSubscription("url=http://localhost:19999/hook")
	if err != nil {
		t.Fatalf("a bare url should parse: %v", err)
	}

	if sub.Name != "localhost:19999" {
		t.Errorf("name should default to the host, got %q", sub.Name)
	}

	if !sub.Wants(domain.ScheduleCreatedV1) {
		t.Error("no event filter should mean everything")
	}

	for name, spec := range map[string]string{
		"no url":        "name=bridge",
		"relative url":  "url=/hook",
		"unknown field": "url=http://x/y,colour=blue",
		"unknown event": "url=http://x/y,events=incident.exploded_v9",
		"not key=value": "url=http://x/y,bare",
	} {
		if _, parseErr := webhooks.ParseSubscription(spec); parseErr == nil {
			t.Errorf("%s: want an error for %q", name, spec)
		}
	}
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func TestSubscriptionNeverSerialisesItsSecret(t *testing.T) {
	t.Parallel()

	// The mock's control plane serialises subscriptions for inspection, so a
	// secret reaching the JSON would be handed to anyone who can call it.
	encoded, err := json.Marshal(webhooks.Subscription{
		Name: "bridge", URL: "http://localhost/hook", Secret: "whsec_c3VwZXItc2VjcmV0LXZhbHVl",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if strings.Contains(string(encoded), "c3VwZXItc2VjcmV0") || strings.Contains(string(encoded), "secret") {
		t.Errorf("a subscription must not serialise its signing secret, got %s", encoded)
	}
}
