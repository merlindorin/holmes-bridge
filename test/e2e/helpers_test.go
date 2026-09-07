package e2e_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
	"github.com/merlindorin/holmes-bridge/internal/infra/memory"
)

// incidentFilter keeps the List call sites readable; a zero filter matches all.
type incidentFilter struct{}

func (incidentFilter) f() incidents.Filter { return incidents.Filter{} }

func pageAll() incidents.Page {
	return incidents.Page{PageSize: incidents.MaxPageSize}
}

// updates reads an incident's update feed straight out of the store, which is
// how the test observes what the bridge wrote back.
func (h *harness) updates(t *testing.T, incidentID string) []incidentio.IncidentUpdateV2 {
	t.Helper()

	items, _, err := memory.NewIncidentRepository(h.store).
		ListUpdates(context.Background(), incidentID, pageAll())
	if err != nil {
		t.Fatalf("ListUpdates: %v", err)
	}

	return items
}

func (h *harness) updateCount(t *testing.T, incidentID string) int {
	t.Helper()

	return len(h.updates(t, incidentID))
}

func unixNow() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}
