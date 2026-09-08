package incidentio

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/globals"
)

// Identity, Incidents and Show answer the questions that otherwise need a
// hand-rolled curl with the right bearer token: is this key valid, what can it
// see, and what would the bridge actually read for a given incident.
//
// They embed the same option group the daemon uses, so what they exercise is
// the same code path.

// Identity verifies the API key.
type Identity struct {
	IncidentIO `embed:""`
}

// Run reports who the key belongs to.
func (c *Identity) Run(
	ctx context.Context, common *cmd.Commons, _ *globals.HTTPServer, _ *globals.MetricServer,
) error {
	logger := common.MustLogger().Named("incidentio")
	printer := common.Printer()

	client, err := c.IncidentIO.Client(logger)
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	identity, err := client.Identity(callCtx)
	if err != nil {
		return fmt.Errorf("could not verify the API key: %w", err)
	}

	printer.Printf("url:        %s\n", c.URL)
	printer.Printf("identity:   %s\n", identity.Name)
	printer.Printf("dashboard:  %s\n", identity.DashboardUrl)
	printer.Printf("roles:      %s\n", joinRoles(identity.Roles))

	// The bridge needs all three to do its job, and a key missing one fails
	// only at the moment it matters.
	for _, want := range []string{"incident_creator", "incident_editor", "viewer"} {
		if !hasRole(identity.Roles, want) {
			printer.Printf("\nwarning: the key has no %q role. "+
				"The bridge needs to view incidents, edit them, and create updates.\n", want)
		}
	}

	return nil
}

// Incidents lists incidents.
type Incidents struct {
	IncidentIO `embed:""`

	Limit int `help:"How many to list" default:"20"`
}

// Run prints a table of incidents.
func (c *Incidents) Run(
	ctx context.Context, common *cmd.Commons, _ *globals.HTTPServer, _ *globals.MetricServer,
) error {
	logger := common.MustLogger().Named("incidentio")
	printer := common.Printer()

	client, err := c.IncidentIO.Client(logger)
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	incidents, err := client.Incidents(callCtx, c.Limit)
	if err != nil {
		return fmt.Errorf("could not list incidents: %w", err)
	}

	if len(incidents) == 0 {
		printer.Println("No incidents.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REFERENCE\tSTATUS\tSEVERITY\tNAME")

	for _, in := range incidents {
		severity := "—"
		if in.Severity != nil {
			severity = in.Severity.Name
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			in.Reference, in.IncidentStatus.Name, severity, truncateCell(in.Name, 50))
	}

	return w.Flush()
}

// Show prints one incident as the bridge sees it.
type Show struct {
	IncidentIO `embed:""`

	ID string `arg:"" help:"incident.io incident ID"`
}

// Run prints the incident with the context an investigation would use.
func (c *Show) Run(
	ctx context.Context, common *cmd.Commons, _ *globals.HTTPServer, _ *globals.MetricServer,
) error {
	logger := common.MustLogger().Named("incidentio")
	printer := common.Printer()

	client, err := c.IncidentIO.Client(logger)
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	incident, err := client.Incident(callCtx, c.ID)
	if err != nil {
		return fmt.Errorf("could not load the incident: %w", err)
	}

	printer.Printf("%s  %s\n", incident.Reference, incident.Name)
	printer.Printf("status:    %s\n", incident.IncidentStatus.Name)

	if incident.Severity != nil {
		printer.Printf("severity:  %s\n", incident.Severity.Name)
	}

	printer.Printf("mode:      %s\n", incident.Mode)
	printer.Printf("created:   %s\n", incident.CreatedAt.Format(time.RFC3339))

	if incident.Summary != nil && *incident.Summary != "" {
		printer.Printf("\nsummary:\n  %s\n", strings.Join(strings.Fields(*incident.Summary), " "))
	}

	// The two collections an investigation reads alongside the incident. Seeing
	// them here is how you tell whether a thin analysis was the model's fault
	// or a thin prompt.
	if alerts, alertErr := client.IncidentAlerts(callCtx, c.ID); alertErr == nil && len(alerts) > 0 {
		printer.Printf("\nalerts (%d):\n", len(alerts))

		for _, a := range alerts {
			printer.Printf("  [%s] %s\n", a.Alert.Status, a.Alert.Title)
		}
	}

	if updates, updateErr := client.IncidentUpdates(callCtx, c.ID); updateErr == nil && len(updates) > 0 {
		printer.Printf("\nupdates (%d):\n", len(updates))

		for _, u := range updates {
			if u.Message == nil || *u.Message == "" {
				continue
			}

			printer.Printf("  [%s] %s\n",
				u.CreatedAt.Format("15:04"), truncateCell(strings.Join(strings.Fields(*u.Message), " "), 90))
		}
	}

	return nil
}

// requestTimeout bounds a single one-shot API call.
const requestTimeout = 30 * time.Second

func joinRoles(roles []incidentio.IdentityV1Roles) string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}

	return strings.Join(out, ", ")
}

func hasRole(roles []incidentio.IdentityV1Roles, want string) bool {
	for _, r := range roles {
		if string(r) == want {
			return true
		}
	}

	return false
}

func truncateCell(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}

	return string(runes[:maxRunes]) + "…"
}
