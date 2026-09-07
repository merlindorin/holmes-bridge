package memory

import (
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
)

// Seed replaces the store's contents with an expanded scenario.
//
// It resets first, so loading a scenario twice is idempotent and switching
// scenarios never leaves records from the previous one behind.
func (s *Store) Seed(e *fixtures.Expanded) {
	s.Reset()

	s.identity = e.Identity
	s.severities = e.Severities
	s.statuses = e.StatusesV1
	s.roles = e.Roles
	s.users = e.Users

	for _, t := range e.CatalogTypes {
		s.catalogTypes.put(t)
	}

	for _, entry := range e.CatalogEntries {
		s.catalogEntries.put(entry)
	}

	for _, a := range e.Alerts {
		s.alerts.put(a)
	}

	for _, ia := range e.IncidentAlerts {
		s.incidentAlerts.put(ia)
	}

	for _, in := range e.Incidents {
		s.incidents.put(in)
	}

	for _, u := range e.Updates {
		s.updates.put(u)
	}

	for _, t := range e.TimelineItems {
		s.timelineItems.put(t)
	}
}
