package memory

import (
	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/domain/alerts"
	"github.com/merlindorin/holmes-bridge/internal/domain/catalog"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// Store holds every collection the mock serves. One Store is one simulated
// incident.io organisation.
type Store struct {
	incidents     *collection[incidents.Incident]
	updates       *collection[incidents.Update]
	timelineItems *collection[incidents.TimelineItem]

	alerts         *collection[alerts.Alert]
	incidentAlerts *collection[alerts.IncidentAlert]

	actions   *collection[incidentio.ActionV3]
	followUps *collection[incidentio.FollowUpV3]

	severities []incidents.Severity
	statuses   []incidentio.IncidentStatusV1
	roles      []incidents.Role
	users      []incidentio.UserV2

	catalogTypes   *collection[catalog.Type]
	catalogEntries *collection[catalog.Entry]

	identity incidentio.IdentityV1
}

// NewStore returns an empty store. Fixtures are loaded separately, so tests can
// build a store record by record when that reads better.
func NewStore() *Store {
	return &Store{
		incidents:      newCollection(func(i incidents.Incident) string { return i.Id }),
		updates:        newCollection(func(u incidents.Update) string { return u.Id }),
		timelineItems:  newCollection(func(t incidents.TimelineItem) string { return t.Id }),
		alerts:         newCollection(func(a alerts.Alert) string { return a.Id }),
		incidentAlerts: newCollection(func(a alerts.IncidentAlert) string { return a.Id }),
		actions:        newCollection(func(a incidentio.ActionV3) string { return a.Id }),
		followUps:      newCollection(func(f incidentio.FollowUpV3) string { return f.Id }),
		catalogTypes:   newCollection(func(t catalog.Type) string { return t.Id }),
		catalogEntries: newCollection(func(e catalog.Entry) string { return e.Id }),
	}
}

// Reset empties every collection. The control plane calls this between test
// cases so a scenario can be reloaded from scratch.
func (s *Store) Reset() {
	s.incidents.reset()
	s.updates.reset()
	s.timelineItems.reset()
	s.alerts.reset()
	s.incidentAlerts.reset()
	s.actions.reset()
	s.followUps.reset()
	s.catalogTypes.reset()
	s.catalogEntries.reset()

	s.severities = nil
	s.statuses = nil
	s.roles = nil
	s.users = nil
}

// Counts reports how many records of each kind are loaded, for the control
// plane's status endpoint.
func (s *Store) Counts() map[string]int {
	return map[string]int{
		"incidents":         s.incidents.len(),
		"incident_updates":  s.updates.len(),
		"timeline_items":    s.timelineItems.len(),
		"alerts":            s.alerts.len(),
		"incident_alerts":   s.incidentAlerts.len(),
		"actions":           s.actions.len(),
		"follow_ups":        s.followUps.len(),
		"catalog_types":     s.catalogTypes.len(),
		"catalog_entries":   s.catalogEntries.len(),
		"severities":        len(s.severities),
		"incident_statuses": len(s.statuses),
		"incident_roles":    len(s.roles),
		"users":             len(s.users),
	}
}

// Identity returns the key identity served by GET /v1/identity.
func (s *Store) Identity() incidentio.IdentityV1 { return s.identity }
