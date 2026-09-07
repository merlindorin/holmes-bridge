// Package v1 implements the Alertmanager webhook receiver.
//
// This is the front door of the pipeline that does not involve incident.io:
// Alertmanager POSTs a firing group, HolmesGPT investigates it, and the
// conclusion is pushed to a phone.
package v1

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/domain/alertmanager"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// maxBodyBytes bounds the webhook body. An Alertmanager group is a few KB even
// with a hundred alerts in it.
const maxBodyBytes = 4 << 20

// Investigator runs an investigation for an Alertmanager group.
type Investigator interface {
	InvestigateAlerts(ctx context.Context, payload *alertmanager.Payload) (*investigate.Result, error)
}

// Server receives Alertmanager webhooks.
type Server struct {
	logger  *zap.Logger
	runner  Investigator
	metrics *metrics.Metrics

	// tokens authenticate the sender. Alertmanager cannot sign its webhooks the
	// way incident.io does, so a bearer token is the only thing standing
	// between a public URL and anyone able to spend your model budget.
	tokens [][]byte
}

// NewServer builds the receiver. Passing no tokens leaves the endpoint open,
// which is only reasonable when it is not reachable from outside.
func NewServer(logger *zap.Logger, runner Investigator, m *metrics.Metrics, tokens ...string) *Server {
	accepted := make([][]byte, 0, len(tokens))

	for _, t := range tokens {
		if t != "" {
			accepted = append(accepted, []byte(t))
		}
	}

	return &Server{logger: logger.Named("alertmanager"), runner: runner, metrics: m, tokens: accepted}
}

// Mount attaches every route, for the local listener.
func (s *Server) Mount(r gin.IRouter) {
	r.POST("/webhooks/alertmanager", s.receive)
}

// MountPublic attaches what is safe to publish. It is the same single route:
// the receiver is the only thing a sender needs, and it is what the tunnel
// exists to carry.
func (s *Server) MountPublic(r gin.IRouter) {
	r.POST("/webhooks/alertmanager", s.receive)
}

// receive handles one Alertmanager notification.
//
// It acknowledges before investigating: Alertmanager gives a webhook receiver a
// short window and retries anything slower, and an investigation takes minutes.
func (s *Server) receive(c *gin.Context) {
	if !s.authenticate(c) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes))
	if err != nil {
		_ = c.Error(api.BadRequest("could not read the request body"))
		return
	}

	var payload alertmanager.Payload
	if decodeErr := json.Unmarshal(body, &payload); decodeErr != nil {
		_ = c.Error(api.BadRequest("body is not an Alertmanager webhook payload: " + decodeErr.Error()))
		return
	}

	if validateErr := payload.Validate(); validateErr != nil {
		// A payload with no alerts is not an error on Alertmanager's side, and
		// answering 4xx would make it retry something that will never differ.
		if errors.Is(validateErr, alertmanager.ErrNoAlerts) {
			c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "no alerts in payload"})
			return
		}

		_ = c.Error(api.Validation("version", "/version", validateErr.Error()))

		return
	}

	log := s.logger.With(
		zap.String("group_key", payload.Key()),
		zap.String("status", string(payload.Status)),
		zap.Int("alerts", len(payload.Alerts)))

	s.metrics.WebhooksReceived.Add(c.Request.Context(), 1,
		metric.WithAttributes(attribute.String("source", "alertmanager")))

	// Detached from the request context on purpose: the response is about to be
	// written, which would cancel it.
	go s.investigate(context.WithoutCancel(c.Request.Context()), log, &payload)

	c.JSON(http.StatusAccepted, gin.H{
		"status":    "investigating",
		"group_key": payload.Key(),
		"alerts":    len(payload.Alerts),
	})
}

func (s *Server) investigate(ctx context.Context, log *zap.Logger, payload *alertmanager.Payload) {
	result, err := s.runner.InvestigateAlerts(ctx, payload)

	switch {
	case errors.Is(err, investigate.ErrAlreadyRunning):
		// Expected: Alertmanager re-notifies for a group that keeps firing.
		log.Debug("investigation already running for this alert group")
	case errors.Is(err, investigate.ErrCoolingDown):
		log.Debug("alert group is cooling down", zap.Error(err))
	case errors.Is(err, investigate.ErrTooBusy):
		log.Warn("dropped an investigation, all slots busy", zap.Error(err))
	case err != nil:
		log.Error("investigation failed", zap.Error(err))
	case result.Skipped != "":
		log.Info("investigation skipped", zap.String("reason", result.Skipped))
	default:
		log.Info("investigation pushed", zap.Int("tool_calls", result.ToolCalls))
	}
}

// authenticate checks the bearer token, when one is configured.
func (s *Server) authenticate(c *gin.Context) bool {
	if len(s.tokens) == 0 {
		return true
	}

	token, ok := bearer(c.GetHeader("Authorization"))
	if ok {
		for _, accepted := range s.tokens {
			if subtle.ConstantTimeCompare([]byte(token), accepted) == 1 {
				return true
			}
		}
	}

	s.metrics.WebhooksRejected.Add(c.Request.Context(), 1,
		metric.WithAttributes(attribute.String("reason", "bad token")))
	_ = c.Error(api.Unauthorized(
		"Expected an Authorization header of the form 'Bearer <token>'. " +
			"Alertmanager sets one with http_config.authorization."))

	return false
}

func bearer(header string) (string, bool) {
	const prefix = "Bearer "

	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])

	return token, token != ""
}
