package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/globals"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/otel"
)

// Investigate runs a single investigation from the command line.
//
// This is the fastest way to evaluate a prompt change or check the bridge's
// wiring: no webhook, no server, one incident, answer on stdout.
type Investigate struct {
	Connections `embed:""`

	IncidentID string `arg:"" help:"incident.io incident ID to investigate"`
	DryRun     bool   `help:"Print the analysis without writing it back to incident.io"`
}

// Run performs the investigation and prints the result.
func (i *Investigate) Run(
	ctx context.Context, common *cmd.Commons, _ *globals.HTTPServer, _ *globals.MetricServer,
) error {
	logger := common.MustLogger().Named("investigate")

	if i.DryRun {
		i.Investigation.WriteBack = "none"
	}

	// A cooldown exists to stop webhook-driven loops. Someone typing this
	// command has asked explicitly, so it does not apply.
	i.Investigation.Cooldown = -1

	// The meter provider is initialised so the instruments have somewhere to
	// report; nothing scrapes them for a one-shot run.
	if _, err := otel.InitMeterProvider(common.Version.Name(), common.Version.Version()); err != nil {
		return fmt.Errorf("failed to initialise the meter provider: %w", err)
	}

	m, err := metrics.New()
	if err != nil {
		return fmt.Errorf("failed to initialise metrics: %w", err)
	}

	deps, err := i.build(logger, m)
	if err != nil {
		return err
	}

	i.preflight(ctx, logger, deps)

	result, err := deps.service.Investigate(ctx, i.IncidentID)
	if err != nil {
		return fmt.Errorf("investigation failed: %w", err)
	}

	if result.Skipped != "" {
		logger.Info("investigation skipped",
			zap.String("incident", result.Reference), zap.String("reason", result.Skipped))

		return nil
	}

	fmt.Fprintf(os.Stdout, "\n=== %s (%s) ===\n\n%s\n\n",
		result.Reference, result.IncidentID, result.Analysis)
	fmt.Fprintf(os.Stdout, "--- %d tool calls in %s, written to %s ---\n",
		result.ToolCalls, result.Duration.Round(time.Millisecond), result.WrittenTo)

	return nil
}
