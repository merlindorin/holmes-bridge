package fixtures_test

import (
	"strings"
	"testing"
	"time"

	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
)

func TestLoadShippedScenarios(t *testing.T) {
	t.Parallel()

	scenarios, err := fixtures.LoadDir("../../../fixtures/scenarios")
	if err != nil {
		t.Fatalf("the shipped scenarios must load: %v", err)
	}

	if _, ok := scenarios["checkout-latency"]; !ok {
		t.Fatalf("checkout-latency scenario missing, got %v", keys(scenarios))
	}

	for name, s := range scenarios {
		e, expandErr := s.Expand(time.Now())
		if expandErr != nil {
			t.Errorf("%s: expand: %v", name, expandErr)
			continue
		}

		if len(e.Incidents) == 0 {
			t.Errorf("%s: expanded to no incidents", name)
		}
	}
}

func TestExpandLinksIncidentToItsReferences(t *testing.T) {
	t.Parallel()

	scenarios, err := fixtures.LoadDir("../../../fixtures/scenarios")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e, err := scenarios["checkout-latency"].Expand(time.Now())
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	var live *struct {
		name     string
		status   string
		severity string
		roles    int
	}

	for _, in := range e.Incidents {
		if in.Reference != "TEST-142" {
			continue
		}

		sev := ""
		if in.Severity != nil {
			sev = in.Severity.Name
		}

		live = &struct {
			name     string
			status   string
			severity string
			roles    int
		}{in.Name, in.IncidentStatus.Name, sev, len(in.IncidentRoleAssignments)}
	}

	if live == nil {
		t.Fatal("TEST-142 not found after expansion")
	}

	if live.status != "Investigating" {
		t.Errorf("status: got %q, want Investigating", live.status)
	}

	if live.severity != "Critical" {
		t.Errorf("severity: got %q, want Critical", live.severity)
	}

	if live.roles != 2 {
		t.Errorf("role assignments: got %d, want 2", live.roles)
	}

	// The three fixture alerts must all be attached to TEST-142.
	attached := 0

	for _, ia := range e.IncidentAlerts {
		if ia.Incident.Reference == "TEST-142" {
			attached++
		}
	}

	if attached != 3 {
		t.Errorf("attached alerts: got %d, want 3", attached)
	}
}

func TestExpandResolvesRelativeTimestamps(t *testing.T) {
	t.Parallel()

	scenarios, _ := fixtures.LoadDir("../../../fixtures/scenarios")
	now := time.Now()

	e, err := scenarios["checkout-latency"].Expand(now)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	for _, in := range e.Incidents {
		if in.Reference != "TEST-142" {
			continue
		}

		// "-40m" in the fixture must land 40 minutes before load time.
		if delta := now.Sub(in.CreatedAt); delta < 39*time.Minute || delta > 41*time.Minute {
			t.Errorf("TEST-142 created_at is %s ago, want ~40m", delta.Round(time.Minute))
		}
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := fixtures.Decode(strings.NewReader("name: x\nsevrities: []\n"))
	if err == nil {
		t.Fatal("a misspelled key should fail the load, not be ignored")
	}
}

func TestValidateCatchesDanglingReferences(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"unknown status": `
name: t
statuses: [{id: triage, name: Triage, category: triage, rank: 1}]
incidents: [{id: I1, name: x, status: nope}]
`,
		"unknown severity": `
name: t
severities: [{id: SEV1, name: Critical, rank: 3}]
incidents: [{id: I1, name: x, severity: SEV9}]
`,
		"unknown alert": `
name: t
incidents: [{id: I1, name: x, alerts: [MISSING]}]
`,
		"unknown role holder": `
name: t
roles: [{id: lead, name: Lead, role_type: lead}]
incidents: [{id: I1, name: x, roles: {lead: NOBODY}}]
`,
		"catalog entry with unknown type": `
name: t
catalog: {types: [{id: CT1, name: Service}], entries: [{id: CE1, type: CT9, name: svc}]}
`,
	} {
		if _, err := fixtures.Decode(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: want a validation error, got none", name)
		}
	}
}

func TestValidateRequiresAName(t *testing.T) {
	t.Parallel()

	if _, err := fixtures.Decode(strings.NewReader("description: no name here\n")); err == nil {
		t.Fatal("a scenario without a name should not load")
	}
}

func keys(m map[string]*fixtures.Scenario) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
