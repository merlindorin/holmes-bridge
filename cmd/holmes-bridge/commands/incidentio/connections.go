package incidentio

import (
	"context"

	"go.uber.org/zap"

	holmescmd "github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/holmes"
	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/investigation"
	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/ntfy"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	"github.com/merlindorin/holmes-bridge/internal/infra/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// Connections is everything both subcommands need, grouped by the thing it
// configures rather than as one flat list. Each group owns its own flags, its
// own client, and its own startup check.
//
// The groups also title the sections of --help.
type Connections struct {
	IncidentIO                  `embed:""`
	holmescmd.Holmes            `embed:""`
	ntfy.Ntfy                   `embed:""`
	investigation.Investigation `embed:""`
}

// dependencies are the wired clients and the service that uses them.
type dependencies struct {
	service *investigate.Service
	client  *incidentio.Client
	holmes  *holmes.Client
}

// build wires the clients and the investigation service.
func (o *Connections) build(logger *zap.Logger, m *metrics.Metrics) (dependencies, error) {
	cfg, err := o.Investigation.Config()
	if err != nil {
		return dependencies{}, err
	}

	client, err := o.IncidentIO.Client(logger)
	if err != nil {
		return dependencies{}, err
	}

	notifier, err := o.Ntfy.Notifier(logger)
	if err != nil {
		return dependencies{}, err
	}

	holmesClient := o.Holmes.Client()

	return dependencies{
		service: investigate.New(logger, client, holmesClient, m, cfg, notifier),
		client:  client,
		holmes:  holmesClient,
	}, nil
}

// preflight checks both dependencies at boot, so a misconfiguration is reported
// on startup rather than in the middle of the first real incident.
//
// Neither failure is fatal: the bridge stays up so its probes and metrics keep
// working, and so a dependency that is merely slow to start still gets used.
func (o *Connections) preflight(ctx context.Context, logger *zap.Logger, deps dependencies) {
	o.IncidentIO.Check(ctx, logger, deps.client)
	o.Holmes.Check(ctx, logger, deps.holmes)
}
