// Package holmes carries the settings for the HolmesGPT server that performs
// the investigations, and the startup check that says whether it can.
package holmes

import (
	"context"

	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/serving"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
)

// Group titles these flags in --help.
const Group = "holmes"

// Holmes is the HolmesGPT server that performs the investigations.
type Holmes struct {
	URL string `name:"holmes-url" env:"HOLMES_URL" group:"holmes" help:"HolmesGPT server base URL" default:"http://localhost:15050"`

	APIKey string `name:"holmes-api-key" env:"HOLMES_API_KEY" group:"holmes" help:"Bearer token, if HolmesGPT is behind auth"`

	Model string `name:"holmes-model" env:"HOLMES_MODEL" group:"holmes" help:"Model to investigate with. This is the KEY from the server's modelList, not the provider's model string. Empty uses the server default."`
}

// Client builds the HolmesGPT client.
func (o *Holmes) Client() *holmes.Client {
	opts := []holmes.Option{holmes.WithModel(o.Model)}
	if o.APIKey != "" {
		opts = append(opts, holmes.WithAPIKey(o.APIKey))
	}

	return holmes.New(o.URL, opts...)
}

// Check reports what HolmesGPT can actually do, and complains about the two
// configurations that produce useless investigations rather than obvious errors.
func (o *Holmes) Check(ctx context.Context, logger *zap.Logger, client *holmes.Client) {
	checkCtx, cancel := context.WithTimeout(ctx, serving.PreflightTimeout)
	defer cancel()

	if err := client.Health(checkCtx); err != nil {
		logger.Warn("could not reach HolmesGPT at startup; investigations will fail until it is up",
			zap.Error(err))

		return
	}

	info, err := client.Info(checkCtx)
	if err != nil {
		logger.Info("connected to HolmesGPT")
		return
	}

	logger.Info("connected to HolmesGPT",
		zap.String("version", info.Version),
		zap.Strings("models", info.Models),
		zap.Int("toolsets_enabled", info.Toolsets.Enabled),
		zap.Int("toolsets_failed", info.Toolsets.Failed))

	// A model the server does not have fails every investigation, and the error
	// only shows up once a real incident is waiting on it.
	if !info.HasModel(o.Model) {
		logger.Error("HolmesGPT does not serve the configured model — every investigation will fail",
			zap.String("holmes_model", o.Model),
			zap.Strings("available", info.Models))
	}

	// Holmes answers with no toolsets, but only from the prompt: no cluster
	// access means no evidence, and an analysis that is pure speculation.
	if info.Toolsets.Enabled == 0 {
		logger.Warn("HolmesGPT has no working toolsets — it cannot inspect anything, " +
			"so its analyses will be speculation rather than investigation")
	}
}
