// Package incidentio wraps the generated incident.io client with the things
// every call needs: authentication, error mapping, and the small number of
// operations the bridge actually performs.
//
// The bridge cannot tell whether it is talking to the real incident.io or the
// mock in this repo, which is the point.
package incidentio

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
)

// Client performs the incident.io operations the bridge needs.
type Client struct {
	api *incidentio.ClientWithResponses
}

// Option configures a Client.
type Option func(*config)

type config struct {
	timeout   time.Duration
	userAgent string
	transport http.RoundTripper
}

// WithTimeout bounds each request. It defaults to 30s.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *config) { c.userAgent = ua }
}

// WithTransport swaps the HTTP transport, for tests.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *config) { c.transport = rt }
}

// New builds a client against baseURL, authenticating with apiKey.
func New(baseURL, apiKey string, opts ...Option) (*Client, error) {
	cfg := config{timeout: 30 * time.Second, userAgent: "holmes-bridge/1.0"}
	for _, opt := range opts {
		opt(&cfg)
	}

	httpClient := &http.Client{Timeout: cfg.timeout, Transport: cfg.transport}

	api, err := incidentio.NewClientWithResponses(
		strings.TrimSuffix(baseURL, "/"),
		incidentio.WithHTTPClient(httpClient),
		incidentio.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			if apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}

			req.Header.Set("User-Agent", cfg.userAgent)

			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build the incident.io client: %w", err)
	}

	return &Client{api: api}, nil
}

// Identity verifies the API key, and is what the bridge calls at boot so a bad
// key is reported on startup rather than on the first real incident.
func (c *Client) Identity(ctx context.Context) (incidentio.IdentityV1, error) {
	resp, err := c.api.UtilitiesV1IdentityWithResponse(ctx)
	if err != nil {
		return incidentio.IdentityV1{}, fmt.Errorf("failed to check identity: %w", err)
	}

	if resp.JSON200 == nil {
		return incidentio.IdentityV1{}, statusError("GET /v1/identity", resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.Identity, nil
}

// Incident fetches the full incident record.
func (c *Client) Incident(ctx context.Context, id string) (incidentio.IncidentV2, error) {
	resp, err := c.api.IncidentsV2ShowWithResponse(ctx, id)
	if err != nil {
		return incidentio.IncidentV2{}, fmt.Errorf("failed to fetch incident %s: %w", id, err)
	}

	if resp.JSON200 == nil {
		return incidentio.IncidentV2{}, statusError("GET /v2/incidents/"+id, resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.Incident, nil
}

// Incidents lists the most recent incidents.
//
// The bridge itself never lists — it is told which incident to look at — but
// the CLI needs this to answer "what is open right now" without a hand-rolled
// curl.
func (c *Client) Incidents(ctx context.Context, limit int) ([]incidentio.IncidentV2, error) {
	if limit <= 0 || limit > maxPageSize {
		limit = maxPageSize
	}

	size := int64(limit)

	resp, err := c.api.IncidentsV2ListWithResponse(ctx, &incidentio.IncidentsV2ListParams{
		PageSize: &size,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list incidents: %w", err)
	}

	if resp.JSON200 == nil {
		return nil, statusError("GET /v2/incidents", resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.Incidents, nil
}

// Alert fetches a single alert.
func (c *Client) Alert(ctx context.Context, id string) (incidentio.AlertV2, error) {
	resp, err := c.api.AlertsV2ShowWithResponse(ctx, id)
	if err != nil {
		return incidentio.AlertV2{}, fmt.Errorf("failed to fetch alert %s: %w", id, err)
	}

	if resp.JSON200 == nil {
		return incidentio.AlertV2{}, statusError("GET /v2/alerts/"+id, resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.Alert, nil
}

// IncidentAlerts lists the alerts attached to an incident, which is where the
// concrete symptoms live: the incident itself often says only "something is
// wrong with checkout".
func (c *Client) IncidentAlerts(ctx context.Context, incidentID string) ([]incidentio.IncidentAlertV2, error) {
	resp, err := c.api.AlertsV2ListIncidentAlertsWithResponse(ctx,
		&incidentio.AlertsV2ListIncidentAlertsParams{
			IncidentId: &incidentID,
			PageSize:   int64(maxPageSize),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list alerts for incident %s: %w", incidentID, err)
	}

	if resp.JSON200 == nil {
		return nil, statusError("GET /v2/incident_alerts", resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.IncidentAlerts, nil
}

// IncidentUpdates lists an incident's update feed, so an investigation can see
// what responders have already established and not repeat it.
func (c *Client) IncidentUpdates(ctx context.Context, incidentID string) ([]incidentio.IncidentUpdateV2, error) {
	size := int64(maxPageSize)

	resp, err := c.api.IncidentUpdatesV2ListWithResponse(ctx,
		&incidentio.IncidentUpdatesV2ListParams{IncidentId: &incidentID, PageSize: &size})
	if err != nil {
		return nil, fmt.Errorf("failed to list updates for incident %s: %w", incidentID, err)
	}

	if resp.JSON200 == nil {
		return nil, statusError("GET /v2/incident_updates", resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.IncidentUpdates, nil
}

// PostUpdate writes a message to the incident's update feed. This is the
// bridge's primary way of returning an analysis: it lands in the Slack channel
// as well as the incident record.
func (c *Client) PostUpdate(
	ctx context.Context, incidentID, message, idempotencyKey string,
) (incidentio.IncidentUpdateV2, error) {
	resp, err := c.api.IncidentUpdatesV2CreateWithResponse(ctx,
		incidentio.IncidentUpdatesV2CreateJSONRequestBody{
			IncidentId:     incidentID,
			Message:        &message,
			IdempotencyKey: idempotencyKey,
		})
	if err != nil {
		return incidentio.IncidentUpdateV2{}, fmt.Errorf("failed to post an update to %s: %w", incidentID, err)
	}

	if resp.JSON201 == nil {
		return incidentio.IncidentUpdateV2{},
			statusError("POST /v2/incident_updates", resp.StatusCode(), resp.Body)
	}

	return resp.JSON201.IncidentUpdate, nil
}

// AddTimelineItem pins a dated entry onto the incident timeline, which is where
// an investigation belongs when it establishes *when* something happened.
func (c *Client) AddTimelineItem(
	ctx context.Context, incidentID, title, description, idempotencyKey string, at time.Time,
) (incidentio.IncidentTimelineItemV2, error) {
	resp, err := c.api.IncidentTimelineItemsV2CreateWithResponse(ctx,
		incidentio.IncidentTimelineItemsV2CreateJSONRequestBody{
			IncidentId:     incidentID,
			Title:          title,
			Description:    &description,
			Timestamp:      at,
			IdempotencyKey: idempotencyKey,
		})
	if err != nil {
		return incidentio.IncidentTimelineItemV2{},
			fmt.Errorf("failed to add a timeline item to %s: %w", incidentID, err)
	}

	if resp.JSON201 == nil {
		return incidentio.IncidentTimelineItemV2{},
			statusError("POST /v2/incident_timeline_items", resp.StatusCode(), resp.Body)
	}

	return resp.JSON201.IncidentTimelineItem, nil
}

// CreateFollowUp records a piece of work the investigation recommends.
func (c *Client) CreateFollowUp(
	ctx context.Context, incidentID, title, description string,
) (incidentio.FollowUpV3, error) {
	resp, err := c.api.FollowUpsV3CreateWithResponse(ctx,
		incidentio.FollowUpsV3CreateJSONRequestBody{
			IncidentId:  incidentID,
			Title:       title,
			Description: &description,
		})
	if err != nil {
		return incidentio.FollowUpV3{}, fmt.Errorf("failed to create a follow-up on %s: %w", incidentID, err)
	}

	if resp.JSON201 == nil {
		return incidentio.FollowUpV3{}, statusError("POST /v3/follow_ups", resp.StatusCode(), resp.Body)
	}

	return resp.JSON201.FollowUp, nil
}

// CatalogEntries lists the entries of a catalog type, which is how the bridge
// resolves a service name to its owning team.
func (c *Client) CatalogEntries(ctx context.Context, catalogTypeID string) ([]incidentio.CatalogEntryV2, error) {
	size := int64(maxPageSize)

	// Catalog V2 is marked deprecated upstream in favour of V3, but V2 is what
	// the mock implements and what most orgs still serve. Revisit when the
	// trimmed spec moves to V3.
	//nolint:staticcheck // deliberate: V2 is the version this bridge targets
	resp, err := c.api.CatalogV2ListEntriesWithResponse(ctx,
		&incidentio.CatalogV2ListEntriesParams{CatalogTypeId: catalogTypeID, PageSize: &size})
	if err != nil {
		return nil, fmt.Errorf("failed to list catalog entries for %s: %w", catalogTypeID, err)
	}

	if resp.JSON200 == nil {
		return nil, statusError("GET /v2/catalog_entries", resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.CatalogEntries, nil
}

// maxPageSize is incident.io's ceiling; the bridge always asks for one large
// page rather than walking cursors, because the collections it reads are bounded
// by a single incident.
const maxPageSize = 250
