package commands

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/infra/incidentio"
)

// groupIncidentIO titles these flags in --help.
const groupIncidentIO = "incidentio"

// IncidentIO is where the bridge reads incidents from and writes analyses back
// to. Point it at the mock for local work, or at api.incident.io for real.
type IncidentIO struct {
	URL string `name:"incidentio-url" env:"INCIDENTIO_URL" group:"incidentio" help:"incident.io API base URL. Point at the mock for local work." default:"https://api.incident.io"`

	APIKey string `name:"incidentio-api-key" env:"INCIDENTIO_API_KEY" group:"incidentio" help:"incident.io API key. Needs: view incidents, edit incidents, create incident updates."`
}

// client builds the incident.io client.
func (o *IncidentIO) client(logger *zap.Logger) (*incidentio.Client, error) {
	if o.APIKey == "" {
		logger.Warn("no incident.io API key set — this only works against a mock that does not require one")
	}

	return incidentio.New(o.URL, o.APIKey, incidentio.WithUserAgent("holmes-bridge/1.0"))
}

// check verifies the key at boot, so a misconfiguration surfaces on startup
// rather than in the middle of the first real incident.
func (o *IncidentIO) check(ctx context.Context, logger *zap.Logger, client *incidentio.Client) {
	checkCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
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

// preflightTimeout bounds each startup connectivity check. Neither failure is
// fatal — the bridge stays up so its probes and metrics keep working, and so a
// dependency that is merely slow to start still gets used.
const preflightTimeout = 10 * time.Second
