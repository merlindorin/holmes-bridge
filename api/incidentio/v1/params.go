// Package v1 implements the incident.io mock's HTTP handlers on top of the
// OpenAPI-generated server interface.
package v1

import (
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// incident.io expresses list filters as field[operator]=value, which arrives
// here as a map of operator to values. The mock implements the operators the
// bridge actually sends; anything else is ignored rather than rejected, so an
// unsupported filter widens the result set instead of failing the call.
const (
	opOneOf    = "one_of"
	opIsOneOf  = "is_one_of"
	opCatOneOf = "category_one_of"
	opGTE      = "gte"
	opLTE      = "lte"
)

// oneOf pulls the values of an inclusive membership filter.
func oneOf(filter *map[string][]string) []string {
	if filter == nil {
		return nil
	}

	for _, op := range []string{opOneOf, opIsOneOf, opCatOneOf} {
		if values, ok := (*filter)[op]; ok && len(values) > 0 {
			return values
		}
	}

	return nil
}

// timeBound pulls a gte/lte bound out of a timestamp filter.
func timeBound(filter *map[string][]string, op string) time.Time {
	if filter == nil {
		return time.Time{}
	}

	values, ok := (*filter)[op]
	if !ok || len(values) == 0 {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339, values[0])
	if err != nil {
		return time.Time{}
	}

	return t.UTC()
}

// page builds a pagination request from the optional query parameters every
// list endpoint shares.
func page(after *string, pageSize *int64) incidents.Page {
	p := incidents.Page{}

	if after != nil {
		p.After = *after
	}

	if pageSize != nil {
		p.PageSize = int(*pageSize)
	}

	return p.Normalise()
}

// pageOf is the variant for endpoints whose page_size is required rather than
// optional.
func pageOf(after *string, pageSize int64) incidents.Page {
	return page(after, &pageSize)
}

// metaV2 renders the pagination block for a v2 list response.
func metaV2(p incidents.Page, after string) incidentio.PaginationMetaResultV2 {
	m := incidentio.PaginationMetaResultV2{PageSize: int64(p.PageSize)}
	if after != "" {
		m.After = &after
	}

	return m
}

// metaV3 renders the pagination block for a v3 list response.
func metaV3(p incidents.Page, after string) incidentio.PaginationMetaResultV3 {
	m := incidentio.PaginationMetaResultV3{PageSize: int64(p.PageSize)}
	if after != "" {
		m.After = &after
	}

	return m
}

// metaWithTotal renders the pagination block for list responses that also
// report how many records matched.
func metaWithTotal(p incidents.Page, after string, total int) *incidentio.PaginationMetaResultWithTotalV2 {
	m := &incidentio.PaginationMetaResultWithTotalV2{
		PageSize:         int64(p.PageSize),
		TotalRecordCount: ptr(int64(total)),
	}
	if after != "" {
		m.After = &after
	}

	return m
}

// severityV1 narrows a v2 severity to the v1 shape the /v1 endpoints serve.
// The two carry the same fields; only the generated type differs.
func severityV1(s incidentio.SeverityV2) incidentio.SeverityV1 {
	return incidentio.SeverityV1(s)
}

// userWithRoles widens a user to the shape the /v2/users endpoints serve.
// The mock has no RBAC model, so every user gets a role derived from their
// incident.io seat rather than a configured set of custom roles.
func userWithRoles(u incidentio.UserV2) incidentio.UserWithRolesV2 {
	return incidentio.UserWithRolesV2{
		Id: u.Id, Name: u.Name, Email: u.Email, SlackUserId: u.SlackUserId,
		Role:     incidentio.UserWithRolesV2Role(u.Role),
		IsActive: true,
		BaseRole: incidentio.RBACRoleV2{
			Id: "01MOCKBASEROLE000000000000", Name: string(u.Role), Slug: string(u.Role),
		},
		CustomRoles: []incidentio.RBACRoleV2{},
		Seats:       incidentio.UserSeatsV2{},
	}
}

func ptr[T any](v T) *T { return &v }
