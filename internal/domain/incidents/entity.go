// Package incidents holds the ports the incident.io mock serves its incident
// endpoints from.
//
// The entities here are aliases of the OpenAPI-generated wire types rather than
// a hand-rolled model. For a fake, the spec is the source of truth: a parallel
// domain struct plus mappers would double the code and give us a second place
// for the wire shape to drift. The repository interfaces still live in the
// domain, so a Postgres adapter can replace the in-memory one untouched.
package incidents

import (
	"context"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
)

type (
	// Incident is a full incident record, as returned by GET /v2/incidents/{id}.
	Incident = incidentio.IncidentV2
	// Update is an entry in an incident's update feed.
	Update = incidentio.IncidentUpdateV2
	// TimelineItem is a custom entry on an incident's timeline.
	TimelineItem = incidentio.IncidentTimelineItemV2
	// Status is a workflow status an incident can occupy.
	Status = incidentio.IncidentStatusV2
	// Severity classifies incident impact.
	Severity = incidentio.SeverityV2
	// Role is an incident role such as lead or communications.
	Role = incidentio.IncidentRoleV2
)

// Filter narrows a list query. A zero Filter matches every incident.
type Filter struct {
	Status     []string
	Severity   []string
	Mode       []string
	Visibility string
	UpdatedGTE time.Time
	UpdatedLTE time.Time
}

// Page requests a slice of results using incident.io's cursor pagination.
type Page struct {
	After    string
	PageSize int
}

// DefaultPageSize mirrors what incident.io serves when page_size is omitted.
const DefaultPageSize = 25

// MaxPageSize mirrors the upstream ceiling on page_size.
const MaxPageSize = 250

// Normalise clamps a page request into the range the real API accepts.
func (p Page) Normalise() Page {
	switch {
	case p.PageSize <= 0:
		p.PageSize = DefaultPageSize
	case p.PageSize > MaxPageSize:
		p.PageSize = MaxPageSize
	}

	return p
}

// Repository stores incidents and the records hanging off them.
type Repository interface {
	List(ctx context.Context, f Filter, p Page) (items []Incident, after string, total int, err error)
	Get(ctx context.Context, id string) (Incident, error)
	Create(ctx context.Context, in Incident) (Incident, error)
	Update(ctx context.Context, id string, mutate func(*Incident) error) (Incident, error)

	ListUpdates(ctx context.Context, incidentID string, p Page) (items []Update, after string, err error)
	CreateUpdate(ctx context.Context, in Update) (Update, error)

	ListTimelineItems(ctx context.Context, incidentID string, p Page) (items []TimelineItem, after string, err error)
	CreateTimelineItem(ctx context.Context, in TimelineItem) (TimelineItem, error)
	UpdateTimelineItem(ctx context.Context, id string, mutate func(*TimelineItem) error) (TimelineItem, error)
}

// CatalogueRepository serves the small reference tables incidents point at.
// These are read-only in the mock: they come from fixtures and never change.
type CatalogueRepository interface {
	Severities(ctx context.Context) ([]Severity, error)
	Severity(ctx context.Context, id string) (Severity, error)
	Statuses(ctx context.Context) ([]incidentio.IncidentStatusV1, error)
	Status(ctx context.Context, id string) (incidentio.IncidentStatusV1, error)
	Roles(ctx context.Context) ([]Role, error)
	Role(ctx context.Context, id string) (Role, error)
}

type (
	// Action is a piece of work tracked during an incident.
	Action = incidentio.ActionV3
	// FollowUp is a piece of work tracked after an incident.
	FollowUp = incidentio.FollowUpV3
)

// WorkRepository stores the actions and follow-ups hanging off incidents.
//
// The two are near-identical CRUD resources upstream, so they share a port
// rather than each getting their own near-duplicate interface.
type WorkRepository interface {
	ListActions(ctx context.Context, incidentID string, p Page) (items []Action, after string, err error)
	GetAction(ctx context.Context, id string) (Action, error)
	CreateAction(ctx context.Context, in Action) (Action, error)
	UpdateAction(ctx context.Context, id string, mutate func(*Action) error) (Action, error)
	DeleteAction(ctx context.Context, id string) error

	ListFollowUps(ctx context.Context, incidentID string, p Page) (items []FollowUp, after string, err error)
	GetFollowUp(ctx context.Context, id string) (FollowUp, error)
	CreateFollowUp(ctx context.Context, in FollowUp) (FollowUp, error)
	UpdateFollowUp(ctx context.Context, id string, mutate func(*FollowUp) error) (FollowUp, error)
	DeleteFollowUp(ctx context.Context, id string) error
}
