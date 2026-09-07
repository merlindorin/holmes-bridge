package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// The reference tables are fixture data: read-only, small, and unpaginated
// upstream, so these handlers are deliberately plain.

func (s *Server) SeveritiesV1List(c *gin.Context) {
	items, err := s.reference.Severities(c.Request.Context())
	if err != nil {
		_ = c.Error(api.Internal("failed to list severities"))
		return
	}

	out := make([]incidentio.SeverityV1, 0, len(items))
	for _, sev := range items {
		out = append(out, severityV1(sev))
	}

	c.JSON(http.StatusOK, incidentio.SeveritiesListResultV1{Severities: out})
}

func (s *Server) SeveritiesV1Show(c *gin.Context, id string) {
	severity, err := s.reference.Severity(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "severity", id, err, incidents.ErrSeverityNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.SeveritiesShowResultV1{Severity: severityV1(severity)})
}

func (s *Server) IncidentStatusesV1List(c *gin.Context) {
	items, err := s.reference.Statuses(c.Request.Context())
	if err != nil {
		_ = c.Error(api.Internal("failed to list incident statuses"))
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentStatusesListResultV1{IncidentStatuses: items})
}

func (s *Server) IncidentStatusesV1Show(c *gin.Context, id string) {
	status, err := s.reference.Status(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "incident status", id, err, incidents.ErrStatusNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentStatusesShowResultV1{IncidentStatus: status})
}

func (s *Server) IncidentRolesV2List(c *gin.Context) {
	items, err := s.reference.Roles(c.Request.Context())
	if err != nil {
		_ = c.Error(api.Internal("failed to list incident roles"))
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentRolesListResultV2{IncidentRoles: items})
}

func (s *Server) IncidentRolesV2Show(c *gin.Context, id string) {
	role, err := s.reference.Role(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "incident role", id, err, incidents.ErrRoleNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentRolesShowResultV2{IncidentRole: role})
}

func (s *Server) UsersV2List(c *gin.Context, params incidentio.UsersV2ListParams) {
	all := s.reference.Users()

	matched := make([]incidentio.UserWithRolesV2, 0, len(all))

	for _, u := range all {
		if params.Email != nil && (u.Email == nil || *u.Email != *params.Email) {
			continue
		}

		if params.SlackUserId != nil && (u.SlackUserId == nil || *u.SlackUserId != *params.SlackUserId) {
			continue
		}

		matched = append(matched, userWithRoles(u))
	}

	p := page(params.After, params.PageSize)

	c.JSON(http.StatusOK, incidentio.UsersListResultV2{
		Users:          matched,
		PaginationMeta: metaV2(p, ""),
	})
}

func (s *Server) UsersV2Show(c *gin.Context, id string) {
	for _, u := range s.reference.Users() {
		if u.Id == id {
			c.JSON(http.StatusOK, incidentio.UsersShowResultV2{User: userWithRoles(u)})
			return
		}
	}

	_ = c.Error(api.NotFound("user", id))
}
