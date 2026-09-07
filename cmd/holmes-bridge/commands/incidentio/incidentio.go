// Package incidentio carries the settings for the org the bridge reads
// incidents from and writes analyses back to, plus the commands for inspecting
// it through the bridge's own client.
package incidentio

import (
	"context"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/serving"
	"github.com/merlindorin/holmes-bridge/internal/infra/incidentio"
)

// Group titles these flags in --help.
const Group = "incidentio"

// IncidentIO is where the bridge reads incidents from and writes analyses back
// to. Point it at the mock for local work, or at api.incident.io for real.
type IncidentIO struct {
	URL string `name:"incidentio-url" env:"INCIDENTIO_URL" group:"incidentio" help:"incident.io API base URL. Point at the mock for local work." default:"https://api.incident.io"`

	APIKey string `name:"incidentio-api-key" env:"INCIDENTIO_API_KEY" group:"incidentio" help:"incident.io API key. Needs: view incidents, edit incidents, create incident updates."`
}

// Client builds the incident.io client.
func (o *IncidentIO) Client(logger *zap.Logger) (*incidentio.Client, error) {
	if o.APIKey == "" {
		logger.Warn("no incident.io API key set — this only works against a mock that does not require one")
	}

	return incidentio.New(o.URL, o.APIKey, incidentio.WithUserAgent("holmes-bridge/1.0"))
}

// Check verifies the key at boot, so a misconfiguration surfaces on startup
// rather than in the middle of the first real incident.
func (o *IncidentIO) Check(ctx context.Context, logger *zap.Logger, client *incidentio.Client) {
	checkCtx, cancel := context.WithTimeout(ctx, serving.PreflightTimeout)
	defer cancel()

	identity, err := client.Identity(checkCtx)

	switch {
	case incidentio.IsUnauthorized(err):
		logger.Error("incident.io rejected the API key — every call will fail until it is fixed",
			zap.Error(err))
	case err != nil:
		logger.Warn("could not reach incident.io at startup", zap.Error(err))
	default:
		logger.Info("connected to incident.io",
			zap.String("identity", identity.Name),
			zap.Any("roles", identity.Roles))
	}
}
