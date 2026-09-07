// Package commands holds the holmes-bridge subcommands.
package commands

import (
	"context"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	"github.com/merlindorin/holmes-bridge/internal/infra/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// Connections is everything both subcommands need, grouped by the thing it
// configures rather than as one flat list. Each group owns its own flags, its
// own client, and its own startup check, so a change to how the bridge talks to
// ntfy does not touch the file that knows about incident.io.
//
// The groups also title the sections of --help.
type Connections struct {
	IncidentIO    `embed:""`
	Holmes        `embed:""`
	Ntfy          `embed:""`
	Investigation `embed:""`
}

// dependencies are the wired clients and the service that uses them.
type dependencies struct {
	service *investigate.Service
	client  *incidentio.Client
	holmes  *holmes.Client
}

// build wires the clients and the investigation service.
func (o *Connections) build(logger *zap.Logger, m *metrics.Metrics) (dependencies, error) {
	cfg, err := o.Investigation.config()
	if err != nil {
		return dependencies{}, err
	}

	client, err := o.IncidentIO.client(logger)
	if err != nil {
		return dependencies{}, err
	}

	notifier, err := o.Ntfy.notifier(logger)
	if err != nil {
		return dependencies{}, err
	}

	holmesClient := o.Holmes.client()

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
	o.IncidentIO.check(ctx, logger, deps.client)
	o.Holmes.check(ctx, logger, deps.holmes)
}
