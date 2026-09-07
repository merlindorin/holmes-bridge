package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/domain/alerts"
	"github.com/merlindorin/holmes-bridge/internal/domain/catalog"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/memory"
)

// Emitter queues a webhook for delivery. The mock fires one whenever state
// changes, so a bridge under test sees the same events production would send.
type Emitter interface {
	Emit(eventType webhooks.EventType, resource any) (string, error)
}

// Server implements the generated incident.io server interface.
type Server struct {
	logger    *zap.Logger
	store     *memory.Store
	incidents incidents.Repository
	work      incidents.WorkRepository
	reference *memory.CatalogueRepository
	alerts    alerts.Repository
	catalog   catalog.Repository
	emitter   Emitter

	// Idempotency keys are held here rather than in the store: they are an
	// API-level concern, and the real service expires them rather than keeping
	// them alongside the resource.
	mu             sync.Mutex
	idempotency    map[string]string
	externalIDSeed int64
}

var _ incidentio.ServerInterface = (*Server)(nil)

// NewServer wires the handlers to a store.
func NewServer(
	logger *zap.Logger,
	store *memory.Store,
	incidentRepo incidents.Repository,
	workRepo incidents.WorkRepository,
	reference *memory.CatalogueRepository,
	alertRepo alerts.Repository,
	catalogRepo catalog.Repository,
	emitter Emitter,
) *Server {
	return &Server{
		logger:      logger.Named("mock"),
		store:       store,
		incidents:   incidentRepo,
		work:        workRepo,
		reference:   reference,
		alerts:      alertRepo,
		catalog:     catalogRepo,
		emitter:     emitter,
		idempotency: map[string]string{},
	}
}

// emit queues a webhook, logging rather than failing the request when the
// queue is saturated: an API call should not 500 because a subscriber is down.
func (s *Server) emit(eventType webhooks.EventType, resource any) {
	if s.emitter == nil {
		return
	}

	if _, err := s.emitter.Emit(eventType, resource); err != nil {
		s.logger.Warn("failed to queue webhook",
			zap.String("event_type", string(eventType)), zap.Error(err))
	}
}

// actor is who the mock attributes changes to: the API key making the call.
func (s *Server) actor() incidentio.ActorV2 {
	return incidentio.ActorV2{
		ApiKey: &incidentio.APIKeyActorV2{Id: "01MOCKAPIKEY00000000000000", Name: "mock api key"},
	}
}

func (s *Server) byIdempotencyKey(key string) (incidents.Incident, bool) {
	s.mu.Lock()
	id, ok := s.idempotency[key]
	s.mu.Unlock()

	if !ok {
		return incidents.Incident{}, false
	}

	incident, err := s.incidents.Get(context.Background(), id)
	if err != nil {
		return incidents.Incident{}, false
	}

	return incident, true
}

func (s *Server) rememberIdempotencyKey(key, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.idempotency[key] = id
}

// nextExternalID hands out the human-facing incident number behind INC-nnn.
func (s *Server) nextExternalID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.externalIDSeed == 0 {
		// Start above whatever the loaded fixtures already used, so a created
		// incident never collides with a seeded reference.
		s.externalIDSeed = int64(s.store.Counts()["incidents"]) + 1000
	}

	s.externalIDSeed++

	return s.externalIDSeed
}

// resolveStatus picks the status a new incident starts in: the requested one,
// or the lowest-ranked triage status when the caller did not say.
func (s *Server) resolveStatus(ctx context.Context, id *string) (incidentio.IncidentStatusV2, error) {
	if id != nil {
		status, err := s.reference.Status(ctx, *id)
		if err != nil {
			return incidentio.IncidentStatusV2{}, api.Validation("incident_status_id", "/incident_status_id",
				fmt.Sprintf("No incident status with ID %q exists.", *id))
		}

		return statusV2(status), nil
	}

	statuses, err := s.reference.Statuses(ctx)
	if err != nil || len(statuses) == 0 {
		return incidentio.IncidentStatusV2{}, api.Internal("this organisation has no incident statuses configured")
	}

	first := statuses[0]
	for _, candidate := range statuses {
		if candidate.Rank < first.Rank {
			first = candidate
		}
	}

	return statusV2(first), nil
}

func (s *Server) resolveRoleAssignments(
	ctx context.Context, payloads []incidentio.IncidentRoleAssignmentPayloadV2,
) ([]incidentio.IncidentRoleAssignmentV2, error) {
	out := make([]incidentio.IncidentRoleAssignmentV2, 0, len(payloads))

	for _, p := range payloads {
		role, err := s.reference.Role(ctx, p.IncidentRoleId)
		if err != nil {
			return nil, api.Validation("incident_role_id", "/incident_role_assignments",
				fmt.Sprintf("No incident role with ID %q exists.", p.IncidentRoleId))
		}

		assignment := incidentio.IncidentRoleAssignmentV2{Role: embeddedRole(role)}

		if p.Assignee != nil && p.Assignee.Id != nil {
			for _, u := range s.reference.Users() {
				if u.Id == *p.Assignee.Id {
					user := u
					assignment.Assignee = &user
				}
			}
		}

		out = append(out, assignment)
	}

	return out, nil
}

// notFound renders a 404 for an expected miss, and a 500 for anything else, so
// a storage fault is never disguised as a missing record.
func (s *Server) notFound(c *gin.Context, resource, id string, err, sentinel error) {
	if errors.Is(err, sentinel) {
		_ = c.Error(api.NotFound(resource, id))
		return
	}

	s.logger.Error("unexpected repository error",
		zap.String("resource", resource), zap.String("id", id), zap.Error(err))
	_ = c.Error(api.Internal(fmt.Sprintf("failed to load %s", resource)))
}

// bindJSON decodes a request body, reporting a malformed one as a 400 and
// telling the caller whether to continue.
func bindJSON(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		_ = c.Error(api.BadRequest(fmt.Sprintf("Request body is not valid JSON for this endpoint: %s", err)))
		return false
	}

	return true
}

func statusV2(s incidentio.IncidentStatusV1) incidentio.IncidentStatusV2 {
	return incidentio.IncidentStatusV2{
		Id: s.Id, Name: s.Name, Description: s.Description,
		Category:  incidentio.IncidentStatusV2Category(s.Category),
		Rank:      s.Rank,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func embeddedRole(r incidentio.IncidentRoleV2) incidentio.EmbeddedIncidentRoleV2 {
	return incidentio.EmbeddedIncidentRoleV2{
		Id: r.Id, Name: r.Name, Shortform: r.Shortform, Description: r.Description,
		Instructions: r.Instructions,
		RoleType:     incidentio.EmbeddedIncidentRoleV2RoleType(r.RoleType),
		CreatedAt:    r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}

	return *p
}

func valueOrEnum[T ~string](p *T, fallback string) string {
	if p == nil {
		return fallback
	}

	return string(*p)
}

// UtilitiesV1Identity reports which key the caller is using, and is what most
// clients hit first to check their configuration.
func (s *Server) UtilitiesV1Identity(c *gin.Context) {
	c.JSON(http.StatusOK, incidentio.UtilitiesIdentityResultV1{Identity: s.store.Identity()})
}
