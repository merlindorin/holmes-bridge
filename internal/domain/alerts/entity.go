// Package alerts holds the ports backing the mock's alert endpoints.
package alerts

import (
	"context"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

type (
	// Alert is a full alert record, as returned by GET /v2/alerts/{id}.
	Alert = incidentio.AlertV2
	// IncidentAlert links an alert to the incident it was attached to.
	IncidentAlert = incidentio.IncidentAlertV2
)

// Filter narrows an alert list query. A zero Filter matches every alert.
type Filter struct {
	Status     []string
	AlertSonID string
	IncidentID string
}

// Repository stores alerts and their attachment to incidents.
type Repository interface {
	List(ctx context.Context, f Filter, p incidents.Page) (items []Alert, after string, err error)
	Get(ctx context.Context, id string) (Alert, error)
	Create(ctx context.Context, in Alert) (Alert, error)
	Update(ctx context.Context, id string, mutate func(*Alert) error) (Alert, error)

	ListIncidentAlerts(ctx context.Context, f Filter, p incidents.Page) (items []IncidentAlert, after string, err error)
	AttachToIncident(ctx context.Context, in IncidentAlert) (IncidentAlert, error)
}
