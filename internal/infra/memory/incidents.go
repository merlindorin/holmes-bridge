package memory

import (
	"context"
	"slices"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// IncidentRepository serves incidents, their update feed, and their timeline
// out of a Store.
type IncidentRepository struct{ store *Store }

// NewIncidentRepository adapts a Store to the incidents domain port.
func NewIncidentRepository(s *Store) *IncidentRepository { return &IncidentRepository{store: s} }

var _ incidents.Repository = (*IncidentRepository)(nil)

func (r *IncidentRepository) List(
	_ context.Context, f incidents.Filter, p incidents.Page,
) ([]incidents.Incident, string, int, error) {
	all := r.store.incidents.all()

	matched := make([]incidents.Incident, 0, len(all))

	for _, in := range all {
		if matches(in, f) {
			matched = append(matched, in)
		}
	}

	page, after := paginate(matched, func(i incidents.Incident) string { return i.Id }, p)

	return page, after, len(matched), nil
}

// matches applies the query filters incident.io supports on GET /v2/incidents.
// An empty filter field means "no constraint", so an incident has to fail an
// explicitly requested constraint to be excluded.
func matches(in incidents.Incident, f incidents.Filter) bool {
	if len(f.Status) > 0 && !slices.Contains(f.Status, in.IncidentStatus.Id) {
		return false
	}

	if len(f.Severity) > 0 {
		if in.Severity == nil || !slices.Contains(f.Severity, in.Severity.Id) {
			return false
		}
	}

	if len(f.Mode) > 0 && !slices.Contains(f.Mode, string(in.Mode)) {
		return false
	}

	if f.Visibility != "" && string(in.Visibility) != f.Visibility {
		return false
	}

	if !f.UpdatedGTE.IsZero() && in.UpdatedAt.Before(f.UpdatedGTE) {
		return false
	}

	if !f.UpdatedLTE.IsZero() && in.UpdatedAt.After(f.UpdatedLTE) {
		return false
	}

	return true
}

func (r *IncidentRepository) Get(_ context.Context, id string) (incidents.Incident, error) {
	in, ok := r.store.incidents.get(id)
	if !ok {
		return incidents.Incident{}, incidents.ErrNotFound
	}

	return in, nil
}

func (r *IncidentRepository) Create(_ context.Context, in incidents.Incident) (incidents.Incident, error) {
	return r.store.incidents.put(in), nil
}

func (r *IncidentRepository) Update(
	_ context.Context, id string, mutate func(*incidents.Incident) error,
) (incidents.Incident, error) {
	// Every mutation refreshes updated_at and last_activity_at, the way the
	// real API does, so clients polling on those timestamps see the change.
	stamped := func(in *incidents.Incident) error {
		if err := mutate(in); err != nil {
			return err
		}

		now := time.Now().UTC()
		in.UpdatedAt = now
		in.LastActivityAt = now

		return nil
	}

	updated, found, err := r.store.incidents.update(id, stamped)
	if err != nil {
		return incidents.Incident{}, err
	}

	if !found {
		return incidents.Incident{}, incidents.ErrNotFound
	}

	return updated, nil
}

func (r *IncidentRepository) ListUpdates(
	_ context.Context, incidentID string, p incidents.Page,
) ([]incidents.Update, string, error) {
	all := r.store.updates.all()

	matched := make([]incidents.Update, 0, len(all))

	for _, u := range all {
		if incidentID == "" || u.IncidentId == incidentID {
			matched = append(matched, u)
		}
	}

	page, after := paginate(matched, func(u incidents.Update) string { return u.Id }, p)

	return page, after, nil
}

func (r *IncidentRepository) CreateUpdate(_ context.Context, in incidents.Update) (incidents.Update, error) {
	return r.store.updates.put(in), nil
}

func (r *IncidentRepository) ListTimelineItems(
	_ context.Context, incidentID string, p incidents.Page,
) ([]incidents.TimelineItem, string, error) {
	all := r.store.timelineItems.all()

	matched := make([]incidents.TimelineItem, 0, len(all))

	for _, t := range all {
		if incidentID == "" || t.IncidentId == incidentID {
			matched = append(matched, t)
		}
	}

	page, after := paginate(matched, func(t incidents.TimelineItem) string { return t.Id }, p)

	return page, after, nil
}

func (r *IncidentRepository) CreateTimelineItem(
	_ context.Context, in incidents.TimelineItem,
) (incidents.TimelineItem, error) {
	return r.store.timelineItems.put(in), nil
}

func (r *IncidentRepository) UpdateTimelineItem(
	_ context.Context, id string, mutate func(*incidents.TimelineItem) error,
) (incidents.TimelineItem, error) {
	updated, found, err := r.store.timelineItems.update(id, mutate)
	if err != nil {
		return incidents.TimelineItem{}, err
	}

	if !found {
		return incidents.TimelineItem{}, incidents.ErrTimelineItemNotFound
	}

	return updated, nil
}

// CatalogueRepository serves the reference tables incidents point at.
type CatalogueRepository struct{ store *Store }

// NewCatalogueRepository adapts a Store to the incidents reference-data port.
func NewCatalogueRepository(s *Store) *CatalogueRepository { return &CatalogueRepository{store: s} }

var _ incidents.CatalogueRepository = (*CatalogueRepository)(nil)

func (r *CatalogueRepository) Severities(context.Context) ([]incidents.Severity, error) {
	return r.store.severities, nil
}

func (r *CatalogueRepository) Severity(_ context.Context, id string) (incidents.Severity, error) {
	for _, s := range r.store.severities {
		if s.Id == id {
			return s, nil
		}
	}

	return incidents.Severity{}, incidents.ErrSeverityNotFound
}

func (r *CatalogueRepository) Statuses(context.Context) ([]incidentio.IncidentStatusV1, error) {
	return r.store.statuses, nil
}

func (r *CatalogueRepository) Status(_ context.Context, id string) (incidentio.IncidentStatusV1, error) {
	for _, s := range r.store.statuses {
		if s.Id == id {
			return s, nil
		}
	}

	return incidentio.IncidentStatusV1{}, incidents.ErrStatusNotFound
}

func (r *CatalogueRepository) Roles(context.Context) ([]incidents.Role, error) {
	return r.store.roles, nil
}

func (r *CatalogueRepository) Role(_ context.Context, id string) (incidents.Role, error) {
	for _, role := range r.store.roles {
		if role.Id == id {
			return role, nil
		}
	}

	return incidents.Role{}, incidents.ErrRoleNotFound
}

// Users returns the directory served by GET /v2/users.
func (r *CatalogueRepository) Users() []incidentio.UserV2 { return r.store.users }
