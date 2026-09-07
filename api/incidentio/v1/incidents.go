package v1

import (
	"errors"
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

func (s *Server) IncidentsV2List(c *gin.Context, params incidentio.IncidentsV2ListParams) {
	p := page(params.After, params.PageSize)

	filter := incidents.Filter{
		Status:     oneOf(params.Status),
		Severity:   oneOf(params.Severity),
		Mode:       oneOf(params.Mode),
		UpdatedGTE: timeBound(params.UpdatedAt, opGTE),
		UpdatedLTE: timeBound(params.UpdatedAt, opLTE),
	}

	items, after, total, err := s.incidents.List(c.Request.Context(), filter, p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list incidents"))
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentsListResultV2{
		Incidents:      items,
		PaginationMeta: metaWithTotal(p, after, total),
	})
}

func (s *Server) IncidentsV2Show(c *gin.Context, id string) {
	incident, err := s.incidents.Get(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "incident", id, err, incidents.ErrNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentsShowResultV2{Incident: incident})
}

func (s *Server) IncidentsV2Create(c *gin.Context) {
	var payload incidentio.IncidentsCreatePayloadV2
	if !bindJSON(c, &payload) {
		return
	}

	if payload.IdempotencyKey == "" {
		_ = c.Error(api.Validation("idempotency_key", "/idempotency_key",
			"An idempotency key is required so a retried create does not open a second incident."))

		return
	}

	ctx := c.Request.Context()

	// Replaying the same idempotency key must return the original incident
	// rather than declaring another one; a retried POST is the normal case
	// when a caller times out, not an error.
	if existing, ok := s.byIdempotencyKey(payload.IdempotencyKey); ok {
		c.JSON(http.StatusOK, incidentio.IncidentsCreateResultV2{Incident: existing})
		return
	}

	status, err := s.resolveStatus(ctx, payload.IncidentStatusId)
	if err != nil {
		_ = c.Error(err)
		return
	}

	now := time.Now().UTC()
	external := s.nextExternalID()

	incident := incidents.Incident{
		Id:                      fixtures.NewID(),
		Reference:               fmt.Sprintf("%s%d", fixtures.ReferencePrefix, external),
		Name:                    valueOr(payload.Name, "Untitled incident"),
		Summary:                 payload.Summary,
		IncidentStatus:          status,
		Mode:                    incidentio.IncidentV2Mode(valueOrEnum(payload.Mode, "standard")),
		Visibility:              incidentio.IncidentV2Visibility(payload.Visibility),
		Creator:                 s.actor(),
		CustomFieldEntries:      []incidentio.CustomFieldEntryV2{},
		IncidentRoleAssignments: []incidentio.IncidentRoleAssignmentV2{},
		TeamIds:                 []string{},
		SlackTeamId:             valueOr(payload.SlackTeamId, "T00MOCKTEAM"),
		SlackChannelId:          "C" + fixtures.NewID()[:10],
		CreatedAt:               now,
		UpdatedAt:               now,
		LastActivityAt:          now,
	}

	if payload.SeverityId != nil {
		severity, sevErr := s.reference.Severity(ctx, *payload.SeverityId)
		if sevErr != nil {
			_ = c.Error(api.Validation("severity_id", "/severity_id",
				fmt.Sprintf("No severity with ID %q exists.", *payload.SeverityId)))

			return
		}

		incident.Severity = &severity
	}

	if payload.IncidentRoleAssignments != nil {
		assignments, assignErr := s.resolveRoleAssignments(ctx, *payload.IncidentRoleAssignments)
		if assignErr != nil {
			_ = c.Error(assignErr)
			return
		}

		incident.IncidentRoleAssignments = assignments
	}

	created, err := s.incidents.Create(ctx, incident)
	if err != nil {
		_ = c.Error(api.Internal("failed to create the incident"))
		return
	}

	s.rememberIdempotencyKey(payload.IdempotencyKey, created.Id)
	s.emit(webhooks.PublicIncidentIncidentCreatedV2, created)

	c.JSON(http.StatusCreated, incidentio.IncidentsCreateResultV2{Incident: created})
}

func (s *Server) IncidentsV2Edit(c *gin.Context, id string) {
	var payload incidentio.IncidentsEditPayloadV2
	if !bindJSON(c, &payload) {
		return
	}

	ctx := c.Request.Context()
	edit := payload.Incident

	// Captured so the right event can be chosen after the mutation: a status
	// change is a different webhook from a plain edit, and carries the
	// transition it represents.
	statusChanged := false
	previousStatus := incidentio.IncidentStatusV2{}

	updated, err := s.incidents.Update(ctx, id, func(in *incidents.Incident) error {
		if edit.Name != nil {
			in.Name = *edit.Name
		}

		if edit.Summary != nil {
			in.Summary = edit.Summary
		}

		if edit.CallUrl != nil {
			in.CallUrl = edit.CallUrl
		}

		if edit.IncidentStatusId != nil {
			status, statusErr := s.reference.Status(ctx, *edit.IncidentStatusId)
			if statusErr != nil {
				return api.Validation("incident_status_id", "/incident/incident_status_id",
					fmt.Sprintf("No incident status with ID %q exists.", *edit.IncidentStatusId))
			}

			statusChanged = in.IncidentStatus.Id != status.Id
			previousStatus = in.IncidentStatus
			in.IncidentStatus = statusV2(status)
		}

		if edit.SeverityId != nil {
			severity, sevErr := s.reference.Severity(ctx, *edit.SeverityId)
			if sevErr != nil {
				return api.Validation("severity_id", "/incident/severity_id",
					fmt.Sprintf("No severity with ID %q exists.", *edit.SeverityId))
			}

			in.Severity = &severity
		}

		if edit.IncidentRoleAssignments != nil {
			assignments, assignErr := s.resolveRoleAssignments(ctx, *edit.IncidentRoleAssignments)
			if assignErr != nil {
				return assignErr
			}

			in.IncidentRoleAssignments = assignments
		}

		return nil
	})

	if err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) {
			_ = c.Error(apiErr)
			return
		}

		s.notFound(c, "incident", id, err, incidents.ErrNotFound)

		return
	}

	// A status change carries a different payload from a plain edit: the
	// incident is nested and accompanied by the transition. Emitting a flat
	// incident here would only be readable by a client written against this
	// mock rather than against incident.io.
	if statusChanged {
		s.emit(webhooks.PublicIncidentIncidentStatusUpdatedV2, webhooks.IncidentWithStatusChange{
			Incident:       updated,
			PreviousStatus: previousStatus,
			NewStatus:      updated.IncidentStatus,
		})
	} else {
		s.emit(webhooks.PublicIncidentIncidentUpdatedV2, updated)
	}

	c.JSON(http.StatusOK, incidentio.IncidentsEditResultV2{Incident: updated})
}

func (s *Server) IncidentUpdatesV2List(c *gin.Context, params incidentio.IncidentUpdatesV2ListParams) {
	p := page(params.After, params.PageSize)

	items, after, err := s.incidents.ListUpdates(c.Request.Context(), valueOr(params.IncidentId, ""), p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list incident updates"))
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentUpdatesListResultV2{
		IncidentUpdates: items,
		PaginationMeta:  ptr(metaV2(p, after)),
	})
}

func (s *Server) IncidentUpdatesV2Create(c *gin.Context) {
	var payload incidentio.IncidentUpdatesCreatePayloadV2
	if !bindJSON(c, &payload) {
		return
	}

	ctx := c.Request.Context()

	incident, err := s.incidents.Get(ctx, payload.IncidentId)
	if err != nil {
		_ = c.Error(api.Validation("incident_id", "/incident_id",
			fmt.Sprintf("No incident with ID %q exists.", payload.IncidentId)))

		return
	}

	// An update with no explicit target carries the incident's current status,
	// which is what the real API records for a comment-only update.
	newStatus := incident.IncidentStatus

	if payload.ToIncidentStatusId != nil {
		status, statusErr := s.reference.Status(ctx, *payload.ToIncidentStatusId)
		if statusErr != nil {
			_ = c.Error(api.Validation("to_incident_status_id", "/to_incident_status_id",
				fmt.Sprintf("No incident status with ID %q exists.", *payload.ToIncidentStatusId)))

			return
		}

		newStatus = statusV2(status)
	}

	update := incidents.Update{
		Id:                fixtures.NewID(),
		IncidentId:        payload.IncidentId,
		Message:           payload.Message,
		NewIncidentStatus: newStatus,
		Updater:           s.actor(),
		CreatedAt:         time.Now().UTC(),
	}

	if payload.ToSeverityId != nil {
		severity, sevErr := s.reference.Severity(ctx, *payload.ToSeverityId)
		if sevErr != nil {
			_ = c.Error(api.Validation("to_severity_id", "/to_severity_id",
				fmt.Sprintf("No severity with ID %q exists.", *payload.ToSeverityId)))

			return
		}

		update.NewSeverity = &severity
	}

	created, err := s.incidents.CreateUpdate(ctx, update)
	if err != nil {
		_ = c.Error(api.Internal("failed to create the incident update"))
		return
	}

	// Posting an update moves the incident too, so a client that polls the
	// incident rather than the feed still sees the change.
	//
	// The event depends on whether anything actually moved: a comment-only
	// update is not a status change, and emitting one as though it were makes
	// every consumer that triggers on status changes fire on plain comments.
	statusMoved := incident.IncidentStatus.Id != created.NewIncidentStatus.Id

	moved, err := s.incidents.Update(ctx, payload.IncidentId, func(in *incidents.Incident) error {
		in.IncidentStatus = created.NewIncidentStatus
		if created.NewSeverity != nil {
			in.Severity = created.NewSeverity
		}

		return nil
	})

	if err == nil {
		if statusMoved {
			s.emit(webhooks.PublicIncidentIncidentStatusUpdatedV2, webhooks.IncidentWithStatusChange{
				Incident:       moved,
				PreviousStatus: incident.IncidentStatus,
				NewStatus:      moved.IncidentStatus,
				Message:        created.Message,
			})
		} else {
			s.emit(webhooks.PublicIncidentIncidentUpdatedV2, moved)
		}
	}

	c.JSON(http.StatusCreated, incidentio.IncidentUpdatesCreateResultV2{IncidentUpdate: created})
}

func (s *Server) IncidentTimelineItemsV2List(c *gin.Context, params incidentio.IncidentTimelineItemsV2ListParams) {
	p := page(params.After, params.PageSize)

	items, after, err := s.incidents.ListTimelineItems(c.Request.Context(), params.IncidentId, p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list timeline items"))
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentTimelineItemsListResultV2{
		IncidentTimelineItems: items,
		PaginationMeta:        ptr(metaV2(p, after)),
	})
}

func (s *Server) IncidentTimelineItemsV2Create(c *gin.Context) {
	var payload incidentio.IncidentTimelineItemsCreatePayloadV2
	if !bindJSON(c, &payload) {
		return
	}

	ctx := c.Request.Context()

	if _, err := s.incidents.Get(ctx, payload.IncidentId); err != nil {
		_ = c.Error(api.Validation("incident_id", "/incident_id",
			fmt.Sprintf("No incident with ID %q exists.", payload.IncidentId)))

		return
	}

	now := time.Now().UTC()

	created, err := s.incidents.CreateTimelineItem(ctx, incidents.TimelineItem{
		Id:          fixtures.NewID(),
		IncidentId:  payload.IncidentId,
		Title:       payload.Title,
		Description: payload.Description,
		Timestamp:   payload.Timestamp,
		Creator:     s.actor(),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		_ = c.Error(api.Internal("failed to create the timeline item"))
		return
	}

	c.JSON(http.StatusCreated, incidentio.IncidentTimelineItemsCreateResultV2{IncidentTimelineItem: created})
}

func (s *Server) IncidentTimelineItemsV2Update(c *gin.Context, id string) {
	var payload incidentio.IncidentTimelineItemsUpdatePayloadV2
	if !bindJSON(c, &payload) {
		return
	}

	updated, err := s.incidents.UpdateTimelineItem(c.Request.Context(), id,
		func(item *incidents.TimelineItem) error {
			if payload.Title != nil {
				item.Title = *payload.Title
			}

			if payload.Description != nil {
				item.Description = payload.Description
			}

			if payload.Timestamp != nil {
				item.Timestamp = *payload.Timestamp
			}

			item.UpdatedAt = time.Now().UTC()

			return nil
		})
	if err != nil {
		s.notFound(c, "incident timeline item", id, err, incidents.ErrTimelineItemNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.IncidentTimelineItemsUpdateResultV2{IncidentTimelineItem: updated})
}
