package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
)

// statusCompleted is the terminal status shared by actions and follow-ups.
const statusCompleted = "completed"

// Actions and follow-ups are where a HolmesGPT investigation lands its
// conclusions: "restart the pod" is an action, "add an alert for pool
// saturation" is a follow-up.

func (s *Server) ActionsV3List(c *gin.Context, params incidentio.ActionsV3ListParams) {
	p := page(params.After, params.PageSize)

	items, after, err := s.work.ListActions(c.Request.Context(), valueOr(params.IncidentId, ""), p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list actions"))
		return
	}

	c.JSON(http.StatusOK, incidentio.ActionsListResultV3{
		Actions:        items,
		PaginationMeta: metaV3(p, after),
	})
}

func (s *Server) ActionsV3Show(c *gin.Context, id string) {
	action, err := s.work.GetAction(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "action", id, err, incidents.ErrActionNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.ActionsShowResultV3{Action: action})
}

func (s *Server) ActionsV3Create(c *gin.Context) {
	var payload incidentio.ActionsCreatePayloadV3
	if !bindJSON(c, &payload) {
		return
	}

	ctx := c.Request.Context()

	if err := s.requireIncident(ctx, payload.IncidentId); err != nil {
		_ = c.Error(err)
		return
	}

	now := time.Now().UTC()

	created, err := s.work.CreateAction(ctx, incidents.Action{
		Id:          fixtures.NewID(),
		IncidentId:  payload.IncidentId,
		Description: payload.Description,
		Status:      "outstanding",
		Assignee:    s.userByID(payload.AssigneeId),
		Creator:     s.actor(),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		_ = c.Error(api.Internal("failed to create the action"))
		return
	}

	s.emit(webhooks.PublicIncidentActionCreatedV1, created)

	c.JSON(http.StatusCreated, incidentio.ActionsCreateResultV3{Action: created})
}

func (s *Server) ActionsV3Update(c *gin.Context, id string) {
	var payload incidentio.ActionsUpdatePayloadV3
	if !bindJSON(c, &payload) {
		return
	}

	updated, err := s.work.UpdateAction(c.Request.Context(), id, func(a *incidents.Action) error {
		a.Description = payload.Description
		a.Status = incidentio.ActionV3Status(payload.Status)
		a.Assignee = s.userByID(payload.AssigneeId)
		a.UpdatedAt = time.Now().UTC()

		// completed_at is derived from the status rather than sent by the
		// caller, so it cannot disagree with it.
		if a.Status == statusCompleted && a.CompletedAt == nil {
			a.CompletedAt = ptr(a.UpdatedAt)
		}

		if a.Status != statusCompleted {
			a.CompletedAt = nil
		}

		return nil
	})
	if err != nil {
		s.notFound(c, "action", id, err, incidents.ErrActionNotFound)
		return
	}

	s.emit(webhooks.PublicIncidentActionUpdatedV1, updated)

	c.JSON(http.StatusOK, incidentio.ActionsUpdateResultV3{Action: updated})
}

func (s *Server) ActionsV3Delete(c *gin.Context, id string) {
	if err := s.work.DeleteAction(c.Request.Context(), id); err != nil {
		s.notFound(c, "action", id, err, incidents.ErrActionNotFound)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) FollowUpsV3List(c *gin.Context, params incidentio.FollowUpsV3ListParams) {
	p := page(params.After, params.PageSize)

	items, after, err := s.work.ListFollowUps(c.Request.Context(), valueOr(params.IncidentId, ""), p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list follow-ups"))
		return
	}

	c.JSON(http.StatusOK, incidentio.FollowUpsListResultV3{
		FollowUps:      items,
		PaginationMeta: metaV3(p, after),
	})
}

func (s *Server) FollowUpsV3Show(c *gin.Context, id string) {
	followUp, err := s.work.GetFollowUp(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "follow-up", id, err, incidents.ErrFollowUpNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.FollowUpsShowResultV3{FollowUp: followUp})
}

func (s *Server) FollowUpsV3Create(c *gin.Context) {
	var payload incidentio.FollowUpsCreatePayloadV3
	if !bindJSON(c, &payload) {
		return
	}

	ctx := c.Request.Context()

	if err := s.requireIncident(ctx, payload.IncidentId); err != nil {
		_ = c.Error(err)
		return
	}

	now := time.Now().UTC()

	followUp := incidents.FollowUp{
		Id:          fixtures.NewID(),
		IncidentId:  payload.IncidentId,
		Title:       payload.Title,
		Description: payload.Description,
		Status:      "outstanding",
		Labels:      []string{},
		Assignee:    s.userByID(payload.AssigneeId),
		Creator:     s.actor(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if payload.Labels != nil {
		followUp.Labels = *payload.Labels
	}

	created, err := s.work.CreateFollowUp(ctx, followUp)
	if err != nil {
		_ = c.Error(api.Internal("failed to create the follow-up"))
		return
	}

	s.emit(webhooks.PublicIncidentFollowUpCreatedV2, created)

	c.JSON(http.StatusCreated, incidentio.FollowUpsCreateResultV3{FollowUp: created})
}

func (s *Server) FollowUpsV3Update(c *gin.Context, id string) {
	var payload incidentio.FollowUpsUpdatePayloadV3
	if !bindJSON(c, &payload) {
		return
	}

	updated, err := s.work.UpdateFollowUp(c.Request.Context(), id, func(f *incidents.FollowUp) error {
		f.Title = payload.Title
		f.Description = payload.Description
		f.Status = incidentio.FollowUpV3Status(payload.Status)
		f.Assignee = s.userByID(payload.AssigneeId)
		f.UpdatedAt = time.Now().UTC()

		if payload.Labels != nil {
			f.Labels = *payload.Labels
		}

		if f.Status == statusCompleted && f.CompletedAt == nil {
			f.CompletedAt = ptr(f.UpdatedAt)
		}

		if f.Status != statusCompleted {
			f.CompletedAt = nil
		}

		return nil
	})
	if err != nil {
		s.notFound(c, "follow-up", id, err, incidents.ErrFollowUpNotFound)
		return
	}

	s.emit(webhooks.PublicIncidentFollowUpUpdatedV2, updated)

	c.JSON(http.StatusOK, incidentio.FollowUpsUpdateResultV3{FollowUp: updated})
}

func (s *Server) FollowUpsV3Delete(c *gin.Context, id string) {
	if err := s.work.DeleteFollowUp(c.Request.Context(), id); err != nil {
		s.notFound(c, "follow-up", id, err, incidents.ErrFollowUpNotFound)
		return
	}

	c.Status(http.StatusNoContent)
}

// requireIncident rejects work attached to an incident that does not exist,
// which is otherwise an easy way to create orphans a client can never find.
func (s *Server) requireIncident(ctx context.Context, id string) error {
	if _, err := s.incidents.Get(ctx, id); err != nil {
		return api.Validation("incident_id", "/incident_id",
			fmt.Sprintf("No incident with ID %q exists.", id))
	}

	return nil
}

func (s *Server) userByID(id *string) *incidentio.UserV2 {
	if id == nil {
		return nil
	}

	for _, u := range s.reference.Users() {
		if u.Id == *id {
			user := u
			return &user
		}
	}

	return nil
}
