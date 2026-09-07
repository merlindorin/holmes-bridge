package investigate_test

import (
	"strings"
	"testing"
	"time"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
)

func TestBuildPromptCarriesTheIncidentContext(t *testing.T) {
	t.Parallel()

	incident := liveIncident()
	incident.IncidentRoleAssignments = []incidentio.IncidentRoleAssignmentV2{{
		Role:     incidentio.EmbeddedIncidentRoleV2{Name: "Incident Lead"},
		Assignee: &incidentio.UserV2{Name: "Test Lead"},
	}}

	description := "p99 above 2s for 10 minutes"
	sourceURL := "https://prometheus.example.com/graph"

	alerts := []incidentio.IncidentAlertV2{{
		Alert: incidentio.AlertSlimV2{
			Title:       "checkout-api p99 latency above 2s",
			Status:      incidentio.AlertSlimV2StatusFiring,
			Description: &description,
			SourceUrl:   &sourceURL,
			CreatedAt:   time.Now().Add(-42 * time.Minute),
		},
	}}

	older := "Declared from a Prometheus alert."
	newer := "Connection pool is saturated at 98/100."

	updates := []incidentio.IncidentUpdateV2{
		{Message: &newer, CreatedAt: time.Now().Add(-12 * time.Minute)},
		{Message: &older, CreatedAt: time.Now().Add(-40 * time.Minute)},
	}

	prompt := investigate.BuildPrompt(incident, alerts, updates)

	for _, want := range []string{
		"TEST-142",
		"Checkout latency spike",
		"Investigating",
		"Critical",
		"Test Lead (Incident Lead)",
		"checkout-api p99 latency above 2s",
		"p99 above 2s for 10 minutes",
		sourceURL,
		older,
		newer,
	} {
		if !strings.Contains(prompt.Ask, want) {
			t.Errorf("prompt is missing %q\n---\n%s", want, prompt.Ask)
		}
	}

	// The update feed must read oldest-first, so it tells a story.
	if strings.Index(prompt.Ask, older) > strings.Index(prompt.Ask, newer) {
		t.Error("updates should appear oldest first")
	}

	// The system prompt has to demand the structure the write-back assumes.
	for _, want := range []string{"Summary", "Evidence", "Most likely cause", "Suggested next steps"} {
		if !strings.Contains(prompt.System, want) {
			t.Errorf("system prompt is missing the %q section", want)
		}
	}
}

func TestBuildPromptHandlesAThinIncident(t *testing.T) {
	t.Parallel()

	// An incident with no severity, no alerts, no updates and no responders is
	// the realistic worst case; it must still produce a usable question rather
	// than panic on a nil pointer.
	bare := incidentio.IncidentV2{
		Id: "01INCIDENT", Reference: "TEST-1", Name: "Something is wrong",
		IncidentStatus: incidentio.IncidentStatusV2{Name: "Triage"},
		CreatedAt:      time.Now(),
	}

	prompt := investigate.BuildPrompt(bare, nil, nil)

	if !strings.Contains(prompt.Ask, "TEST-1") || !strings.Contains(prompt.Ask, "Something is wrong") {
		t.Errorf("a bare incident should still produce a prompt:\n%s", prompt.Ask)
	}

	if strings.Contains(prompt.Ask, "## Alerts") {
		t.Error("no alerts should mean no alerts section")
	}
}

func TestBuildPromptCollapsesMultilineText(t *testing.T) {
	t.Parallel()

	incident := liveIncident()
	summary := "line one\n  line two\n\n\tline three"
	incident.Summary = &summary

	prompt := investigate.BuildPrompt(incident, nil, nil)

	if !strings.Contains(prompt.Ask, "line one line two line three") {
		t.Errorf("multi-line summaries should collapse onto one bullet:\n%s", prompt.Ask)
	}
}
