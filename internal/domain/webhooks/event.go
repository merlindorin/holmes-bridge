// Package webhooks models incident.io's outbound webhooks: the event
// vocabulary, the payload envelope, and the signature scheme.
//
// Both binaries need this. The mock emits events; the bridge receives and
// verifies them.
package webhooks

import (
	"encoding/json"
	"fmt"
)

// EventType names one of the events incident.io can deliver. The full set is
// taken from https://docs.incident.io/openapi/webhooks.json.
type EventType string

// Public events carry the full resource. Private events carry only an ID,
// because the resource is restricted.
const (
	PublicIncidentIncidentCreatedV2                 EventType = "public_incident.incident_created_v2"
	PublicIncidentIncidentUpdatedV2                 EventType = "public_incident.incident_updated_v2"
	PublicIncidentIncidentStatusUpdatedV2           EventType = "public_incident.incident_status_updated_v2"
	PublicIncidentActionCreatedV1                   EventType = "public_incident.action_created_v1"
	PublicIncidentActionUpdatedV1                   EventType = "public_incident.action_updated_v1"
	PublicIncidentFollowUpCreatedV1                 EventType = "public_incident.follow_up_created_v1"
	PublicIncidentFollowUpCreatedV2                 EventType = "public_incident.follow_up_created_v2"
	PublicIncidentFollowUpUpdatedV1                 EventType = "public_incident.follow_up_updated_v1"
	PublicIncidentFollowUpUpdatedV2                 EventType = "public_incident.follow_up_updated_v2"
	PublicIncidentPostmortemDocumentStatusUpdatedV1 EventType = "public_incident.postmortem_document_status_updated_v1"

	PublicAlertAlertCreatedV1  EventType = "public_alert.alert_created_v1"
	PublicAlertAlertResolvedV1 EventType = "public_alert.alert_resolved_v1"

	PublicEscalationEscalationCreatedV1       EventType = "public_escalation.escalation_created_v1"
	PublicEscalationEscalationStatusUpdatedV1 EventType = "public_escalation.escalation_status_updated_v1"

	PrivateIncidentIncidentCreatedV2                 EventType = "private_incident.incident_created_v2"
	PrivateIncidentIncidentUpdatedV2                 EventType = "private_incident.incident_updated_v2"
	PrivateIncidentActionCreatedV1                   EventType = "private_incident.action_created_v1"
	PrivateIncidentActionUpdatedV1                   EventType = "private_incident.action_updated_v1"
	PrivateIncidentFollowUpCreatedV1                 EventType = "private_incident.follow_up_created_v1"
	PrivateIncidentFollowUpCreatedV2                 EventType = "private_incident.follow_up_created_v2"
	PrivateIncidentFollowUpUpdatedV1                 EventType = "private_incident.follow_up_updated_v1"
	PrivateIncidentFollowUpUpdatedV2                 EventType = "private_incident.follow_up_updated_v2"
	PrivateIncidentMembershipGrantedV1               EventType = "private_incident.membership_granted_v1"
	PrivateIncidentMembershipRevokedV1               EventType = "private_incident.membership_revoked_v1"
	PrivateIncidentPostmortemDocumentStatusUpdatedV1 EventType = "private_incident.postmortem_document_status_updated_v1"

	PrivateAlertAlertCreatedV1  EventType = "private_alert.alert_created_v1"
	PrivateAlertAlertResolvedV1 EventType = "private_alert.alert_resolved_v1"

	PrivateEscalationEscalationCreatedV1       EventType = "private_escalation.escalation_created_v1"
	PrivateEscalationEscalationStatusUpdatedV1 EventType = "private_escalation.escalation_status_updated_v1"

	ScheduleCreatedV1     EventType = "schedule.created_v1"
	ScheduleUpdatedV1     EventType = "schedule.updated_v1"
	ScheduleDeletedV1     EventType = "schedule.deleted_v1"
	ScheduleShiftChangeV1 EventType = "schedule.shift_change_v1"

	StatusPageIncidentUpdateSharedV1 EventType = "status_page_incident.update_shared_v1"
)

// AllEventTypes lists every event the mock knows how to emit.
//
//nolint:gochecknoglobals // a package-level catalogue is the point
var AllEventTypes = []EventType{
	PublicIncidentIncidentCreatedV2, PublicIncidentIncidentUpdatedV2,
	PublicIncidentIncidentStatusUpdatedV2, PublicIncidentActionCreatedV1,
	PublicIncidentActionUpdatedV1, PublicIncidentFollowUpCreatedV1,
	PublicIncidentFollowUpCreatedV2, PublicIncidentFollowUpUpdatedV1,
	PublicIncidentFollowUpUpdatedV2, PublicIncidentPostmortemDocumentStatusUpdatedV1,
	PublicAlertAlertCreatedV1, PublicAlertAlertResolvedV1,
	PublicEscalationEscalationCreatedV1, PublicEscalationEscalationStatusUpdatedV1,
	PrivateIncidentIncidentCreatedV2, PrivateIncidentIncidentUpdatedV2,
	PrivateIncidentActionCreatedV1, PrivateIncidentActionUpdatedV1,
	PrivateIncidentFollowUpCreatedV1, PrivateIncidentFollowUpCreatedV2,
	PrivateIncidentFollowUpUpdatedV1, PrivateIncidentFollowUpUpdatedV2,
	PrivateIncidentMembershipGrantedV1, PrivateIncidentMembershipRevokedV1,
	PrivateIncidentPostmortemDocumentStatusUpdatedV1,
	PrivateAlertAlertCreatedV1, PrivateAlertAlertResolvedV1,
	PrivateEscalationEscalationCreatedV1, PrivateEscalationEscalationStatusUpdatedV1,
	ScheduleCreatedV1, ScheduleUpdatedV1, ScheduleDeletedV1, ScheduleShiftChangeV1,
	StatusPageIncidentUpdateSharedV1,
}

// Valid reports whether e is an event incident.io actually sends.
func (e EventType) Valid() bool {
	for _, known := range AllEventTypes {
		if known == e {
			return true
		}
	}

	return false
}

// Envelope is the JSON body incident.io POSTs to a webhook endpoint.
//
// The resource is keyed by the event type itself, so the wire form of an
// incident-created event is:
//
//	{"event_type": "public_incident.incident_created_v2",
//	 "public_incident.incident_created_v2": {...incident...}}
//
// That dynamic key is why this marshals by hand rather than through struct tags.
type Envelope struct {
	EventType EventType
	Resource  any
}

// MarshalJSON renders the envelope with the resource under its event-type key.
func (e Envelope) MarshalJSON() ([]byte, error) {
	resource, err := json.Marshal(e.Resource)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal webhook resource: %w", err)
	}

	body := map[string]json.RawMessage{
		"event_type":        json.RawMessage(strconvQuote(string(e.EventType))),
		string(e.EventType): resource,
	}

	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal webhook envelope: %w", err)
	}

	return out, nil
}

// UnmarshalJSON pulls the resource out from under its event-type key, leaving
// it as raw JSON for the caller to decode into a concrete type.
func (e *Envelope) UnmarshalJSON(data []byte) error {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("failed to unmarshal webhook envelope: %w", err)
	}

	raw, ok := body["event_type"]
	if !ok {
		return ErrMissingEventType
	}

	var eventType string
	if err := json.Unmarshal(raw, &eventType); err != nil {
		return fmt.Errorf("failed to unmarshal event_type: %w", err)
	}

	e.EventType = EventType(eventType)

	if resource, found := body[eventType]; found {
		e.Resource = resource
	}

	return nil
}

// ResourceJSON returns the raw resource payload, when the envelope came off the
// wire. It returns nil for an envelope built in-process.
func (e Envelope) ResourceJSON() json.RawMessage {
	raw, ok := e.Resource.(json.RawMessage)
	if !ok {
		return nil
	}

	return raw
}

func strconvQuote(s string) []byte {
	quoted, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string cannot fail.
		return []byte(`""`)
	}

	return quoted
}
