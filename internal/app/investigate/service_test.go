package investigate_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// fakeIncidentIO stands in for incident.io, recording what was written back.
type fakeIncidentIO struct {
	incident incidentio.IncidentV2
	alerts   []incidentio.IncidentAlertV2
	updates  []incidentio.IncidentUpdateV2

	getErr error

	mu            sync.Mutex
	postedUpdates []string
	postedKeys    []string
	timelineItems []string
}

func (f *fakeIncidentIO) Incident(context.Context, string) (incidentio.IncidentV2, error) {
	if f.getErr != nil {
		return incidentio.IncidentV2{}, f.getErr
	}

	return f.incident, nil
}

func (f *fakeIncidentIO) IncidentAlerts(context.Context, string) ([]incidentio.IncidentAlertV2, error) {
	return f.alerts, nil
}

func (f *fakeIncidentIO) IncidentUpdates(context.Context, string) ([]incidentio.IncidentUpdateV2, error) {
	return f.updates, nil
}

func (f *fakeIncidentIO) PostUpdate(
	_ context.Context, _, message, key string,
) (incidentio.IncidentUpdateV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.postedUpdates = append(f.postedUpdates, message)
	f.postedKeys = append(f.postedKeys, key)

	return incidentio.IncidentUpdateV2{}, nil
}

func (f *fakeIncidentIO) AddTimelineItem(
	_ context.Context, _, _, description, _ string, _ time.Time,
) (incidentio.IncidentTimelineItemV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.timelineItems = append(f.timelineItems, description)

	return incidentio.IncidentTimelineItemV2{}, nil
}

func (f *fakeIncidentIO) posted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, len(f.postedUpdates))
	copy(out, f.postedUpdates)

	return out
}

// fakeHolmes stands in for a HolmesGPT server.
type fakeHolmes struct {
	analysis string
	err      error
	failures int32
	calls    atomic.Int32
	lastAsk  atomic.Value
	delay    time.Duration
}

func (f *fakeHolmes) Ask(ctx context.Context, req holmes.ChatRequest) (*holmes.ChatResponse, error) {
	f.calls.Add(1)
	f.lastAsk.Store(req)

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Fail the first `failures` calls, so retry behaviour can be observed.
	if f.calls.Load() <= atomic.LoadInt32(&f.failures) {
		return nil, &holmes.Error{Operation: "POST /api/chat", StatusCode: 503, Body: "unavailable"}
	}

	if f.err != nil {
		return nil, f.err
	}

	return &holmes.ChatResponse{
		Analysis:  f.analysis,
		ToolCalls: []holmes.ToolCall{{ToolName: "kubectl_get"}, {ToolName: "prometheus_query"}},
	}, nil
}

func liveIncident() incidentio.IncidentV2 {
	summary := "Checkout is slow and erroring."

	return incidentio.IncidentV2{
		Id:        "01INCIDENT",
		Reference: "TEST-142",
		Name:      "Checkout latency spike",
		Summary:   &summary,
		Mode:      incidentio.IncidentV2ModeStandard,
		IncidentStatus: incidentio.IncidentStatusV2{
			Id: "01STATUS", Name: "Investigating",
			Category: incidentio.IncidentStatusV2CategoryLive,
		},
		Severity:  &incidentio.SeverityV2{Id: "01SEV1", Name: "Critical", Rank: 3},
		CreatedAt: time.Now().Add(-40 * time.Minute),
	}
}

func newServiceWith(
	t *testing.T, io investigate.IncidentIO, h investigate.Holmes, cfg investigate.Config,
) *investigate.Service {
	t.Helper()

	m, err := metrics.New()
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	return investigate.New(zaptest.NewLogger(t), io, h, m, cfg)
}

func newService(t *testing.T, io *fakeIncidentIO, h *fakeHolmes, cfg investigate.Config) *investigate.Service {
	t.Helper()

	m, err := metrics.New()
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	return investigate.New(zaptest.NewLogger(t), io, h, m, cfg)
}

func TestInvestigatePostsAnalysisAsAnUpdate(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "**Summary** The connection pool is exhausted."}

	result, err := newService(t, io, h, investigate.Config{}).
		Investigate(context.Background(), "01INCIDENT")
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if result.Skipped != "" {
		t.Fatalf("a live SEV1 should not be skipped, got %q", result.Skipped)
	}

	posted := io.posted()
	if len(posted) != 1 {
		t.Fatalf("want one posted update, got %d", len(posted))
	}

	if !strings.Contains(posted[0], "connection pool is exhausted") {
		t.Errorf("the analysis should reach incident.io, got:\n%s", posted[0])
	}

	// A responder must be able to tell this came from a machine.
	if !strings.Contains(posted[0], "HolmesGPT") || !strings.Contains(posted[0], "AI-generated") {
		t.Errorf("the update should be attributed and carry a caveat, got:\n%s", posted[0])
	}

	if result.ToolCalls != 2 {
		t.Errorf("tool calls: got %d, want 2", result.ToolCalls)
	}
}

func TestInvestigateWritesToTimeline(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}

	_, err := newService(t, io, h, investigate.Config{WriteBack: investigate.WriteBackTimeline}).
		Investigate(context.Background(), "01INCIDENT")
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if len(io.timelineItems) != 1 {
		t.Errorf("want one timeline item, got %d", len(io.timelineItems))
	}

	if len(io.posted()) != 0 {
		t.Errorf("timeline mode should not post an update")
	}
}

func TestInvestigateWriteBackNoneTouchesNothing(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}

	result, err := newService(t, io, h, investigate.Config{WriteBack: investigate.WriteBackNone}).
		Investigate(context.Background(), "01INCIDENT")
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if len(io.posted()) != 0 || len(io.timelineItems) != 0 {
		t.Error("write-back none must not modify the incident")
	}

	if result.Analysis == "" {
		t.Error("the analysis should still be returned to the caller")
	}
}

func TestInvestigateSkipsClosedAndTestIncidents(t *testing.T) {
	t.Parallel()

	closed := liveIncident()
	closed.IncidentStatus.Category = incidentio.IncidentStatusV2CategoryClosed

	declined := liveIncident()
	declined.IncidentStatus.Category = incidentio.IncidentStatusV2CategoryDeclined

	testMode := liveIncident()
	testMode.Mode = incidentio.IncidentV2ModeTest

	tutorial := liveIncident()
	tutorial.Mode = incidentio.IncidentV2ModeTutorial

	for name, incident := range map[string]incidentio.IncidentV2{
		"closed":   closed,
		"declined": declined,
		"test":     testMode,
		"tutorial": tutorial,
	} {
		io := &fakeIncidentIO{incident: incident}
		h := &fakeHolmes{analysis: "should never run"}

		result, err := newService(t, io, h, investigate.Config{}).
			Investigate(context.Background(), "01INCIDENT")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if result.Skipped == "" {
			t.Errorf("%s: should have been skipped", name)
		}

		if h.calls.Load() != 0 {
			t.Errorf("%s: HolmesGPT should not have been called", name)
		}

		if len(io.posted()) != 0 {
			t.Errorf("%s: nothing should have been written back", name)
		}
	}
}

func TestInvestigateRespectsMinimumSeverity(t *testing.T) {
	t.Parallel()

	minor := liveIncident()
	minor.Severity = &incidentio.SeverityV2{Id: "01SEV3", Name: "Minor", Rank: 1}

	io := &fakeIncidentIO{incident: minor}
	h := &fakeHolmes{analysis: "should never run"}

	result, err := newService(t, io, h, investigate.Config{MinSeverityRank: 3}).
		Investigate(context.Background(), "01INCIDENT")
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if result.Skipped == "" {
		t.Error("a Minor incident should be skipped when the minimum rank is 3")
	}

	// An incident with no severity at all must also be skipped, not treated as
	// infinitely severe.
	noSeverity := liveIncident()
	noSeverity.Severity = nil

	result, err = newService(t, &fakeIncidentIO{incident: noSeverity}, h,
		investigate.Config{MinSeverityRank: 3}).Investigate(context.Background(), "01INCIDENT")
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if result.Skipped == "" {
		t.Error("an incident with no severity should be skipped when a minimum is set")
	}
}

func TestInvestigateRetriesTransientHolmesFailures(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "recovered", failures: 1}

	_, err := newService(t, io, h, investigate.Config{Attempts: 3}).
		Investigate(context.Background(), "01INCIDENT")
	if err != nil {
		t.Fatalf("a retryable failure should not end the investigation: %v", err)
	}

	if h.calls.Load() != 2 {
		t.Errorf("want 2 calls (1 failure + 1 success), got %d", h.calls.Load())
	}

	if len(io.posted()) != 1 {
		t.Errorf("the recovered analysis should be posted")
	}
}

func TestInvestigateRejectsEmptyAnalysis(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "   "}

	_, err := newService(t, io, h, investigate.Config{}).
		Investigate(context.Background(), "01INCIDENT")
	if err == nil {
		t.Fatal("an empty analysis should fail rather than post an empty update")
	}

	if len(io.posted()) != 0 {
		t.Error("nothing should be posted when the analysis is empty")
	}
}

func TestInvestigateDeduplicatesConcurrentRuns(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis", delay: 200 * time.Millisecond}

	s := newService(t, io, h, investigate.Config{MaxConcurrent: 4})

	var (
		wg       sync.WaitGroup
		rejected atomic.Int32
	)

	// incident.io fires several events per incident; they must collapse into
	// one investigation rather than five.
	for range 5 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := s.Investigate(context.Background(), "01INCIDENT"); errors.Is(err, investigate.ErrAlreadyRunning) {
				rejected.Add(1)
			}
		}()
	}

	wg.Wait()

	if h.calls.Load() != 1 {
		t.Errorf("want exactly 1 HolmesGPT call for 5 concurrent triggers, got %d", h.calls.Load())
	}

	if rejected.Load() != 4 {
		t.Errorf("want 4 duplicate triggers rejected, got %d", rejected.Load())
	}
}

func TestInvestigateReleasesTheLockAfterCompleting(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}
	// Cooldown off: this covers the in-flight lock being released, not the
	// separate cooldown window.
	s := newService(t, io, h, investigate.Config{Cooldown: -1})

	for i := range 2 {
		if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
			t.Fatalf("sequential run %d should succeed: %v", i, err)
		}
	}

	if h.calls.Load() != 2 {
		t.Errorf("sequential investigations should both run, got %d calls", h.calls.Load())
	}
}

func TestInvestigateUsesAStableIdempotencyKey(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "the same conclusion"}
	// Cooldown off so the same analysis is produced twice and the keys can be
	// compared; the cooldown itself is covered separately.
	s := newService(t, io, h, investigate.Config{Cooldown: -1})

	for range 2 {
		if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
			t.Fatalf("Investigate: %v", err)
		}
	}

	// The same analysis for the same incident must reuse the key, so a
	// redelivered webhook cannot post the conclusion twice.
	if io.postedKeys[0] != io.postedKeys[1] {
		t.Errorf("identical analyses should share an idempotency key, got %q and %q",
			io.postedKeys[0], io.postedKeys[1])
	}
}

func TestInvestigateFailsWhenTheIncidentCannotBeLoaded(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{getErr: errors.New("boom")}
	h := &fakeHolmes{analysis: "never"}

	if _, err := newService(t, io, h, investigate.Config{}).
		Investigate(context.Background(), "01INCIDENT"); err == nil {
		t.Fatal("an unloadable incident should fail the investigation")
	}

	if h.calls.Load() != 0 {
		t.Error("HolmesGPT should not be called when the incident could not be loaded")
	}
}

func TestInvestigateCoolsDownAfterARun(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}
	s := newService(t, io, h, investigate.Config{Cooldown: time.Hour})

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Writing the analysis back is itself a change, and incident.io emits an
	// event for it. Without the cooldown that event starts another
	// investigation, which writes back again, and the bridge bills an LLM in a
	// loop. This is the guard that makes that impossible.
	_, err := s.Investigate(context.Background(), "01INCIDENT")
	if !errors.Is(err, investigate.ErrCoolingDown) {
		t.Fatalf("want ErrCoolingDown on the follow-up event, got %v", err)
	}

	if h.calls.Load() != 1 {
		t.Errorf("want exactly 1 HolmesGPT call, got %d", h.calls.Load())
	}

	if len(io.posted()) != 1 {
		t.Errorf("want exactly 1 posted update, got %d", len(io.posted()))
	}
}

func TestInvestigateRunsAgainOnceTheCooldownExpires(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}

	// A short window rather than a fake clock: the service's cooldown is wall
	// clock, and this keeps the test honest about that.
	s := newService(t, io, h, investigate.Config{Cooldown: 150 * time.Millisecond})

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); !errors.Is(err, investigate.ErrCoolingDown) {
		t.Fatalf("want ErrCoolingDown immediately after, got %v", err)
	}

	time.Sleep(250 * time.Millisecond)

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("a genuinely new development should investigate again: %v", err)
	}

	if h.calls.Load() != 2 {
		t.Errorf("want 2 calls once the window has passed, got %d", h.calls.Load())
	}
}

func TestInvestigateCooldownIsPerIncident(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}
	s := newService(t, io, h, investigate.Config{Cooldown: time.Hour})

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("first incident: %v", err)
	}

	// A cooldown on one incident must not silence a genuinely different one.
	other := liveIncident()
	other.Id = "01OTHER"
	io.incident = other

	if _, err := s.Investigate(context.Background(), "01OTHER"); err != nil {
		t.Fatalf("a different incident should not be cooling down: %v", err)
	}

	if h.calls.Load() != 2 {
		t.Errorf("want 2 calls across 2 incidents, got %d", h.calls.Load())
	}
}

func TestInvestigateCooldownCanBeDisabled(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}

	// A negative cooldown disables the window, which is what the one-shot CLI
	// command does: a human asking explicitly should never be told to wait.
	s := newService(t, io, h, investigate.Config{Cooldown: -1})

	for i := range 3 {
		if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	if h.calls.Load() != 3 {
		t.Errorf("want 3 calls with the cooldown disabled, got %d", h.calls.Load())
	}
}

func TestFailedInvestigationDoesNotArmTheCooldown(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	// Fail every attempt, so the first Investigate returns an error.
	h := &fakeHolmes{analysis: "recovered", failures: 99}
	s := newService(t, io, h, investigate.Config{Cooldown: time.Hour, Attempts: 1})

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); err == nil {
		t.Fatal("the first run should fail")
	}

	// A failure posted nothing, so there is no feedback loop to guard against.
	// Locking the incident out for the cooldown would mean one transient fault
	// stops the bridge investigating it for the next hour.
	atomic.StoreInt32(&h.failures, 0)

	result, err := s.Investigate(context.Background(), "01INCIDENT")
	if errors.Is(err, investigate.ErrCoolingDown) {
		t.Fatal("a failed investigation must not arm the cooldown")
	}

	if err != nil {
		t.Fatalf("the retry should succeed: %v", err)
	}

	if len(io.posted()) != 1 {
		t.Errorf("the retry should post its analysis, got %d updates", len(io.posted()))
	}

	if result.Analysis == "" {
		t.Error("the retry should return an analysis")
	}
}

func TestSuccessfulInvestigationStillArmsTheCooldown(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}
	s := newService(t, io, h, investigate.Config{Cooldown: time.Hour})

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if _, err := s.Investigate(context.Background(), "01INCIDENT"); !errors.Is(err, investigate.ErrCoolingDown) {
		t.Fatalf("a completed investigation must still arm the cooldown, got %v", err)
	}
}

// multiIncidentIO serves a distinct incident per ID, so a test can tell the
// investigations apart.
type multiIncidentIO struct {
	mu     sync.Mutex
	posted map[string][]string
}

func newMultiIncidentIO() *multiIncidentIO {
	return &multiIncidentIO{posted: map[string][]string{}}
}

func (m *multiIncidentIO) Incident(_ context.Context, id string) (incidentio.IncidentV2, error) {
	in := liveIncident()
	in.Id = id
	in.Reference = "TEST-" + id

	return in, nil
}

func (m *multiIncidentIO) IncidentAlerts(context.Context, string) ([]incidentio.IncidentAlertV2, error) {
	return nil, nil
}

func (m *multiIncidentIO) IncidentUpdates(context.Context, string) ([]incidentio.IncidentUpdateV2, error) {
	return nil, nil
}

func (m *multiIncidentIO) PostUpdate(
	_ context.Context, incidentID, message, _ string,
) (incidentio.IncidentUpdateV2, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.posted[incidentID] = append(m.posted[incidentID], message)

	return incidentio.IncidentUpdateV2{}, nil
}

func (m *multiIncidentIO) AddTimelineItem(
	context.Context, string, string, string, string, time.Time,
) (incidentio.IncidentTimelineItemV2, error) {
	return incidentio.IncidentTimelineItemV2{}, nil
}

func (m *multiIncidentIO) incidentsPosted() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.posted)
}

// concurrencyProbe records how many investigations overlap.
type concurrencyProbe struct {
	delay time.Duration

	mu      sync.Mutex
	current int
	peak    int
	seen    map[string]int
}

func (c *concurrencyProbe) Ask(ctx context.Context, req holmes.ChatRequest) (*holmes.ChatResponse, error) {
	c.mu.Lock()
	c.current++
	if c.current > c.peak {
		c.peak = c.current
	}

	if c.seen == nil {
		c.seen = map[string]int{}
	}

	c.seen[req.ConversationID]++
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.current--
		c.mu.Unlock()
	}()

	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &holmes.ChatResponse{Analysis: "analysis for " + req.ConversationID}, nil
}

func (c *concurrencyProbe) peakConcurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.peak
}

func (c *concurrencyProbe) distinctConversations() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.seen)
}

func TestInvestigateHandlesManyIncidentsConcurrently(t *testing.T) {
	t.Parallel()

	const (
		incidents     = 6
		maxConcurrent = 2
	)

	io := newMultiIncidentIO()
	probe := &concurrencyProbe{delay: 120 * time.Millisecond}
	svc := newServiceWith(t, io, probe, investigate.Config{
		MaxConcurrent: maxConcurrent,
		Cooldown:      time.Hour,
	})

	var wg sync.WaitGroup

	errs := make([]error, incidents)

	for i := range incidents {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, errs[i] = svc.Investigate(context.Background(), fmt.Sprintf("01INC%d", i))
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("incident %d failed: %v", i, err)
		}
	}

	// Every incident is its own piece of work: distinct claim, distinct
	// cooldown, distinct analysis posted back to it.
	if got := io.incidentsPosted(); got != incidents {
		t.Errorf("want an analysis on each of %d incidents, got %d", incidents, got)
	}

	if got := probe.distinctConversations(); got != incidents {
		t.Errorf("want %d distinct HolmesGPT conversations, got %d", incidents, got)
	}

	// ...but never more than MaxConcurrent at once. The rest queue.
	if peak := probe.peakConcurrency(); peak > maxConcurrent {
		t.Errorf("peak concurrency %d exceeded MaxConcurrent %d", peak, maxConcurrent)
	}
}

func TestOneIncidentDoesNotBlockAnother(t *testing.T) {
	t.Parallel()

	io := newMultiIncidentIO()
	probe := &concurrencyProbe{delay: 150 * time.Millisecond}
	svc := newServiceWith(t, io, probe, investigate.Config{MaxConcurrent: 4, Cooldown: time.Hour})

	// A second trigger for an incident already running is rejected, while a
	// different incident proceeds unaffected.
	var wg sync.WaitGroup

	wg.Add(1)

	go func() { defer wg.Done(); _, _ = svc.Investigate(context.Background(), "01SAME") }()

	time.Sleep(30 * time.Millisecond)

	_, dup := svc.Investigate(context.Background(), "01SAME")
	if !errors.Is(dup, investigate.ErrAlreadyRunning) {
		t.Errorf("a duplicate trigger should be rejected, got %v", dup)
	}

	if _, err := svc.Investigate(context.Background(), "01OTHER"); err != nil {
		t.Errorf("a different incident should proceed, got %v", err)
	}

	wg.Wait()
}

func TestQueuedInvestigationGivesUpRatherThanWaitingForever(t *testing.T) {
	t.Parallel()

	io := newMultiIncidentIO()
	probe := &concurrencyProbe{delay: 400 * time.Millisecond}
	svc := newServiceWith(t, io, probe, investigate.Config{
		MaxConcurrent: 1,
		Cooldown:      time.Hour,
		QueueTimeout:  80 * time.Millisecond,
	})

	go func() { _, _ = svc.Investigate(context.Background(), "01FIRST") }()
	time.Sleep(50 * time.Millisecond)

	// A webhook-driven investigation carries a context that is never cancelled,
	// so without a queue bound this would block until the first finished — and
	// during a storm, until dozens had.
	start := time.Now()

	_, err := svc.Investigate(context.WithoutCancel(context.Background()), "01QUEUED")
	if !errors.Is(err, investigate.ErrTooBusy) {
		t.Fatalf("want ErrTooBusy once the queue timeout passes, got %v", err)
	}

	if waited := time.Since(start); waited > 250*time.Millisecond {
		t.Errorf("gave up after %s, which is well past the 80ms bound", waited)
	}

	if n := probe.distinctConversations(); n != 1 {
		t.Errorf("the dropped investigation should never reach HolmesGPT, got %d conversations", n)
	}
}

func TestQueueTimeoutCanBeDisabled(t *testing.T) {
	t.Parallel()

	io := newMultiIncidentIO()
	probe := &concurrencyProbe{delay: 150 * time.Millisecond}
	svc := newServiceWith(t, io, probe, investigate.Config{
		MaxConcurrent: 1,
		Cooldown:      time.Hour,
		QueueTimeout:  -1, // wait as long as it takes
	})

	go func() { _, _ = svc.Investigate(context.Background(), "01FIRST") }()
	time.Sleep(30 * time.Millisecond)

	if _, err := svc.Investigate(context.Background(), "01QUEUED"); err != nil {
		t.Fatalf("with the bound disabled the queued investigation should still run: %v", err)
	}
}

// recordingNotifier captures what the service reported.
type recordingNotifier struct {
	mu     sync.Mutex
	events []investigate.Notification
}

func (r *recordingNotifier) Notify(_ context.Context, n investigate.Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, n)
}

func (r *recordingNotifier) all() []investigate.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]investigate.Notification, len(r.events))
	copy(out, r.events)

	return out
}

func newServiceNotifying(
	t *testing.T, io investigate.IncidentIO, h investigate.Holmes,
	cfg investigate.Config, n investigate.Notifier,
) *investigate.Service {
	t.Helper()

	m, err := metrics.New()
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	return investigate.New(zaptest.NewLogger(t), io, h, m, cfg, n)
}

func TestInvestigateNotifiesOnSuccess(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "**Summary**\nThe pool is exhausted.\n\n**Evidence**\n- x"}
	notifier := &recordingNotifier{}

	if _, err := newServiceNotifying(t, io, h, investigate.Config{}, notifier).
		Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	events := notifier.all()
	if len(events) != 1 {
		t.Fatalf("want 1 notification, got %d", len(events))
	}

	if events[0].Failed() {
		t.Error("a successful investigation should not be reported as failed")
	}

	if events[0].Headline != "The pool is exhausted." {
		t.Errorf("headline should be the summary, got %q", events[0].Headline)
	}

	if events[0].Reference != "INC-142" && events[0].Reference != "TEST-142" {
		t.Errorf("the notification should carry the reference, got %q", events[0].Reference)
	}
}

func TestInvestigateNotifiesOnFailure(t *testing.T) {
	t.Parallel()

	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "never", failures: 99}
	notifier := &recordingNotifier{}

	if _, err := newServiceNotifying(t, io, h, investigate.Config{Attempts: 1}, notifier).
		Investigate(context.Background(), "01INCIDENT"); err == nil {
		t.Fatal("the investigation should have failed")
	}

	events := notifier.all()
	if len(events) != 1 || !events[0].Failed() {
		t.Fatalf("a failure should be reported, got %+v", events)
	}
}

func TestInvestigateDoesNotNotifyOnSkip(t *testing.T) {
	t.Parallel()

	closed := liveIncident()
	closed.IncidentStatus.Category = incidentio.IncidentStatusV2CategoryClosed

	notifier := &recordingNotifier{}

	// A skip spends nothing and learns nothing; pushing it would be noise.
	if _, err := newServiceNotifying(t, &fakeIncidentIO{incident: closed},
		&fakeHolmes{analysis: "never"}, investigate.Config{}, notifier).
		Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if n := len(notifier.all()); n != 0 {
		t.Errorf("a skipped investigation should not notify, got %d", n)
	}
}

func TestInvestigateWorksWithoutANotifier(t *testing.T) {
	t.Parallel()

	// The notifier is optional; nothing should nil-panic without one.
	io := &fakeIncidentIO{incident: liveIncident()}
	h := &fakeHolmes{analysis: "an analysis"}

	if _, err := newService(t, io, h, investigate.Config{}).
		Investigate(context.Background(), "01INCIDENT"); err != nil {
		t.Fatalf("Investigate: %v", err)
	}
}
