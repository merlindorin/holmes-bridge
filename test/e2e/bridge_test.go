// Package e2e drives the whole loop over real HTTP: the incident.io mock, the
// bridge, and a stand-in HolmesGPT.
//
// Everything below the transport is the production code path — the generated
// client, the real signer, the real handlers — so this is what catches wiring
// mistakes the unit tests cannot see.
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap/zaptest"

	bridgeV1 "github.com/merlindorin/holmes-bridge/api/bridge/v1"
	"github.com/merlindorin/holmes-bridge/api/incidentio"
	incidentioV1 "github.com/merlindorin/holmes-bridge/api/incidentio/v1"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	infraincidentio "github.com/merlindorin/holmes-bridge/internal/infra/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/infra/memory"
	infrawebhooks "github.com/merlindorin/holmes-bridge/internal/infra/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/middleware"
)

const webhookSecret = "whsec_ZTJlLXRlc3Qtc2lnbmluZy1zZWNyZXQtMQ=="

// harness is the three services wired together, as they run in production.
type harness struct {
	mockURL   string
	bridgeURL string
	store     *memory.Store
	deliverer *infrawebhooks.Deliverer
	holmes    *stubHolmes
}

// stubHolmes is a HolmesGPT server that returns a canned analysis.
type stubHolmes struct {
	mu       sync.Mutex
	analysis string
	asks     []string
	server   *httptest.Server
}

func newStubHolmes(t *testing.T, analysis string) *stubHolmes {
	t.Helper()

	s := &stubHolmes{analysis: analysis}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "test", "model": "stub"})
	})
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var req holmes.ChatRequest
		_ = json.Unmarshal(body, &req)

		s.mu.Lock()
		s.asks = append(s.asks, req.Ask)
		reply := s.analysis
		s.mu.Unlock()

		_ = json.NewEncoder(w).Encode(holmes.ChatResponse{
			Analysis:  reply,
			ToolCalls: []holmes.ToolCall{{ToolName: "kubectl_get"}},
		})
	})

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	return s
}

func (s *stubHolmes) lastAsk() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.asks) == 0 {
		return ""
	}

	return s.asks[len(s.asks)-1]
}

func (s *stubHolmes) askCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.asks)
}

// setup builds the mock, the bridge, and a stub HolmesGPT, wired to each other.
func setup(t *testing.T, triggers ...webhooks.EventType) *harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	logger := zaptest.NewLogger(t)

	m, err := metrics.New()
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	// --- the incident.io mock, loaded from the shipped fixtures ---
	scenarios, err := fixtures.LoadDir("../../fixtures/scenarios")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	expanded, err := scenarios["checkout-latency"].Expand(time.Now())
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	store := memory.NewStore()
	store.Seed(expanded)

	h := &harness{store: store, holmes: newStubHolmes(t, "**Summary** The pool is exhausted.")}

	// The bridge's URL is not known until its server starts, so the deliverer
	// is pointed at a placeholder and the subscription rewritten below.
	bridgeMux := gin.New()
	bridgeMux.Use(middleware.ErrorHandler(logger))
	bridgeServer := httptest.NewServer(bridgeMux)
	t.Cleanup(bridgeServer.Close)

	h.bridgeURL = bridgeServer.URL

	deliverer, err := infrawebhooks.NewDeliverer(logger, webhookSecret,
		[]infrawebhooks.Subscription{{Name: "bridge", URL: bridgeServer.URL + "/webhooks/incidentio"}})
	if err != nil {
		t.Fatalf("NewDeliverer: %v", err)
	}

	h.deliverer = deliverer

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { defer close(done); _ = deliverer.Start(ctx)() }()

	t.Cleanup(func() { cancel(); <-done })

	mockMux := gin.New()
	mockMux.Use(middleware.ErrorHandler(logger))
	mockMux.Use(middleware.Metrics(m))
	incidentio.RegisterHandlers(mockMux, incidentioV1.NewServer(
		logger, store,
		memory.NewIncidentRepository(store),
		memory.NewWorkRepository(store),
		memory.NewCatalogueRepository(store),
		memory.NewAlertRepository(store),
		memory.NewCatalogRepository(store),
		deliverer,
	))

	mockServer := httptest.NewServer(mockMux)
	t.Cleanup(mockServer.Close)

	h.mockURL = mockServer.URL

	// --- the bridge, pointed at both ---
	client, err := infraincidentio.New(mockServer.URL, "")
	if err != nil {
		t.Fatalf("incidentio.New: %v", err)
	}

	service := investigate.New(logger, client,
		holmes.New(h.holmes.server.URL), m, investigate.Config{MaxConcurrent: 4})

	signer, err := webhooks.NewSigner(webhookSecret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	if len(triggers) == 0 {
		triggers = []webhooks.EventType{
			webhooks.PublicIncidentIncidentCreatedV2,
			webhooks.PublicIncidentIncidentStatusUpdatedV2,
		}
	}

	bridgeV1.NewServer(logger, signer, service, m, triggers).Mount(bridgeMux)

	return h
}

// liveIncidentID finds TEST-142 in the seeded scenario.
func (h *harness) liveIncidentID(t *testing.T) string {
	t.Helper()

	items, _, _, err := memory.NewIncidentRepository(h.store).
		List(context.Background(), incidentFilter{}.f(), pageAll())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, in := range items {
		if in.Reference == "TEST-142" {
			return in.Id
		}
	}

	t.Fatal("TEST-142 not found in the seeded store")

	return ""
}

// eventually polls until cond holds or the deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

func TestWebhookDrivesAnInvestigationAndWritesItBack(t *testing.T) {
	h := setup(t)
	incidentID := h.liveIncidentID(t)

	before := h.updateCount(t, incidentID)

	// Fire the event the mock would emit when an incident is declared.
	incident, err := memory.NewIncidentRepository(h.store).Get(context.Background(), incidentID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, err = h.deliverer.Emit(webhooks.PublicIncidentIncidentCreatedV2, incident); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	eventually(t, "HolmesGPT to be asked", func() bool { return h.holmes.askCount() > 0 })

	// The prompt must carry the incident context, not just its id.
	ask := h.holmes.lastAsk()
	for _, want := range []string{"TEST-142", "Checkout latency spike", "checkout-api p99 latency"} {
		if !strings.Contains(ask, want) {
			t.Errorf("prompt is missing %q\n---\n%s", want, ask)
		}
	}

	eventually(t, "the analysis to be posted back", func() bool {
		return h.updateCount(t, incidentID) > before
	})

	// And it must be findable through the public API, the way a responder sees it.
	found := false

	for _, u := range h.updates(t, incidentID) {
		if u.Message != nil && strings.Contains(*u.Message, "The pool is exhausted") {
			found = true
		}
	}

	if !found {
		t.Error("the analysis should appear in the incident's update feed")
	}
}

func TestWebhookWithABadSignatureIsRejected(t *testing.T) {
	h := setup(t)

	body := []byte(`{"event_type":"public_incident.incident_created_v2",` +
		`"public_incident.incident_created_v2":{"id":"01INCIDENT"}}`)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.bridgeURL+"/webhooks/incidentio", strings.NewReader(string(body)))
	req.Header.Set(webhooks.HeaderID, "msg_forged")
	req.Header.Set(webhooks.HeaderTimestamp, unixNow())
	req.Header.Set(webhooks.HeaderSignature, "v1,Zm9yZ2VkLXNpZ25hdHVyZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a forged signature should be rejected with 401, got %d", resp.StatusCode)
	}

	time.Sleep(200 * time.Millisecond)

	if h.holmes.askCount() != 0 {
		t.Error("a forged webhook must not reach HolmesGPT")
	}
}

func TestNonTriggerEventsAreAcknowledgedButIgnored(t *testing.T) {
	h := setup(t, webhooks.PublicIncidentIncidentCreatedV2)

	// The bridge subscribes to created-only here, so a status update is noise.
	incident, _ := memory.NewIncidentRepository(h.store).Get(context.Background(), h.liveIncidentID(t))

	if _, err := h.deliverer.Emit(webhooks.PublicIncidentIncidentStatusUpdatedV2, incident); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	if h.holmes.askCount() != 0 {
		t.Errorf("a non-trigger event should not start an investigation, got %d asks", h.holmes.askCount())
	}
}

func TestManualTriggerReturnsTheAnalysis(t *testing.T) {
	h := setup(t)
	incidentID := h.liveIncidentID(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.bridgeURL+"/investigations/"+incidentID, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("manual trigger returned %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Reference string `json:"reference"`
		Analysis  string `json:"analysis"`
		WrittenTo string `json:"written_to"`
	}

	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.Reference != "TEST-142" {
		t.Errorf("reference: got %q, want TEST-142", out.Reference)
	}

	if !strings.Contains(out.Analysis, "pool is exhausted") {
		t.Errorf("the analysis should be returned synchronously, got %q", out.Analysis)
	}

	if out.WrittenTo != "incident update" {
		t.Errorf("written_to: got %q", out.WrittenTo)
	}
}

func TestClosedIncidentsAreSkippedEndToEnd(t *testing.T) {
	h := setup(t)

	// TEST-141 is closed in the fixture.
	items, _, _, _ := memory.NewIncidentRepository(h.store).
		List(context.Background(), incidentFilter{}.f(), pageAll())

	var closedID string

	for _, in := range items {
		if in.Reference == "TEST-141" {
			closedID = in.Id
		}
	}

	if closedID == "" {
		t.Fatal("TEST-141 not found")
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.bridgeURL+"/investigations/"+closedID, nil)
	resp, _ := http.DefaultClient.Do(req)

	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Skipped string `json:"skipped"`
	}

	_ = json.NewDecoder(resp.Body).Decode(&out)

	if out.Skipped == "" {
		t.Error("a closed incident should be skipped")
	}

	if h.holmes.askCount() != 0 {
		t.Error("a closed incident should not reach HolmesGPT")
	}
}

func TestWriteBackDoesNotRetriggerItself(t *testing.T) {
	h := setup(t)
	incidentID := h.liveIncidentID(t)

	incident, err := memory.NewIncidentRepository(h.store).Get(context.Background(), incidentID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// One real trigger. The investigation posts an update, the mock emits an
	// event for that update, and the bridge receives it — which is exactly the
	// shape of a runaway loop if nothing stops it.
	if _, err = h.deliverer.Emit(webhooks.PublicIncidentIncidentCreatedV2, incident); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	eventually(t, "the first investigation to finish", func() bool {
		return h.holmes.askCount() > 0
	})

	// Long enough for a second round trip to have happened if one were going to.
	time.Sleep(1500 * time.Millisecond)

	if got := h.holmes.askCount(); got != 1 {
		t.Errorf("one trigger should produce exactly one investigation, got %d — "+
			"the bridge is reacting to its own write-back", got)
	}

	posted := 0

	for _, u := range h.updates(t, incidentID) {
		if u.Message != nil && strings.Contains(*u.Message, "HolmesGPT") {
			posted++
		}
	}

	if posted != 1 {
		t.Errorf("want exactly 1 analysis posted, got %d", posted)
	}
}

func TestStatusUpdatedEventCarriesTheRealPayloadShape(t *testing.T) {
	h := setup(t, webhooks.PublicIncidentIncidentStatusUpdatedV2)
	incidentID := h.liveIncidentID(t)

	// Move the incident's status through the public API, which is what makes
	// the mock emit incident_status_updated_v2.
	statuses, err := memory.NewCatalogueRepository(h.store).Statuses(context.Background())
	if err != nil || len(statuses) == 0 {
		t.Fatalf("statuses: %v", err)
	}

	var target string

	for _, s := range statuses {
		if s.Name == "Monitoring" {
			target = s.Id
		}
	}

	if target == "" {
		t.Fatal("no Monitoring status in the fixture")
	}

	body, _ := json.Marshal(map[string]any{
		"incident":                map[string]any{"incident_status_id": target},
		"notify_incident_channel": false,
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.mockURL+"/v2/incidents/"+incidentID+"/actions/edit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}

	_ = resp.Body.Close()

	// The bridge only investigates if it could pull an incident ID out of the
	// payload. Real incident.io nests the incident under `incident` for this
	// event rather than spreading it at the top level, so this is what proves
	// the mock emits the shape a production receiver is written against.
	eventually(t, "the status change to drive an investigation", func() bool {
		return h.holmes.askCount() > 0
	})
}

func TestStatusUpdatedPayloadIsNestedNotFlat(t *testing.T) {
	t.Parallel()

	// Guard the wire shape directly: incident nested, plus the transition.
	encoded, err := json.Marshal(webhooks.Envelope{
		EventType: webhooks.PublicIncidentIncidentStatusUpdatedV2,
		Resource: webhooks.IncidentWithStatusChange{
			Incident:       incidentio.IncidentV2{Id: "01INC", Reference: "TEST-1"},
			PreviousStatus: incidentio.IncidentStatusV2{Id: "triage", Name: "Triage"},
			NewStatus:      incidentio.IncidentStatusV2{Id: "live", Name: "Investigating"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var body map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	type ref struct {
		ID string `json:"id"`
	}

	var payload struct {
		Incident       *ref `json:"incident"`
		PreviousStatus *ref `json:"previous_status"`
		NewStatus      *ref `json:"new_status"`
	}

	if err = json.Unmarshal(body[string(webhooks.PublicIncidentIncidentStatusUpdatedV2)], &payload); err != nil {
		t.Fatalf("unmarshal resource: %v", err)
	}

	if payload.Incident == nil || payload.Incident.ID != "01INC" {
		t.Error("the incident must be nested under `incident`, as incident.io sends it")
	}

	if payload.PreviousStatus == nil || payload.NewStatus == nil {
		t.Error("the payload must carry previous_status and new_status")
	}
}

func TestActionCreatedTriggersAnInvestigation(t *testing.T) {
	h := setup(t, webhooks.PublicIncidentActionCreatedV1)
	incidentID := h.liveIncidentID(t)

	// Create an action the way a responder would, through the public API. The
	// mock emits action_created_v1, whose payload references the incident by
	// incident_id rather than carrying it inline.
	body, _ := json.Marshal(map[string]any{
		"incident_id": incidentID,
		"description": "Roll back checkout-api to v2.30.x",
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.mockURL+"/v3/actions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create action: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("create action returned %d: %s", resp.StatusCode, payload)
	}

	eventually(t, "the new action to drive an investigation", func() bool {
		return h.holmes.askCount() > 0
	})
}

func TestActionCreatedIsIgnoredWhenNotATrigger(t *testing.T) {
	// The default trigger set does not include actions, so adding one must not
	// start an investigation unless it was asked for.
	h := setup(t) // defaults: incident created + status updated

	incidentID := h.liveIncidentID(t)

	body, _ := json.Marshal(map[string]any{
		"incident_id": incidentID,
		"description": "Add an alert on pool utilisation",
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.mockURL+"/v3/actions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create action: %v", err)
	}

	_ = resp.Body.Close()

	time.Sleep(700 * time.Millisecond)

	if n := h.holmes.askCount(); n != 0 {
		t.Errorf("an action should not investigate when it is not a trigger, got %d asks", n)
	}
}
