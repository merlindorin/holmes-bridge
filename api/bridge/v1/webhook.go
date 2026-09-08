// Package v1 implements the bridge's HTTP surface: the incident.io webhook
// receiver, and a manual trigger for driving investigations by hand.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// statusKey is the field every acknowledgement carries, naming what the bridge
// decided to do with the delivery.
const statusKey = "status"

// maxBodyBytes bounds the webhook body read. incident.io payloads are a few KB;
// anything far larger is not a genuine delivery.
const maxBodyBytes = 1 << 20

// Investigator runs an investigation for an incident.
type Investigator interface {
	Investigate(ctx context.Context, incidentID string) (*investigate.Result, error)
	Chat(ctx context.Context, ask string) (*investigate.Result, error)
}

// Server receives incident.io webhooks and turns them into investigations.
type Server struct {
	logger   *zap.Logger
	verifier webhooks.Verifier
	runner   Investigator
	metrics  *metrics.Metrics
	triggers map[webhooks.EventType]bool
}

// NewServer builds the receiver. Events outside triggers are acknowledged and
// ignored, because incident.io has no per-event subscription granularity on
// some plans and the bridge should not 4xx on events it simply does not want.
func NewServer(
	logger *zap.Logger,
	verifier webhooks.Verifier,
	runner Investigator,
	m *metrics.Metrics,
	triggers []webhooks.EventType,
) *Server {
	set := make(map[webhooks.EventType]bool, len(triggers))
	for _, t := range triggers {
		set[t] = true
	}

	return &Server{logger: logger.Named("webhook"), verifier: verifier, runner: runner, metrics: m, triggers: set}
}

// Mount attaches every route, for the local listener.
//
// The manual trigger has no authentication of its own: it is reachable only to
// whoever can already reach the process, which on a loopback port or a cluster
// Service is the intended audience.
func (s *Server) Mount(r gin.IRouter) {
	r.POST("/webhooks/incidentio", s.receive)
	r.POST("/investigations/:incident_id", s.trigger)
	r.POST("/chat", s.chat)
}

// MountPublic attaches only what is safe to publish on the internet: the
// webhook receiver, which authenticates every delivery by signature.
//
// The manual trigger is deliberately absent. It takes no credential, so
// publishing it would let any caller start investigations — spending on a model
// and posting AI-written updates onto real incidents. Everything else the
// process serves (probes, /metrics) describes its internals and belongs on the
// local listener too.
func (s *Server) MountPublic(r gin.IRouter) {
	r.POST("/webhooks/incidentio", s.receive)
}

// receive handles an inbound incident.io webhook.
//
// It verifies, acknowledges, and only then investigates: incident.io retries a
// delivery that does not return quickly, and an investigation takes minutes.
func (s *Server) receive(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes))
	if err != nil {
		s.reject(c, "could not read the request body", err)
		return
	}

	if verifyErr := s.verifier.Verify(
		c.GetHeader(webhooks.HeaderID),
		c.GetHeader(webhooks.HeaderTimestamp),
		c.GetHeader(webhooks.HeaderSignature),
		body,
	); verifyErr != nil {
		s.reject(c, "signature verification failed", verifyErr)
		return
	}

	var envelope webhooks.Envelope
	if decodeErr := json.Unmarshal(body, &envelope); decodeErr != nil {
		s.reject(c, "body is not a webhook envelope", decodeErr)
		return
	}

	log := s.logger.With(
		zap.String("event_type", string(envelope.EventType)),
		zap.String("webhook_id", c.GetHeader(webhooks.HeaderID)))

	s.metrics.WebhooksReceived.Add(c.Request.Context(), 1,
		metric.WithAttributes(attribute.String("event_type", string(envelope.EventType))))

	if !s.triggers[envelope.EventType] {
		log.Debug("event is not a configured trigger, ignoring")
		c.JSON(http.StatusOK, gin.H{statusKey: "ignored", "reason": "event type is not a trigger"})

		return
	}

	incidentID, ok := incidentIDOf(envelope)
	if !ok {
		log.Warn("trigger event carried no incident id, ignoring")
		c.JSON(http.StatusOK, gin.H{statusKey: "ignored", "reason": "no incident id in payload"})

		return
	}

	// Detached from the request context on purpose: the response is about to be
	// written, which would cancel it.
	go s.investigate(context.WithoutCancel(c.Request.Context()), log, incidentID)

	c.JSON(http.StatusAccepted, gin.H{statusKey: "investigating", "incident_id": incidentID})
}

// trigger starts an investigation by hand, which is how you exercise the bridge
// against a real incident without waiting for one to happen.
func (s *Server) trigger(c *gin.Context) {
	incidentID := c.Param("incident_id")

	// Synchronous, unlike the webhook path: a human asked, so a human should
	// get the answer rather than have to go and look for it.
	result, err := s.runner.Investigate(c.Request.Context(), incidentID)
	if err != nil {
		if errors.Is(err, investigate.ErrAlreadyRunning) ||
			errors.Is(err, investigate.ErrCoolingDown) ||
			errors.Is(err, investigate.ErrTooBusy) {
			_ = c.Error(api.New(http.StatusConflict, api.TypeConflict, api.Single{
				Code: "investigation_not_started", Message: err.Error(),
			}))

			return
		}

		s.logger.Error("manual investigation failed",
			zap.String("incident_id", incidentID), zap.Error(err))
		_ = c.Error(api.Internal("investigation failed: " + err.Error()))

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"incident_id": result.IncidentID,
		"reference":   result.Reference,
		"skipped":     result.Skipped,
		"tool_calls":  result.ToolCalls,
		"written_to":  result.WrittenTo,
		"duration":    result.Duration.Round(time.Millisecond).String(),
		"analysis":    result.Analysis,
	})
}

func (s *Server) investigate(ctx context.Context, log *zap.Logger, incidentID string) {
	result, err := s.runner.Investigate(ctx, incidentID)

	switch {
	case errors.Is(err, investigate.ErrAlreadyRunning):
		// Expected: incident.io fires several events per incident and they all
		// point at the same investigation.
		log.Debug("investigation already running for this incident")
	case errors.Is(err, investigate.ErrTooBusy):
		// Expected under load, and worth seeing: it means investigations are
		// arriving faster than they can be run.
		log.Warn("dropped an investigation, all slots busy",
			zap.String("incident_id", incidentID), zap.Error(err))
	case errors.Is(err, investigate.ErrCoolingDown):
		// Also expected, and the reason the bridge does not react to its own
		// write-back in a loop.
		log.Debug("incident is cooling down", zap.String("incident_id", incidentID), zap.Error(err))
	case err != nil:
		log.Error("investigation failed", zap.String("incident_id", incidentID), zap.Error(err))
	case result.Skipped != "":
		log.Info("investigation skipped",
			zap.String("incident_id", incidentID), zap.String("reason", result.Skipped))
	default:
		log.Info("investigation posted",
			zap.String("incident_id", incidentID), zap.String("written_to", result.WrittenTo))
	}
}

func (s *Server) reject(c *gin.Context, reason string, err error) {
	s.logger.Warn("rejected webhook", zap.String("reason", reason), zap.Error(err))
	s.metrics.WebhooksRejected.Add(c.Request.Context(), 1,
		metric.WithAttributes(attribute.String("reason", reason)))

	// A 401 rather than a 400: an unverifiable delivery is an authentication
	// failure, and incident.io should not retry it.
	_ = c.Error(api.Unauthorized(reason))
}

// incidentIDOf digs the incident identifier out of an event payload.
//
// The shape varies by event: an incident event carries the incident itself,
// while an alert or follow-up event nests it. Rather than decode into a dozen
// generated types, this reads only the field it needs.
func incidentIDOf(envelope webhooks.Envelope) (string, bool) {
	raw := envelope.ResourceJSON()
	if raw == nil {
		return "", false
	}

	var payload struct {
		ID       string `json:"id"`
		Incident *struct {
			ID string `json:"id"`
		} `json:"incident"`
		IncidentID string `json:"incident_id"`
	}

	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false
	}

	switch {
	case payload.Incident != nil && payload.Incident.ID != "":
		return payload.Incident.ID, true
	case payload.IncidentID != "":
		return payload.IncidentID, true
	case slices.Contains(incidentEvents, envelope.EventType) && payload.ID != "":
		return payload.ID, true
	default:
		return "", false
	}
}

// incidentEvents are the events whose payload *is* an incident, so its own id
// is the incident id. Every other event references an incident instead.
//
//nolint:gochecknoglobals // a package-level lookup list is the clearest form
var incidentEvents = []webhooks.EventType{
	webhooks.PublicIncidentIncidentCreatedV2,
	webhooks.PublicIncidentIncidentUpdatedV2,
	webhooks.PublicIncidentIncidentStatusUpdatedV2,
	webhooks.PrivateIncidentIncidentCreatedV2,
	webhooks.PrivateIncidentIncidentUpdatedV2,
}

// chatRequest is a question typed by a person.
type chatRequest struct {
	Ask string `json:"ask"`
}

// chat answers a free-form question through the bridge's system prompt.
//
// The point is the prompt: asking HolmesGPT directly gets its stock behaviour,
// while going through here applies the same structure and rules an
// investigation gets — which is what makes the two answers comparable.
//
// Local listener only, like the manual trigger: it takes no credential and
// spends on a model.
func (s *Server) chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(api.New(http.StatusBadRequest, api.TypeValidation, api.Single{
			Code: "invalid_request", Message: `body must be {"ask": "..."}`,
		}))

		return
	}

	result, err := s.runner.Chat(c.Request.Context(), req.Ask)
	if err != nil {
		s.logger.Error("chat failed", zap.Error(err))
		_ = c.Error(api.Internal("chat failed: " + err.Error()))

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"analysis":   result.Analysis,
		"tool_calls": result.ToolCalls,
		"duration":   result.Duration.String(),
	})
}
