// Package v1 implements the mock's control plane: the endpoints a test harness
// uses to drive the fake, as opposed to the incident.io API surface it fakes.
//
// Everything here lives under /_mock, a prefix the real API does not use, so
// nothing in this package can be mistaken for incident.io behaviour.
package v1

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
	"github.com/merlindorin/holmes-bridge/internal/infra/memory"
	infrawebhooks "github.com/merlindorin/holmes-bridge/internal/infra/webhooks"
)

// Server exposes the control plane.
type Server struct {
	logger    *zap.Logger
	store     *memory.Store
	scenarios map[string]*fixtures.Scenario
	deliverer *infrawebhooks.Deliverer

	active string
}

// NewServer builds the control plane over a store and its loaded scenarios.
func NewServer(
	logger *zap.Logger,
	store *memory.Store,
	scenarios map[string]*fixtures.Scenario,
	deliverer *infrawebhooks.Deliverer,
	active string,
) *Server {
	return &Server{
		logger:    logger.Named("control"),
		store:     store,
		scenarios: scenarios,
		deliverer: deliverer,
		active:    active,
	}
}

// Mount attaches the control plane to a router.
func (s *Server) Mount(r gin.IRouter) {
	g := r.Group("/_mock")
	g.GET("/status", s.status)
	g.GET("/scenarios", s.listScenarios)
	g.POST("/reset", s.reset)
	g.POST("/webhooks/fire", s.fireWebhook)
	g.GET("/webhooks/deliveries", s.deliveries)
	g.GET("/webhooks/subscriptions", s.subscriptions)
}

type statusResponse struct {
	Scenario  string         `json:"scenario"`
	Scenarios []string       `json:"scenarios"`
	Records   map[string]int `json:"records"`
}

func (s *Server) status(c *gin.Context) {
	c.JSON(http.StatusOK, statusResponse{
		Scenario:  s.active,
		Scenarios: s.scenarioNames(),
		Records:   s.store.Counts(),
	})
}

type scenarioSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Incidents   int    `json:"incidents"`
	Alerts      int    `json:"alerts"`
	Active      bool   `json:"active"`
}

func (s *Server) listScenarios(c *gin.Context) {
	out := make([]scenarioSummary, 0, len(s.scenarios))

	for name, sc := range s.scenarios {
		out = append(out, scenarioSummary{
			Name:        name,
			Description: sc.Description,
			Incidents:   len(sc.Incidents),
			Alerts:      len(sc.Alerts),
			Active:      name == s.active,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	c.JSON(http.StatusOK, gin.H{"scenarios": out})
}

type resetRequest struct {
	// Scenario names which fixture to load. Empty reloads the active one,
	// which is the common case between test cases.
	Scenario string `json:"scenario"`
}

func (s *Server) reset(c *gin.Context) {
	var req resetRequest

	// An empty body is the common case, so a decode failure is only an error
	// when the caller actually sent something.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			_ = c.Error(api.BadRequest("Request body must be {\"scenario\": \"<name>\"} or empty."))
			return
		}
	}

	name := req.Scenario
	if name == "" {
		name = s.active
	}

	scenario, ok := s.scenarios[name]
	if !ok {
		_ = c.Error(api.BadRequest("No scenario named " + name + " is loaded."))
		return
	}

	expanded, err := scenario.Expand(time.Now())
	if err != nil {
		_ = c.Error(api.Internal("failed to expand scenario " + name + ": " + err.Error()))
		return
	}

	s.store.Seed(expanded)
	s.active = name

	s.logger.Info("scenario reloaded", zap.String("scenario", name))

	c.JSON(http.StatusOK, statusResponse{
		Scenario:  name,
		Scenarios: s.scenarioNames(),
		Records:   s.store.Counts(),
	})
}

type fireRequest struct {
	EventType string `json:"event_type"`
	// Resource is the payload to deliver. When omitted, the mock looks up the
	// record named by ResourceID so a caller can fire a realistic event
	// without restating the whole object.
	Resource   any    `json:"resource"`
	ResourceID string `json:"resource_id"`
}

// fireWebhook emits an event on demand, which is how a test drives the bridge
// without having to provoke the state change that would normally cause it.
func (s *Server) fireWebhook(c *gin.Context) {
	var req fireRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(api.BadRequest("Request body must be {\"event_type\": ..., \"resource\"|\"resource_id\": ...}."))
		return
	}

	event := webhooks.EventType(req.EventType)
	if !event.Valid() {
		_ = c.Error(api.Validation("event_type", "/event_type",
			"Unknown event type "+req.EventType+". See GET /_mock/webhooks/subscriptions for the vocabulary."))

		return
	}

	resource := req.Resource

	if resource == nil {
		found, ok := s.lookup(c, req.ResourceID)
		if !ok {
			_ = c.Error(api.Validation("resource_id", "/resource_id",
				"No incident or alert with ID "+req.ResourceID+" exists. Pass an explicit resource instead."))

			return
		}

		resource = found
	}

	id, err := s.deliverer.Emit(event, resource)
	if err != nil {
		_ = c.Error(api.Internal("failed to queue the webhook: " + err.Error()))
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"webhook_id": id, "event_type": event})
}

// lookup finds a record to attach to a hand-fired event.
func (s *Server) lookup(c *gin.Context, id string) (any, bool) {
	if id == "" {
		return nil, false
	}

	ctx := c.Request.Context()

	if incident, err := memory.NewIncidentRepository(s.store).Get(ctx, id); err == nil {
		return incident, true
	}

	if alert, err := memory.NewAlertRepository(s.store).Get(ctx, id); err == nil {
		return alert, true
	}

	return nil, false
}

func (s *Server) deliveries(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"deliveries": s.deliverer.Log()})
}

type subscriptionSummary struct {
	Name      string               `json:"name"`
	URL       string               `json:"url"`
	Events    []webhooks.EventType `json:"events,omitempty"`
	OwnSecret bool                 `json:"own_secret"`
}

func (s *Server) subscriptions(c *gin.Context) {
	subs := s.deliverer.Subscriptions()
	out := make([]subscriptionSummary, 0, len(subs))

	for _, sub := range subs {
		out = append(out, subscriptionSummary{
			Name: sub.Name, URL: sub.URL, Events: sub.Events, OwnSecret: sub.HasOwnSecret(),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"subscriptions": out,
		"event_types":   webhooks.AllEventTypes,
	})
}

func (s *Server) scenarioNames() []string {
	out := make([]string, 0, len(s.scenarios))
	for name := range s.scenarios {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}
