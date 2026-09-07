package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/domain/alerts"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
)

func (s *Server) AlertsV2List(c *gin.Context, params incidentio.AlertsV2ListParams) {
	p := pageOf(params.After, params.PageSize)

	filter := alerts.Filter{
		Status:     oneOf(params.Status),
		AlertSonID: firstOf(oneOf(params.AlertSource)),
	}

	items, after, err := s.alerts.List(c.Request.Context(), filter, p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list alerts"))
		return
	}

	c.JSON(http.StatusOK, incidentio.AlertsListResultV2{
		Alerts:         items,
		PaginationMeta: metaV2(p, after),
	})
}

func (s *Server) AlertsV2Show(c *gin.Context, id string) {
	alert, err := s.alerts.Get(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "alert", id, err, alerts.ErrNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.AlertsShowResultV2{Alert: alert})
}

// statusResolved is the terminal alert status, both on the wire and in the
// resolve handler's guard.
const (
	statusFiring   = "firing"
	statusResolved = "resolved"
)

func (s *Server) AlertsV2Resolve(c *gin.Context, id string) {
	resolved, err := s.alerts.Update(c.Request.Context(), id, func(a *alerts.Alert) error {
		// Resolving an already-resolved alert is a no-op upstream rather than
		// an error, so the original resolution time is preserved.
		if a.Status == statusResolved {
			return nil
		}

		now := time.Now().UTC()
		a.Status = statusResolved
		a.ResolvedAt = &now
		a.UpdatedAt = now

		return nil
	})
	if err != nil {
		s.notFound(c, "alert", id, err, alerts.ErrNotFound)
		return
	}

	s.emit(webhooks.PublicAlertAlertResolvedV1, resolved)

	c.JSON(http.StatusOK, incidentio.AlertsResolveResultV2{Alert: resolved})
}

func (s *Server) AlertsV2ListIncidentAlerts(c *gin.Context, params incidentio.AlertsV2ListIncidentAlertsParams) {
	p := pageOf(params.After, params.PageSize)

	items, after, err := s.alerts.ListIncidentAlerts(c.Request.Context(),
		alerts.Filter{IncidentID: valueOr(params.IncidentId, "")}, p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list incident alerts"))
		return
	}

	c.JSON(http.StatusOK, incidentio.AlertsListIncidentAlertsResultV2{
		IncidentAlerts: items,
		PaginationMeta: metaV2(p, after),
	})
}

// AlertEventsV2CreateHTTP is how alerts get into the system: a monitoring tool
// POSTs an event to an HTTP alert source and incident.io creates or dedupes an
// alert from it. Tests use this to make an alert fire on demand.
func (s *Server) AlertEventsV2CreateHTTP(
	c *gin.Context, alertSourceConfigID string, _ incidentio.AlertEventsV2CreateHTTPParams,
) {
	var payload incidentio.AlertEventsCreateHTTPPayloadV2
	if !bindJSON(c, &payload) {
		return
	}

	if payload.Title == "" {
		_ = c.Error(api.Validation("title", "/title", "An alert event needs a title."))
		return
	}

	ctx := c.Request.Context()
	dedupeKey := valueOr(payload.DeduplicationKey, fixtures.NewID())

	// Deduplication is the whole point of the endpoint: a monitoring tool
	// re-sends the same event every evaluation interval, and only the first
	// should open an alert.
	if existing, found := s.alertByDeduplicationKey(ctx, dedupeKey); found {
		c.JSON(http.StatusAccepted, incidentio.AlertEventsCreateHTTPResultV2{
			DeduplicationKey: dedupeKey,
			Message:          fmt.Sprintf("Alert %s already exists for this deduplication key", existing.Id),
			Status:           "success",
		})

		return
	}

	now := time.Now().UTC()

	alert := alerts.Alert{
		Id:            fixtures.NewID(),
		AlertSourceId: alertSourceConfigID,
		Title:         payload.Title,
		Description:   payload.Description,
		SourceUrl:     payload.SourceUrl,
		// status is required upstream, but defaulting an empty one to firing
		// keeps hand-written curl payloads working.
		Status:           incidentio.AlertV2Status(orDefault(string(payload.Status), statusFiring)),
		DeduplicationKey: dedupeKey,
		Attributes:       []incidentio.AlertAttributeEntryV2{},
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if alert.Status == statusResolved {
		alert.ResolvedAt = &now
	}

	created, err := s.alerts.Create(ctx, alert)
	if err != nil {
		_ = c.Error(api.Internal("failed to record the alert event"))
		return
	}

	event := webhooks.PublicAlertAlertCreatedV1
	if created.Status == statusResolved {
		event = webhooks.PublicAlertAlertResolvedV1
	}

	s.emit(event, created)

	c.JSON(http.StatusAccepted, incidentio.AlertEventsCreateHTTPResultV2{
		DeduplicationKey: dedupeKey,
		Message:          fmt.Sprintf("Created alert %s", created.Id),
		Status:           "success",
	})
}

func (s *Server) alertByDeduplicationKey(ctx context.Context, key string) (alerts.Alert, bool) {
	all, _, err := s.alerts.List(ctx, alerts.Filter{}, pageOf(nil, int64(maxScan)))
	if err != nil {
		return alerts.Alert{}, false
	}

	for _, a := range all {
		if a.DeduplicationKey == key {
			return a, true
		}
	}

	return alerts.Alert{}, false
}

// maxScan bounds the dedupe lookup. A mock never holds enough alerts for this
// to matter, and an index would be the fix if it ever did.
const maxScan = 250

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
