package memory

import (
	"context"
	"slices"

	"github.com/merlindorin/holmes-bridge/internal/domain/alerts"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// AlertRepository serves alerts and their attachment to incidents.
type AlertRepository struct{ store *Store }

// NewAlertRepository adapts a Store to the alerts domain port.
func NewAlertRepository(s *Store) *AlertRepository { return &AlertRepository{store: s} }

var _ alerts.Repository = (*AlertRepository)(nil)

func (r *AlertRepository) List(
	_ context.Context, f alerts.Filter, p incidents.Page,
) ([]alerts.Alert, string, error) {
	all := r.store.alerts.all()

	matched := make([]alerts.Alert, 0, len(all))

	for _, a := range all {
		if len(f.Status) > 0 && !slices.Contains(f.Status, string(a.Status)) {
			continue
		}

		if f.AlertSonID != "" && a.AlertSourceId != f.AlertSonID {
			continue
		}

		matched = append(matched, a)
	}

	page, after := paginate(matched, func(a alerts.Alert) string { return a.Id }, p)

	return page, after, nil
}

func (r *AlertRepository) Get(_ context.Context, id string) (alerts.Alert, error) {
	a, ok := r.store.alerts.get(id)
	if !ok {
		return alerts.Alert{}, alerts.ErrNotFound
	}

	return a, nil
}

func (r *AlertRepository) Create(_ context.Context, in alerts.Alert) (alerts.Alert, error) {
	return r.store.alerts.put(in), nil
}

func (r *AlertRepository) Update(
	_ context.Context, id string, mutate func(*alerts.Alert) error,
) (alerts.Alert, error) {
	updated, found, err := r.store.alerts.update(id, mutate)
	if err != nil {
		return alerts.Alert{}, err
	}

	if !found {
		return alerts.Alert{}, alerts.ErrNotFound
	}

	return updated, nil
}

func (r *AlertRepository) ListIncidentAlerts(
	_ context.Context, f alerts.Filter, p incidents.Page,
) ([]alerts.IncidentAlert, string, error) {
	all := r.store.incidentAlerts.all()

	matched := make([]alerts.IncidentAlert, 0, len(all))

	for _, ia := range all {
		if f.IncidentID != "" && ia.Incident.Id != f.IncidentID {
			continue
		}

		matched = append(matched, ia)
	}

	page, after := paginate(matched, func(a alerts.IncidentAlert) string { return a.Id }, p)

	return page, after, nil
}

func (r *AlertRepository) AttachToIncident(
	_ context.Context, in alerts.IncidentAlert,
) (alerts.IncidentAlert, error) {
	return r.store.incidentAlerts.put(in), nil
}
