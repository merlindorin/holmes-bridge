package memory

import (
	"context"

	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// WorkRepository serves the actions and follow-ups attached to incidents.
type WorkRepository struct{ store *Store }

// NewWorkRepository adapts a Store to the incident work port.
func NewWorkRepository(s *Store) *WorkRepository { return &WorkRepository{store: s} }

var _ incidents.WorkRepository = (*WorkRepository)(nil)

func (r *WorkRepository) ListActions(
	_ context.Context, incidentID string, p incidents.Page,
) ([]incidents.Action, string, error) {
	matched := filterByIncident(r.store.actions.all(), incidentID,
		func(a incidents.Action) string { return a.IncidentId })

	page, after := paginate(matched, func(a incidents.Action) string { return a.Id }, p)

	return page, after, nil
}

func (r *WorkRepository) GetAction(_ context.Context, id string) (incidents.Action, error) {
	a, ok := r.store.actions.get(id)
	if !ok {
		return incidents.Action{}, incidents.ErrActionNotFound
	}

	return a, nil
}

func (r *WorkRepository) CreateAction(_ context.Context, in incidents.Action) (incidents.Action, error) {
	return r.store.actions.put(in), nil
}

func (r *WorkRepository) UpdateAction(
	_ context.Context, id string, mutate func(*incidents.Action) error,
) (incidents.Action, error) {
	updated, found, err := r.store.actions.update(id, mutate)
	if err != nil {
		return incidents.Action{}, err
	}

	if !found {
		return incidents.Action{}, incidents.ErrActionNotFound
	}

	return updated, nil
}

func (r *WorkRepository) DeleteAction(_ context.Context, id string) error {
	if !r.store.actions.delete(id) {
		return incidents.ErrActionNotFound
	}

	return nil
}

func (r *WorkRepository) ListFollowUps(
	_ context.Context, incidentID string, p incidents.Page,
) ([]incidents.FollowUp, string, error) {
	matched := filterByIncident(r.store.followUps.all(), incidentID,
		func(f incidents.FollowUp) string { return f.IncidentId })

	page, after := paginate(matched, func(f incidents.FollowUp) string { return f.Id }, p)

	return page, after, nil
}

func (r *WorkRepository) GetFollowUp(_ context.Context, id string) (incidents.FollowUp, error) {
	f, ok := r.store.followUps.get(id)
	if !ok {
		return incidents.FollowUp{}, incidents.ErrFollowUpNotFound
	}

	return f, nil
}

func (r *WorkRepository) CreateFollowUp(_ context.Context, in incidents.FollowUp) (incidents.FollowUp, error) {
	return r.store.followUps.put(in), nil
}

func (r *WorkRepository) UpdateFollowUp(
	_ context.Context, id string, mutate func(*incidents.FollowUp) error,
) (incidents.FollowUp, error) {
	updated, found, err := r.store.followUps.update(id, mutate)
	if err != nil {
		return incidents.FollowUp{}, err
	}

	if !found {
		return incidents.FollowUp{}, incidents.ErrFollowUpNotFound
	}

	return updated, nil
}

func (r *WorkRepository) DeleteFollowUp(_ context.Context, id string) error {
	if !r.store.followUps.delete(id) {
		return incidents.ErrFollowUpNotFound
	}

	return nil
}

// filterByIncident narrows a slice to one incident, or returns it whole when no
// incident was named.
func filterByIncident[T any](items []T, incidentID string, of func(T) string) []T {
	if incidentID == "" {
		return items
	}

	out := make([]T, 0, len(items))

	for _, item := range items {
		if of(item) == incidentID {
			out = append(out, item)
		}
	}

	return out
}
