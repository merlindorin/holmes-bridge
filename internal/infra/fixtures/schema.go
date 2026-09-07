// Package fixtures loads a simulated incident.io organisation from YAML.
//
// The fixture schema is deliberately not the wire schema. Writing a valid
// IncidentV2 by hand means filling in a dozen required fields that carry no
// meaning for a test; here an incident is a name, a status and a severity, and
// the loader expands that into the full wire object.
package fixtures

// Scenario is one YAML file: a complete organisation, ready to serve.
type Scenario struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	Identity Identity `yaml:"identity"`

	Severities []Severity `yaml:"severities"`
	Statuses   []Status   `yaml:"statuses"`
	Roles      []Role     `yaml:"roles"`
	Users      []User     `yaml:"users"`

	Catalog Catalog `yaml:"catalog"`

	Alerts    []Alert    `yaml:"alerts"`
	Incidents []Incident `yaml:"incidents"`
}

// Identity describes the API key the mock pretends to authenticate.
type Identity struct {
	Name         string   `yaml:"name"`
	DashboardURL string   `yaml:"dashboard_url"`
	Roles        []string `yaml:"roles"`
}

// Severity is a row of the severity reference table.
type Severity struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Rank        int64  `yaml:"rank"`
}

// Status is a row of the incident status reference table.
type Status struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Category is one of triage, declined, merged, live, learning, closed.
	Category string `yaml:"category"`
	Rank     int64  `yaml:"rank"`
}

// Role is a row of the incident role reference table.
type Role struct {
	ID           string `yaml:"id"`
	Name         string `yaml:"name"`
	Shortform    string `yaml:"shortform"`
	Description  string `yaml:"description"`
	Instructions string `yaml:"instructions"`
	// RoleType is one of lead, reporter, custom.
	RoleType string `yaml:"role_type"`
	Required bool   `yaml:"required"`
}

// User is a member of the simulated organisation.
type User struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Email       string `yaml:"email"`
	SlackUserID string `yaml:"slack_user_id"`
	// Role is one of viewer, responder, administrator, owner.
	Role string `yaml:"role"`
}

// Catalog holds the service/team registry HolmesGPT reads ownership from.
type Catalog struct {
	Types   []CatalogType  `yaml:"types"`
	Entries []CatalogEntry `yaml:"entries"`
}

// CatalogType is a catalog type definition, such as "Service".
type CatalogType struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	TypeName    string `yaml:"type_name"`
}

// CatalogEntry is one record of a catalog type.
type CatalogEntry struct {
	ID         string   `yaml:"id"`
	Type       string   `yaml:"type"`
	Name       string   `yaml:"name"`
	ExternalID string   `yaml:"external_id"`
	Aliases    []string `yaml:"aliases"`
	Rank       int32    `yaml:"rank"`
}

// Alert is an alert that fired against the simulated organisation.
type Alert struct {
	ID string `yaml:"id"`
	// SourceID is the alert source config the alert arrived on.
	SourceID string `yaml:"source_id"`
	Title    string `yaml:"title"`
	// Status is one of firing, resolved.
	Status           string `yaml:"status"`
	Description      string `yaml:"description"`
	DeduplicationKey string `yaml:"deduplication_key"`
	SourceURL        string `yaml:"source_url"`
	// CreatedAt is RFC3339. Relative forms like "-15m" are resolved against
	// load time, so a scenario stays fresh however long after it was written.
	CreatedAt string `yaml:"created_at"`
}

// Incident is an incident in the simulated organisation.
type Incident struct {
	ID        string `yaml:"id"`
	Reference string `yaml:"reference"`
	// ExternalID is the numeric counterpart of Reference (INC-42 -> 42).
	ExternalID int64  `yaml:"external_id"`
	Name       string `yaml:"name"`
	Summary    string `yaml:"summary"`
	// Status and Severity reference the reference-table IDs above.
	Status   string `yaml:"status"`
	Severity string `yaml:"severity"`
	// Mode is one of standard, retrospective, test, tutorial, stream.
	Mode string `yaml:"mode"`
	// Visibility is one of public, private.
	Visibility string `yaml:"visibility"`
	CreatedAt  string `yaml:"created_at"`
	UpdatedAt  string `yaml:"updated_at"`

	SlackChannelID   string `yaml:"slack_channel_id"`
	SlackChannelName string `yaml:"slack_channel_name"`
	Permalink        string `yaml:"permalink"`

	// Roles maps a role ID to the user ID holding it.
	Roles map[string]string `yaml:"roles"`
	// Alerts lists alert IDs attached to this incident.
	Alerts []string `yaml:"alerts"`

	Updates  []Update       `yaml:"updates"`
	Timeline []TimelineItem `yaml:"timeline"`
}

// Update is an entry in an incident's update feed.
type Update struct {
	ID        string `yaml:"id"`
	Message   string `yaml:"message"`
	Status    string `yaml:"status"`
	Severity  string `yaml:"severity"`
	CreatedAt string `yaml:"created_at"`
	// Updater is a user ID; empty means the API key acted.
	Updater string `yaml:"updater"`
}

// TimelineItem is a custom entry on an incident's timeline.
type TimelineItem struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Timestamp   string `yaml:"timestamp"`
	Creator     string `yaml:"creator"`
}
