package webhooks

import "github.com/merlindorin/holmes-bridge/api/incidentio"

// Some events do not carry a bare resource. Their payload wraps it alongside
// what changed, and a receiver written against the real API will look for the
// resource in that nested position. These types exist so the mock emits the
// same shape rather than a flat resource that only its own consumers can read.

// IncidentWithStatusChange is the payload of
// public_incident.incident_status_updated_v2.
//
// Note that the incident is nested under `incident`, not spread at the top
// level as it is for incident_created_v2 — the difference matters to anything
// pulling an incident ID out of the payload.
type IncidentWithStatusChange struct {
	Incident       incidentio.IncidentV2       `json:"incident"`
	PreviousStatus incidentio.IncidentStatusV2 `json:"previous_status"`
	NewStatus      incidentio.IncidentStatusV2 `json:"new_status"`
	Message        *string                     `json:"message,omitempty"`
}
