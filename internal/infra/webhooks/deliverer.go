package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
)

const (
	// queueDepth bounds how many events can be waiting for delivery. A mock
	// that blocks its own API handlers because a subscriber is down would be
	// worse than one that drops, so overflow is dropped and logged loudly.
	queueDepth = 256

	defaultAttempts = 4
	defaultTimeout  = 10 * time.Second
	baseBackoff     = 250 * time.Millisecond
	maxBackoff      = 5 * time.Second

	// logSize bounds the retained delivery log the control plane exposes.
	logSize = 200
)

// Attempt records one delivery attempt, for the control plane to show.
type Attempt struct {
	Subscription string             `json:"subscription"`
	URL          string             `json:"url"`
	EventType    webhooks.EventType `json:"event_type"`
	WebhookID    string             `json:"webhook_id"`
	Attempt      int                `json:"attempt"`
	StatusCode   int                `json:"status_code,omitempty"`
	Error        string             `json:"error,omitempty"`
	At           time.Time          `json:"at"`
	Duration     string             `json:"duration"`
}

// Deliverer signs events and POSTs them to subscribers, off the request path.
type Deliverer struct {
	logger        *zap.Logger
	client        *http.Client
	signer        *webhooks.Signer
	perSubSigners map[string]*webhooks.Signer
	subscriptions []Subscription
	attempts      int

	queue chan job

	mu  sync.RWMutex
	log []Attempt

	wg sync.WaitGroup
}

type job struct {
	envelope webhooks.Envelope
	body     []byte
	id       string
}

// NewDeliverer builds a deliverer. The default secret signs any subscription
// that does not carry its own.
func NewDeliverer(
	logger *zap.Logger, defaultSecret string, subs []Subscription,
) (*Deliverer, error) {
	signer, err := webhooks.NewSigner(defaultSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to build the default webhook signer: %w", err)
	}

	perSub := map[string]*webhooks.Signer{}

	for _, sub := range subs {
		if sub.Secret == "" {
			continue
		}

		s, sErr := webhooks.NewSigner(sub.Secret)
		if sErr != nil {
			return nil, fmt.Errorf("subscription %q: %w", sub.Name, sErr)
		}

		perSub[sub.Name] = s
	}

	return &Deliverer{
		logger:        logger.Named("webhooks"),
		client:        &http.Client{Timeout: defaultTimeout},
		signer:        signer,
		perSubSigners: perSub,
		subscriptions: subs,
		attempts:      defaultAttempts,
		queue:         make(chan job, queueDepth),
	}, nil
}

// Subscriptions returns the configured endpoints, for the control plane.
func (d *Deliverer) Subscriptions() []Subscription { return d.subscriptions }

// Emit queues an event for delivery and returns immediately.
//
// It returns the webhook ID assigned to the delivery, so a caller can correlate
// it with the delivery log. An event is dropped rather than queued if the queue
// is full, because stalling an API handler on a dead subscriber is worse.
func (d *Deliverer) Emit(eventType webhooks.EventType, resource any) (string, error) {
	envelope := webhooks.Envelope{EventType: eventType, Resource: resource}

	body, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("failed to marshal %s: %w", eventType, err)
	}

	id := "msg_" + fixtures.NewID()

	select {
	case d.queue <- job{envelope: envelope, body: body, id: id}:
		return id, nil
	default:
		d.logger.Error("webhook queue is full, dropping event",
			zap.String("event_type", string(eventType)), zap.String("webhook_id", id))

		return id, fmt.Errorf("webhook queue is full")
	}
}

// Start runs the delivery loop until ctx is cancelled, then drains what is
// already queued so a shutdown does not silently lose events.
func (d *Deliverer) Start(ctx context.Context) func() error {
	return func() error {
		d.logger.Info("webhook deliverer started",
			zap.Int("subscriptions", len(d.subscriptions)))

		for {
			select {
			case <-ctx.Done():
				d.drain()
				d.wg.Wait()
				d.logger.Info("webhook deliverer stopped")

				return nil
			case j := <-d.queue:
				d.dispatch(ctx, j)
			}
		}
	}
}

// drain delivers whatever is still queued at shutdown, without blocking.
func (d *Deliverer) drain() {
	for {
		select {
		case j := <-d.queue:
			// A fresh context: the parent is already cancelled, and these
			// deliveries are exactly what we are trying not to lose.
			ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
			d.dispatch(ctx, j)
			cancel()
		default:
			return
		}
	}
}

func (d *Deliverer) dispatch(ctx context.Context, j job) {
	for _, sub := range d.subscriptions {
		if !sub.Wants(j.envelope.EventType) {
			continue
		}

		d.wg.Add(1)

		go func(sub Subscription) {
			defer d.wg.Done()
			d.deliver(ctx, sub, j)
		}(sub)
	}
}

// deliver POSTs one event to one subscriber, retrying on transport errors and
// 5xx with exponential backoff. A 4xx is not retried: the subscriber has
// rejected the payload and will keep doing so.
func (d *Deliverer) deliver(ctx context.Context, sub Subscription, j job) {
	signer := d.signer
	if s, ok := d.perSubSigners[sub.Name]; ok {
		signer = s
	}

	for attempt := 1; attempt <= d.attempts; attempt++ {
		started := time.Now()
		status, err := d.post(ctx, sub, signer, j)

		d.record(Attempt{
			Subscription: sub.Name, URL: sub.URL, EventType: j.envelope.EventType,
			WebhookID: j.id, Attempt: attempt, StatusCode: status,
			Error: errString(err), At: started, Duration: time.Since(started).Round(time.Millisecond).String(),
		})

		switch {
		case err == nil && status < http.StatusBadRequest:
			d.logger.Debug("webhook delivered",
				zap.String("subscription", sub.Name),
				zap.String("event_type", string(j.envelope.EventType)),
				zap.Int("status", status), zap.Int("attempt", attempt))

			return

		case err == nil && status < http.StatusInternalServerError:
			// The subscriber rejected it. Retrying changes nothing.
			d.logger.Warn("webhook rejected, not retrying",
				zap.String("subscription", sub.Name),
				zap.String("event_type", string(j.envelope.EventType)),
				zap.Int("status", status))

			return
		}

		if attempt == d.attempts {
			d.logger.Error("webhook delivery failed, giving up",
				zap.String("subscription", sub.Name),
				zap.String("event_type", string(j.envelope.EventType)),
				zap.Int("attempts", attempt), zap.Int("status", status), zap.Error(err))

			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff(attempt)):
		}
	}
}

func (d *Deliverer) post(
	ctx context.Context, sub Subscription, signer *webhooks.Signer, j job,
) (int, error) {
	now := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(j.body))
	if err != nil {
		return 0, fmt.Errorf("failed to build webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "incident.io-mock-webhooks/1.0")
	req.Header.Set(webhooks.HeaderID, j.id)
	req.Header.Set(webhooks.HeaderTimestamp, fmt.Sprintf("%d", now.Unix()))
	req.Header.Set(webhooks.HeaderSignature, signer.Sign(j.id, now, j.body))

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("webhook POST failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	// Drain so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	return resp.StatusCode, nil
}

func (d *Deliverer) record(a Attempt) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.log = append(d.log, a)
	if len(d.log) > logSize {
		d.log = d.log[len(d.log)-logSize:]
	}
}

// Log returns the retained delivery attempts, newest last.
func (d *Deliverer) Log() []Attempt {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]Attempt, len(d.log))
	copy(out, d.log)

	return out
}

func backoff(attempt int) time.Duration {
	d := time.Duration(math.Pow(2, float64(attempt-1))) * baseBackoff
	if d > maxBackoff {
		return maxBackoff
	}

	return d
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
