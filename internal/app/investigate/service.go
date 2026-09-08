package investigate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	infraincidentio "github.com/merlindorin/holmes-bridge/internal/infra/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// IncidentIO is the slice of the incident.io API the investigation needs.
// Narrowing it here keeps the service testable without an HTTP server.
type IncidentIO interface {
	Incident(ctx context.Context, id string) (incidentio.IncidentV2, error)
	IncidentAlerts(ctx context.Context, incidentID string) ([]incidentio.IncidentAlertV2, error)
	IncidentUpdates(ctx context.Context, incidentID string) ([]incidentio.IncidentUpdateV2, error)
	PostUpdate(ctx context.Context, incidentID, message, idempotencyKey string) (incidentio.IncidentUpdateV2, error)
	AddTimelineItem(ctx context.Context, incidentID, title, description, idempotencyKey string,
		at time.Time) (incidentio.IncidentTimelineItemV2, error)
}

// Holmes is the slice of the HolmesGPT API the investigation needs.
type Holmes interface {
	Ask(ctx context.Context, req holmes.ChatRequest) (*holmes.ChatResponse, error)
}

// WriteBack selects where an analysis is recorded.
type WriteBack string

const (
	// WriteBackUpdate posts to the incident's update feed, which mirrors into
	// the Slack channel. This is the useful default during a live incident.
	WriteBackUpdate WriteBack = "update"
	// WriteBackTimeline pins the analysis to the incident timeline instead,
	// which is quieter and better suited to retrospective work.
	WriteBackTimeline WriteBack = "timeline"
	// WriteBackNone runs the investigation and logs it without writing back,
	// which is how you evaluate the bridge against a real org safely.
	WriteBackNone WriteBack = "none"
)

// Valid reports whether w is a write-back mode the service implements.
func (w WriteBack) Valid() bool {
	switch w {
	case WriteBackUpdate, WriteBackTimeline, WriteBackNone:
		return true
	default:
		return false
	}
}

// Config tunes the service.
type Config struct {
	// SystemPrompt is a Go text/template for the system prompt. Empty uses the
	// built-in default, which is what almost every deployment should do.
	SystemPrompt string

	// WriteBack selects where the analysis is recorded.
	WriteBack WriteBack
	// MaxConcurrent bounds simultaneous investigations. An investigation is
	// expensive in both LLM spend and cluster load, and an alert storm would
	// otherwise start one per alert.
	MaxConcurrent int
	// MinSeverityRank skips incidents below a severity rank. Zero investigates
	// everything.
	MinSeverityRank int64
	// Attempts bounds retries of a failed investigation.
	Attempts int
	// Timeout bounds a single investigation end to end.
	Timeout time.Duration
	// QueueTimeout bounds how long an investigation waits for a free slot.
	//
	// Webhook-driven investigations carry a context that is never cancelled, so
	// without this they queue indefinitely: during a storm the twentieth
	// incident waits behind nineteen others, then spends on a model to analyse
	// something that moved on a quarter of an hour ago. Giving up is the more
	// useful answer. A non-positive value waits forever.
	QueueTimeout time.Duration
	// Cooldown is how long an incident is left alone after being investigated.
	//
	// This is what makes a feedback loop impossible. Writing an analysis back
	// to incident.io is itself a change, and incident.io emits an event for it;
	// without a cooldown that event can re-trigger the investigation that
	// caused it, and the bridge bills an LLM in a loop. Every incident.io
	// event for an incident inside the window is ignored.
	Cooldown time.Duration
}

// Service runs investigations.
type Service struct {
	logger    *zap.Logger
	incidents IncidentIO
	holmes    Holmes
	metrics   *metrics.Metrics
	cfg       Config

	// slots bounds concurrency across every trigger.
	slots chan struct{}

	// inflight suppresses duplicate investigations of the same incident, which
	// arrive constantly: incident.io fires an event per alert, per status
	// change, and per edit.
	//
	// lastRun extends that protection past the end of a run, so the event the
	// write-back itself produces cannot start the next one.
	mu       sync.Mutex
	inflight map[string]bool
	lastRun  map[string]time.Time

	// now is overridable so the cooldown can be tested without sleeping.
	now func() time.Time
}

// DefaultCooldown is how long an incident is left alone after an investigation.
// A negative Cooldown disables the window, which only makes sense in tests.
const DefaultCooldown = 30 * time.Minute

// DefaultQueueTimeout is how long an investigation waits for a slot before
// giving up. Sized against a typical investigation: waiting several times
// longer than one takes means answering a question nobody is still asking.
const DefaultQueueTimeout = 5 * time.Minute

// New builds the service, filling in sensible defaults for a zero Config.
func New(
	logger *zap.Logger, client IncidentIO, holmesClient Holmes, m *metrics.Metrics, cfg Config,
) *Service {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 2
	}

	if cfg.Attempts <= 0 {
		cfg.Attempts = 2
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}

	if cfg.WriteBack == "" {
		cfg.WriteBack = WriteBackUpdate
	}

	if cfg.Cooldown == 0 {
		cfg.Cooldown = DefaultCooldown
	}

	if cfg.QueueTimeout == 0 {
		cfg.QueueTimeout = DefaultQueueTimeout
	}

	return &Service{
		logger:    logger.Named("investigate"),
		incidents: client,
		holmes:    holmesClient,
		metrics:   m,
		cfg:       cfg,
		slots:     make(chan struct{}, cfg.MaxConcurrent),
		inflight:  map[string]bool{},
		lastRun:   map[string]time.Time{},
		now:       time.Now,
	}
}

// Result reports what an investigation did.
type Result struct {
	IncidentID string
	Reference  string
	Name       string
	Permalink  string
	Analysis   string
	ToolCalls  int
	Duration   time.Duration
	WrittenTo  string
	Skipped    string
}

var (
	// ErrAlreadyRunning is returned when an incident is already being investigated.
	ErrAlreadyRunning = errors.New("an investigation is already running for this incident")
	// ErrCoolingDown is returned when an incident was investigated too recently.
	// This is the guard that stops the bridge reacting to its own write-back.
	ErrCoolingDown = errors.New("this incident was investigated too recently")
	// ErrTooBusy is returned when no investigation slot came free in time.
	ErrTooBusy = errors.New("no investigation slot came free in time")
)

// Investigate runs an investigation for one incident, end to end.
//
// It is safe to call for the same incident concurrently: the second call
// returns ErrAlreadyRunning rather than starting a duplicate.
// Investigate runs an investigation for one incident, end to end.
//
// It is safe to call for the same incident concurrently: the second call
// returns ErrAlreadyRunning rather than starting a duplicate.
func (s *Service) Investigate(ctx context.Context, incidentID string) (*Result, error) {
	return s.guarded(ctx, incidentID, func(ctx context.Context, _ time.Time) (*Result, error) {
		return s.run(ctx, incidentID)
	})
}

// guarded wraps one investigation with everything that is true of all of them,
// whatever they are about: one at a time per subject, a bounded number at once,
// a cooldown afterwards, and metrics.
func (s *Service) guarded(
	ctx context.Context, key string, run func(context.Context, time.Time) (*Result, error),
) (*Result, error) {
	if err := s.claim(key); err != nil {
		return nil, err
	}

	// The cooldown exists to stop the bridge reacting to its own write-back, so
	// it should start only when there was a write-back. An investigation that
	// failed — Holmes unreachable, the request cancelled, the model erroring —
	// posted nothing and created no loop to prevent. Arming the window anyway
	// would lock the subject out for half an hour on a single transient fault,
	// which during a real incident is exactly when you want the retry.
	completed := false
	defer func() { s.release(key, completed) }()

	// Acquire a slot before doing any work, so a burst of incidents queues
	// rather than stampeding HolmesGPT — but bounded, so the queue cannot grow into a
	// backlog of analyses nobody will read.
	waitCtx := ctx

	if s.cfg.QueueTimeout > 0 {
		var cancelWait context.CancelFunc

		waitCtx, cancelWait = context.WithTimeout(ctx, s.cfg.QueueTimeout)
		defer cancelWait()
	}

	queued := time.Now()

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-waitCtx.Done():
		if ctx.Err() == nil {
			return nil, fmt.Errorf("%w: waited %s with %d running",
				ErrTooBusy, s.cfg.QueueTimeout, s.cfg.MaxConcurrent)
		}

		return nil, fmt.Errorf("gave up waiting for an investigation slot: %w", ctx.Err())
	}

	if waited := time.Since(queued); waited > time.Second {
		s.logger.Info("investigation queued behind others",
			zap.String("subject", key), zap.Duration("waited", waited))
	}

	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	started := time.Now()

	s.metrics.InvestigationsInFlight.Add(ctx, 1)
	defer s.metrics.InvestigationsInFlight.Add(ctx, -1)

	result, err := run(ctx, started)
	completed = err == nil

	attrs := metric.WithAttributes(attribute.String("outcome", outcome(result, err)))
	s.metrics.InvestigationDuration.Record(ctx, time.Since(started).Seconds(), attrs)

	if err != nil {
		s.metrics.InvestigationsFailed.Add(ctx, 1)
		return nil, err
	}

	s.metrics.InvestigationsSucceeded.Add(ctx, 1)

	return result, nil
}

func (s *Service) run(ctx context.Context, incidentID string) (*Result, error) {
	started := time.Now()

	incident, err := s.incidents.Incident(ctx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("failed to load the incident: %w", err)
	}

	log := s.logger.With(
		zap.String("incident_id", incidentID),
		zap.String("reference", incident.Reference))

	if reason, skip := s.shouldSkip(incident); skip {
		log.Info("skipping investigation", zap.String("reason", reason))

		return &Result{
			IncidentID: incidentID, Reference: incident.Reference, Name: incident.Name,
			Skipped: reason, Duration: time.Since(started),
		}, nil
	}

	// A missing alert list or update feed makes for a thinner prompt, not a
	// failed investigation — the incident itself is the essential part.
	alerts, err := s.incidents.IncidentAlerts(ctx, incidentID)
	if err != nil {
		log.Warn("could not load alerts; investigating without them", zap.Error(err))
	}

	updates, err := s.incidents.IncidentUpdates(ctx, incidentID)
	if err != nil {
		log.Warn("could not load the update feed; investigating without it", zap.Error(err))
	}

	prompt := BuildPrompt(incident, alerts, updates)
	prompt.System = SystemPrompt(s.cfg.SystemPrompt, SystemPromptData{Source: "incident"})

	log.Info("starting investigation",
		zap.Int("alerts", len(alerts)),
		zap.Int("updates", len(updates)),
		zap.Int("prompt_bytes", len(prompt.Ask)))

	s.metrics.InvestigationsStarted.Add(ctx, 1)

	answer, err := s.ask(ctx, log, incident, prompt)
	if err != nil {
		return nil, err
	}

	result := &Result{
		IncidentID: incidentID,
		Reference:  incident.Reference,
		Name:       incident.Name,
		Permalink:  valueOr(incident.Permalink, ""),
		Analysis:   answer.Analysis,
		ToolCalls:  len(answer.ToolCalls),
		Duration:   time.Since(started),
	}

	if writeErr := s.writeBack(ctx, incident, result); writeErr != nil {
		return nil, writeErr
	}

	log.Info("investigation complete",
		zap.Int("tool_calls", result.ToolCalls),
		zap.String("written_to", result.WrittenTo),
		zap.Duration("took", result.Duration))

	return result, nil
}

// ask calls HolmesGPT, retrying transient failures.
func (s *Service) ask(
	ctx context.Context, log *zap.Logger, incident incidentio.IncidentV2, prompt Prompt,
) (*holmes.ChatResponse, error) {
	// Keyed on the incident so a follow-up investigation of the same incident
	// is recognisable as the same conversation in HolmesGPT.
	return s.askWith(ctx, log, "incidentio-"+incident.Id, prompt)
}

// askWith calls HolmesGPT, retrying transient failures.
func (s *Service) askWith(
	ctx context.Context, log *zap.Logger, conversationID string, prompt Prompt,
) (*holmes.ChatResponse, error) {
	req := holmes.ChatRequest{
		Ask:                    prompt.Ask,
		AdditionalSystemPrompt: prompt.System,
		ConversationID:         conversationID,
		RequestSource:          "holmes-bridge",
	}

	var lastErr error

	for attempt := 1; attempt <= s.cfg.Attempts; attempt++ {
		started := time.Now()
		answer, err := s.holmes.Ask(ctx, req)

		s.metrics.HolmesDuration.Record(ctx, time.Since(started).Seconds())
		s.metrics.HolmesRequests.Add(ctx, 1,
			metric.WithAttributes(attribute.Bool("ok", err == nil)))

		if err == nil {
			if strings.TrimSpace(answer.Analysis) == "" {
				return nil, errors.New("holmesgpt returned an empty analysis")
			}

			return answer, nil
		}

		lastErr = err

		if !holmes.IsRetryable(err) || attempt == s.cfg.Attempts {
			break
		}

		log.Warn("holmesgpt call failed, retrying",
			zap.Int("attempt", attempt), zap.Error(err))

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("investigation cancelled: %w", ctx.Err())
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}

	return nil, fmt.Errorf("holmesgpt could not complete the investigation: %w", lastErr)
}

// writeBack records the analysis on the incident.
func (s *Service) writeBack(ctx context.Context, incident incidentio.IncidentV2, result *Result) error {
	body := formatAnalysis(result)

	// incident.io deduplicates on the idempotency key, so a redelivered webhook
	// updates nothing twice. Keying on the analysis means a genuinely new
	// conclusion still posts.
	key := idempotencyKey(incident.Id, result.Analysis)

	switch s.cfg.WriteBack {
	case WriteBackNone:
		result.WrittenTo = "nowhere (write-back disabled)"
		s.logger.Info("analysis not written back",
			zap.String("incident_id", incident.Id), zap.String("analysis", result.Analysis))

		return nil

	case WriteBackTimeline:
		if _, err := s.incidents.AddTimelineItem(ctx, incident.Id,
			"HolmesGPT investigation", body, key, time.Now().UTC()); err != nil {
			return fmt.Errorf("failed to add the analysis to the timeline: %w", err)
		}

		result.WrittenTo = "timeline"

		return nil

	case WriteBackUpdate:
		if _, err := s.incidents.PostUpdate(ctx, incident.Id, body, key); err != nil {
			return fmt.Errorf("failed to post the analysis as an update: %w", err)
		}

		result.WrittenTo = "incident update"

		return nil

	default:
		return fmt.Errorf("unknown write-back mode %q", s.cfg.WriteBack)
	}
}

// shouldSkip filters out incidents not worth spending an investigation on.
func (s *Service) shouldSkip(incident incidentio.IncidentV2) (string, bool) {
	// A closed incident does not need a root cause posted into it.
	if incident.IncidentStatus.Category == incidentio.IncidentStatusV2CategoryClosed ||
		incident.IncidentStatus.Category == incidentio.IncidentStatusV2CategoryDeclined {
		return "incident is " + string(incident.IncidentStatus.Category), true
	}

	// Test and tutorial incidents are noise: they exist to exercise incident.io
	// itself, and investigating them wastes both LLM spend and responder trust.
	if incident.Mode == incidentio.IncidentV2ModeTest ||
		incident.Mode == incidentio.IncidentV2ModeTutorial {
		return "incident mode is " + string(incident.Mode), true
	}

	if s.cfg.MinSeverityRank > 0 {
		if incident.Severity == nil {
			return "incident has no severity and a minimum is configured", true
		}

		if incident.Severity.Rank < s.cfg.MinSeverityRank {
			return fmt.Sprintf("severity %s ranks below the configured minimum", incident.Severity.Name), true
		}
	}

	return "", false
}

// claim reserves an incident for investigation, or reports why it cannot be.
func (s *Service) claim(incidentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.inflight[incidentID] {
		return ErrAlreadyRunning
	}

	if s.cfg.Cooldown > 0 {
		if last, ok := s.lastRun[incidentID]; ok {
			if elapsed := s.now().Sub(last); elapsed < s.cfg.Cooldown {
				return fmt.Errorf("%w: %s left of %s",
					ErrCoolingDown, (s.cfg.Cooldown - elapsed).Round(time.Second), s.cfg.Cooldown)
			}
		}
	}

	s.inflight[incidentID] = true

	return nil
}

// release ends the claim, and starts the cooldown only if the investigation
// actually finished. See the call site for why a failure must not arm it.
func (s *Service) release(incidentID string, completed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inflight, incidentID)

	if completed {
		s.lastRun[incidentID] = s.now()
	}
}

// formatAnalysis wraps HolmesGPT's answer with the provenance a responder needs
// to weigh it: this is a machine's opinion, not a colleague's.
func formatAnalysis(result *Result) string {
	var b strings.Builder

	b.WriteString("🔍 **Automated investigation by HolmesGPT**\n\n")
	b.WriteString(strings.TrimSpace(result.Analysis))
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "_%d tool calls, %s. This is AI-generated: verify before acting._",
		result.ToolCalls, result.Duration.Round(time.Second))

	return b.String()
}

// idempotencyKey derives a stable key from the incident and the analysis, so a
// redelivered webhook cannot post the same conclusion twice.
func idempotencyKey(incidentID, analysis string) string {
	sum := sha256.Sum256([]byte(incidentID + "\x00" + analysis))

	return "holmes-" + hex.EncodeToString(sum[:16])
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}

	return *p
}

func outcome(result *Result, err error) string {
	switch {
	case err != nil:
		return "error"
	case result != nil && result.Skipped != "":
		return "skipped"
	default:
		return "success"
	}
}

// Ensure the concrete client satisfies the narrowed interface.
var _ IncidentIO = (*infraincidentio.Client)(nil)

// Chat answers a question typed by a person, through the same system prompt an
// investigation uses.
//
// Deliberately not routed through guarded: the claim, the cooldown and the
// per-subject dedup all exist to stop webhook-driven storms re-investigating
// one incident. A human typing a question is none of those things, and a
// cooldown would refuse the obvious follow-up.
func (s *Service) Chat(ctx context.Context, ask string) (*Result, error) {
	ask = strings.TrimSpace(ask)
	if ask == "" {
		return nil, errors.New("nothing to ask: the request carried an empty question")
	}

	log := s.logger.Named("chat")
	started := s.now()

	answer, err := s.askWith(ctx, log, "", Prompt{
		Ask:    ask,
		System: SystemPrompt(s.cfg.SystemPrompt, SystemPromptData{Source: "chat"}),
	})
	if err != nil {
		return nil, err
	}

	return &Result{
		Analysis:  answer.Analysis,
		ToolCalls: len(answer.ToolCalls),
		Duration:  s.now().Sub(started),
		WrittenTo: string(WriteBackNone),
	}, nil
}
