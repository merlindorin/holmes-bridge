package fixtures

import (
	"fmt"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
)

// Expanded is a scenario turned into the wire objects the API serves.
type Expanded struct {
	Identity incidentio.IdentityV1

	Severities []incidentio.SeverityV2
	StatusesV1 []incidentio.IncidentStatusV1
	StatusesV2 map[string]incidentio.IncidentStatusV2
	Roles      []incidentio.IncidentRoleV2
	Users      []incidentio.UserV2

	CatalogTypes   []incidentio.CatalogTypeV2
	CatalogEntries []incidentio.CatalogEntryV2

	Alerts         []incidentio.AlertV2
	Incidents      []incidentio.IncidentV2
	IncidentAlerts []incidentio.IncidentAlertV2
	Updates        []incidentio.IncidentUpdateV2
	TimelineItems  []incidentio.IncidentTimelineItemV2
}

// Expand turns a scenario into wire objects, resolving relative timestamps
// against now.
func (s *Scenario) Expand(now time.Time) (*Expanded, error) {
	b := &builder{
		scenario: s,
		now:      now.UTC(),
		out:      &Expanded{StatusesV2: map[string]incidentio.IncidentStatusV2{}},
		severity: map[string]incidentio.SeverityV2{},
		role:     map[string]incidentio.EmbeddedIncidentRoleV2{},
		user:     map[string]incidentio.UserV2{},
		alert:    map[string]incidentio.AlertV2{},
		typeByID: map[string]incidentio.CatalogTypeV2{},
	}

	b.expandIdentity()
	b.expandReferenceTables()
	b.expandCatalog()

	if err := b.expandAlerts(); err != nil {
		return nil, err
	}

	if err := b.expandIncidents(); err != nil {
		return nil, err
	}

	return b.out, nil
}

// builder carries the lookup maps every stage of expansion needs, so the stages
// can be separate functions without threading six maps through each signature.
type builder struct {
	scenario *Scenario
	now      time.Time
	out      *Expanded

	severity map[string]incidentio.SeverityV2
	role     map[string]incidentio.EmbeddedIncidentRoleV2
	user     map[string]incidentio.UserV2
	alert    map[string]incidentio.AlertV2
	typeByID map[string]incidentio.CatalogTypeV2
}

func (b *builder) expandIdentity() {
	id := b.scenario.Identity

	b.out.Identity = incidentio.IdentityV1{
		Name:         orDefault(id.Name, "incident.io mock"),
		DashboardUrl: orDefault(id.DashboardURL, "https://app.incident.io/mock"),
		Roles:        identityRoles(id.Roles),
		TeamRoles:    []incidentio.IdentityV1TeamRoles{},
		Teams:        []incidentio.IdentityTeamV1{},
	}
}

func (b *builder) expandReferenceTables() {
	for _, sev := range b.scenario.Severities {
		v := incidentio.SeverityV2{
			Id: idFor(sev.ID), Name: sev.Name, Description: sev.Description,
			Rank: sev.Rank, CreatedAt: b.now, UpdatedAt: b.now,
		}
		b.out.Severities = append(b.out.Severities, v)
		b.severity[sev.ID] = v
	}

	for _, st := range b.scenario.Statuses {
		id := idFor(st.ID)

		b.out.StatusesV1 = append(b.out.StatusesV1, incidentio.IncidentStatusV1{
			Id: id, Name: st.Name, Description: st.Description,
			Category: incidentio.IncidentStatusV1Category(st.Category),
			Rank:     st.Rank, CreatedAt: b.now, UpdatedAt: b.now,
		})
		b.out.StatusesV2[st.ID] = incidentio.IncidentStatusV2{
			Id: id, Name: st.Name, Description: st.Description,
			Category: incidentio.IncidentStatusV2Category(st.Category),
			Rank:     st.Rank, CreatedAt: b.now, UpdatedAt: b.now,
		}
	}

	for _, r := range b.scenario.Roles {
		id := idFor(r.ID)
		required := r.Required

		b.out.Roles = append(b.out.Roles, incidentio.IncidentRoleV2{
			Id: id, Name: r.Name, Shortform: r.Shortform, Description: r.Description,
			Instructions: r.Instructions,
			RoleType:     incidentio.IncidentRoleV2RoleType(r.RoleType),
			CreatedAt:    b.now, UpdatedAt: b.now,
		})
		b.role[r.ID] = incidentio.EmbeddedIncidentRoleV2{
			Id: id, Name: r.Name, Shortform: r.Shortform, Description: r.Description,
			Instructions: r.Instructions,
			RoleType:     incidentio.EmbeddedIncidentRoleV2RoleType(r.RoleType),
			Required:     &required, CreatedAt: b.now, UpdatedAt: b.now,
		}
	}

	for _, u := range b.scenario.Users {
		v := incidentio.UserV2{
			Id: idFor(u.ID), Name: u.Name,
			Role: incidentio.UserV2Role(orDefault(u.Role, "responder")),
		}

		if u.Email != "" {
			email := u.Email
			v.Email = &email
		}

		if u.SlackUserID != "" {
			slackID := u.SlackUserID
			v.SlackUserId = &slackID
		}

		b.out.Users = append(b.out.Users, v)
		b.user[u.ID] = v
	}
}

func (b *builder) expandCatalog() {
	for _, ct := range b.scenario.Catalog.Types {
		v := incidentio.CatalogTypeV2{
			Id: idFor(ct.ID), Name: ct.Name, Description: ct.Description,
			TypeName: orDefault(ct.TypeName, `Custom["`+ct.Name+`"]`),
			Schema: incidentio.CatalogTypeSchemaV2{
				Version:    1,
				Attributes: []incidentio.CatalogTypeAttributeV2{},
			},
			Annotations: map[string]string{},
			CreatedAt:   b.now, UpdatedAt: b.now,
		}
		b.out.CatalogTypes = append(b.out.CatalogTypes, v)
		b.typeByID[ct.ID] = v
	}

	for _, ce := range b.scenario.Catalog.Entries {
		entry := incidentio.CatalogEntryV2{
			Id:              idFor(ce.ID),
			CatalogTypeId:   b.typeByID[ce.Type].Id,
			Name:            ce.Name,
			Aliases:         ce.Aliases,
			Rank:            ce.Rank,
			AttributeValues: map[string]incidentio.CatalogEntryEngineParamBindingV2{},
			CreatedAt:       b.now, UpdatedAt: b.now,
		}

		if entry.Aliases == nil {
			entry.Aliases = []string{}
		}

		if ce.ExternalID != "" {
			external := ce.ExternalID
			entry.ExternalId = &external
		}

		b.out.CatalogEntries = append(b.out.CatalogEntries, entry)
	}
}

func (b *builder) expandAlerts() error {
	for _, a := range b.scenario.Alerts {
		createdAt, err := parseTime(a.CreatedAt, b.now)
		if err != nil {
			return fmt.Errorf("alert %q: %w", a.Title, err)
		}

		v := incidentio.AlertV2{
			Id:               idFor(a.ID),
			AlertSourceId:    orDefault(a.SourceID, "01MOCKALERTSOURCE0000000000"),
			Title:            a.Title,
			Status:           incidentio.AlertV2Status(orDefault(a.Status, "firing")),
			DeduplicationKey: orDefault(a.DeduplicationKey, NewID()),
			Attributes:       []incidentio.AlertAttributeEntryV2{},
			CreatedAt:        createdAt,
			UpdatedAt:        createdAt,
		}

		if a.Description != "" {
			description := a.Description
			v.Description = &description
		}

		if a.SourceURL != "" {
			sourceURL := a.SourceURL
			v.SourceUrl = &sourceURL
		}

		if v.Status == "resolved" {
			resolved := createdAt.Add(time.Minute)
			v.ResolvedAt = &resolved
		}

		b.out.Alerts = append(b.out.Alerts, v)
		b.alert[a.ID] = v
	}

	return nil
}

// apiKeyActor is who a fixture-seeded record is attributed to when no user is
// named, matching how incident.io attributes API-driven changes.
func apiKeyActor() incidentio.ActorV2 {
	return incidentio.ActorV2{
		ApiKey: &incidentio.APIKeyActorV2{Id: "01MOCKAPIKEY00000000000000", Name: "mock api key"},
	}
}

func (b *builder) actorFor(userID string) incidentio.ActorV2 {
	if u, ok := b.user[userID]; ok {
		return incidentio.ActorV2{User: &u}
	}

	return apiKeyActor()
}

func (b *builder) expandIncidents() error {
	for _, in := range b.scenario.Incidents {
		if err := b.expandIncident(in); err != nil {
			return err
		}
	}

	return nil
}

func (b *builder) expandIncident(in Incident) error {
	createdAt, err := parseTime(in.CreatedAt, b.now)
	if err != nil {
		return fmt.Errorf("incident %q: %w", in.Name, err)
	}

	updatedAt, err := parseTime(in.UpdatedAt, createdAt)
	if err != nil {
		return fmt.Errorf("incident %q: %w", in.Name, err)
	}

	incident := incidentio.IncidentV2{
		Id:                      idFor(in.ID),
		Reference:               orDefault(in.Reference, fmt.Sprintf("%s%d", ReferencePrefix, in.ExternalID)),
		Name:                    in.Name,
		IncidentStatus:          b.out.StatusesV2[in.Status],
		Mode:                    incidentio.IncidentV2Mode(orDefault(in.Mode, "standard")),
		Visibility:              incidentio.IncidentV2Visibility(orDefault(in.Visibility, "public")),
		Creator:                 apiKeyActor(),
		CustomFieldEntries:      []incidentio.CustomFieldEntryV2{},
		IncidentRoleAssignments: []incidentio.IncidentRoleAssignmentV2{},
		TeamIds:                 []string{},
		SlackTeamId:             "T00MOCKTEAM",
		SlackChannelId:          orDefault(in.SlackChannelID, "C00MOCKCHANNEL"),
		CreatedAt:               createdAt,
		UpdatedAt:               updatedAt,
		LastActivityAt:          updatedAt,
	}

	if in.Summary != "" {
		summary := in.Summary
		incident.Summary = &summary
	}

	if in.SlackChannelName != "" {
		channel := in.SlackChannelName
		incident.SlackChannelName = &channel
	}

	if in.Permalink != "" {
		permalink := in.Permalink
		incident.Permalink = &permalink
	}

	if sev, ok := b.severity[in.Severity]; ok {
		incident.Severity = &sev
	}

	for roleID, userID := range in.Roles {
		assignment := incidentio.IncidentRoleAssignmentV2{Role: b.role[roleID]}
		if u, ok := b.user[userID]; ok {
			assignment.Assignee = &u
		}

		incident.IncidentRoleAssignments = append(incident.IncidentRoleAssignments, assignment)
	}

	b.out.Incidents = append(b.out.Incidents, incident)
	b.attachAlerts(in, incident)

	if updateErr := b.expandUpdates(in, incident, createdAt); updateErr != nil {
		return updateErr
	}

	return b.expandTimeline(in, incident, createdAt)
}

func (b *builder) attachAlerts(in Incident, incident incidentio.IncidentV2) {
	slim := incidentio.IncidentSlimV2{
		Id: incident.Id, Name: incident.Name, Reference: incident.Reference,
		ExternalId:     in.ExternalID,
		StatusCategory: incidentio.IncidentSlimV2StatusCategory(categoryOf(b.scenario, in.Status)),
		Visibility:     incidentio.IncidentSlimV2Visibility(incident.Visibility),
		Summary:        incident.Summary,
	}

	for _, alertID := range in.Alerts {
		a := b.alert[alertID]
		b.out.IncidentAlerts = append(b.out.IncidentAlerts, incidentio.IncidentAlertV2{
			Id:       NewID(),
			Incident: slim,
			Alert: incidentio.AlertSlimV2{
				Id: a.Id, Title: a.Title, AlertSourceId: a.AlertSourceId,
				DeduplicationKey: a.DeduplicationKey, Description: a.Description,
				SourceUrl: a.SourceUrl, Status: incidentio.AlertSlimV2Status(a.Status),
				ResolvedAt: a.ResolvedAt, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
			},
		})
	}
}

func (b *builder) expandUpdates(in Incident, incident incidentio.IncidentV2, createdAt time.Time) error {
	for _, u := range in.Updates {
		at, err := parseTime(u.CreatedAt, createdAt)
		if err != nil {
			return fmt.Errorf("incident %q update: %w", in.Name, err)
		}

		update := incidentio.IncidentUpdateV2{
			Id:                idFor(u.ID),
			IncidentId:        incident.Id,
			NewIncidentStatus: b.out.StatusesV2[orDefault(u.Status, in.Status)],
			Updater:           b.actorFor(u.Updater),
			CreatedAt:         at,
		}

		if u.Message != "" {
			message := u.Message
			update.Message = &message
		}

		if sev, ok := b.severity[u.Severity]; ok {
			update.NewSeverity = &sev
		}

		b.out.Updates = append(b.out.Updates, update)
	}

	return nil
}

func (b *builder) expandTimeline(in Incident, incident incidentio.IncidentV2, createdAt time.Time) error {
	for _, t := range in.Timeline {
		at, err := parseTime(t.Timestamp, createdAt)
		if err != nil {
			return fmt.Errorf("incident %q timeline item: %w", in.Name, err)
		}

		item := incidentio.IncidentTimelineItemV2{
			Id:         idFor(t.ID),
			IncidentId: incident.Id,
			Title:      t.Title,
			Timestamp:  at,
			Creator:    b.actorFor(t.Creator),
			CreatedAt:  at,
			UpdatedAt:  at,
		}

		if t.Description != "" {
			description := t.Description
			item.Description = &description
		}

		b.out.TimelineItems = append(b.out.TimelineItems, item)
	}

	return nil
}

func categoryOf(s *Scenario, statusID string) string {
	for _, st := range s.Statuses {
		if st.ID == statusID {
			return st.Category
		}
	}

	return "triage"
}

func identityRoles(roles []string) []incidentio.IdentityV1Roles {
	if len(roles) == 0 {
		roles = []string{"incident_creator", "incident_editor", "viewer"}
	}

	out := make([]incidentio.IdentityV1Roles, 0, len(roles))
	for _, r := range roles {
		out = append(out, incidentio.IdentityV1Roles(r))
	}

	return out
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
