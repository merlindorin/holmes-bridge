// Package commands holds the incidentio-mock subcommands.
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/requestid"
	"github.com/gin-contrib/timeout"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/merlindorin/go-shared/pkg/cmd"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	controlV1 "github.com/merlindorin/holmes-bridge/api/control/v1"
	"github.com/merlindorin/holmes-bridge/api/incidentio"
	mockV1 "github.com/merlindorin/holmes-bridge/api/incidentio/v1"
	"github.com/merlindorin/holmes-bridge/api/private"
	privateV1 "github.com/merlindorin/holmes-bridge/api/private/v1"
	"github.com/merlindorin/holmes-bridge/internal/globals"
	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
	"github.com/merlindorin/holmes-bridge/internal/infra/memory"
	infrawebhooks "github.com/merlindorin/holmes-bridge/internal/infra/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/middleware"
	"github.com/merlindorin/holmes-bridge/internal/otel"
)

// DefaultWebhookSecret is a fixed development secret, so the mock signs
// correctly out of the box. It decodes to "TEST-MOCK-NOT-A-REAL-SECRET!", which
// is the point: it is published in this repo, and anyone who finds it in a log
// or a config should be able to tell at a glance that it protects nothing.
//
// Anything that matters sets --webhook-secret.
//
//nolint:gosec // a published development default, not a credential
const DefaultWebhookSecret = "whsec_VEVTVC1NT0NLLU5PVC1BLVJFQUwtU0VDUkVUIQ=="

// Serve starts the mock API.
type Serve struct {
	FixturesDir string `name:"fixtures-dir" env:"FIXTURES_DIR" help:"Directory of scenario YAML files" default:"fixtures/scenarios"`
	Scenario    string `name:"scenario" env:"SCENARIO" help:"Scenario to load at boot; defaults to the only one, or 'checkout-latency'" default:""`

	APIKeys []string `name:"api-key" env:"API_KEYS" sep:"none" help:"Accepted bearer token. Repeatable. Leave empty to accept any request."`

	// sep:"none" matters here: a subscription spec is itself comma-separated,
	// and kong would otherwise split it into fragments.
	WebhookSecret string   `name:"webhook-secret" env:"WEBHOOK_SECRET" help:"Secret used to sign outbound webhooks" default:"${default_webhook_secret}"`
	Webhook       []string `name:"webhook" env:"WEBHOOKS" sep:"none" help:"Subscriber, as name=..,url=..[,secret=..][,events=a|b]. Repeatable."`

	Timeout time.Duration `default:"15s" help:"Per-request timeout"`
}

// Run boots the mock and blocks until the process is asked to stop.
func (s *Serve) Run(
	ctx context.Context,
	common *cmd.Commons,
	httpServer *globals.HTTPServer,
	metricServer *globals.MetricServer,
) error {
	name := common.Version.Name()
	version := common.Version.Version()
	logger := common.MustLogger().Named(name)

	scenarios, err := fixtures.LoadDir(s.FixturesDir)
	if err != nil {
		return fmt.Errorf("failed to load fixtures: %w", err)
	}

	active, err := s.pickScenario(scenarios)
	if err != nil {
		return err
	}

	expanded, err := scenarios[active].Expand(time.Now())
	if err != nil {
		return fmt.Errorf("failed to expand scenario %s: %w", active, err)
	}

	store := memory.NewStore()
	store.Seed(expanded)

	subscriptions, err := s.subscriptions()
	if err != nil {
		return err
	}

	deliverer, err := infrawebhooks.NewDeliverer(logger, s.WebhookSecret, subscriptions)
	if err != nil {
		return fmt.Errorf("failed to build the webhook deliverer: %w", err)
	}

	m, err := metrics.New()
	if err != nil {
		return fmt.Errorf("failed to initialise metrics: %w", err)
	}

	logger.Info("Starting mock incident.io",
		zap.String("version", version),
		zap.String("address", httpServer.Addr()),
		zap.String("scenario", active),
		zap.Int("scenarios", len(scenarios)),
		zap.Int("webhook_subscriptions", len(subscriptions)),
		zap.Bool("auth_required", len(s.APIKeys) > 0),
		zap.Any("records", store.Counts()))

	if len(subscriptions) == 0 {
		logger.Warn("no webhook subscribers configured — state changes will not be delivered anywhere. " +
			"Pass --webhook url=http://localhost:18081/webhooks/incidentio to wire up the bridge.")
	}

	gin.SetMode(ginMode(common.Development))

	router := gin.New()
	router.Use(otelgin.Middleware(name))
	router.Use(timeout.New(timeout.WithTimeout(s.Timeout)))
	router.Use(requestid.New())
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	router.Use(middleware.ErrorHandler(logger))
	router.Use(middleware.Metrics(m))
	router.NoRoute(middleware.NotFound())

	metricServer.Mount(router)
	private.RegisterHandlers(router, privateV1.NewServer(logger, nil))
	controlV1.NewServer(logger, store, scenarios, deliverer, active).Mount(router)

	// Only the incident.io surface is behind the API key; the control plane and
	// the probes stay reachable so a harness can always drive and inspect it.
	api := router.Group("", middleware.RequireAPIKey(s.APIKeys...))
	incidentio.RegisterHandlers(api, mockV1.NewServer(
		logger, store,
		memory.NewIncidentRepository(store),
		memory.NewWorkRepository(store),
		memory.NewCatalogueRepository(store),
		memory.NewAlertRepository(store),
		memory.NewCatalogRepository(store),
		deliverer,
	))

	errs, ctx := errgroup.WithContext(ctx)
	errs.Go(waitForShutdownSignal(ctx))
	errs.Go(runMeterProvider(ctx, name, version, logger))
	errs.Go(deliverer.Start(ctx))
	errs.Go(httpServer.Start(ctx, logger, router))

	if waitErr := errs.Wait(); waitErr != nil {
		logger.Info("Shutting down", zap.Error(waitErr))
	}

	return nil
}

// pickScenario resolves which fixture to serve at boot.
func (s *Serve) pickScenario(scenarios map[string]*fixtures.Scenario) (string, error) {
	if s.Scenario != "" {
		if _, ok := scenarios[s.Scenario]; !ok {
			return "", fmt.Errorf("no scenario named %q in %s (found %v)",
				s.Scenario, s.FixturesDir, names(scenarios))
		}

		return s.Scenario, nil
	}

	// With a single fixture there is no ambiguity to resolve.
	if len(scenarios) == 1 {
		for name := range scenarios {
			return name, nil
		}
	}

	if _, ok := scenarios["checkout-latency"]; ok {
		return "checkout-latency", nil
	}

	return "", fmt.Errorf("several scenarios are available (%v); pick one with --scenario", names(scenarios))
}

func (s *Serve) subscriptions() ([]infrawebhooks.Subscription, error) {
	out := make([]infrawebhooks.Subscription, 0, len(s.Webhook))

	for _, spec := range s.Webhook {
		sub, err := infrawebhooks.ParseSubscription(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid --webhook %q: %w", spec, err)
		}

		out = append(out, sub)
	}

	return out, nil
}

func names(scenarios map[string]*fixtures.Scenario) []string {
	out := make([]string, 0, len(scenarios))
	for name := range scenarios {
		out = append(out, name)
	}

	return out
}

func runMeterProvider(ctx context.Context, name, version string, logger *zap.Logger) func() error {
	return func() error {
		provider, err := otel.InitMeterProvider(name, version)
		if err != nil {
			return fmt.Errorf("failed to initialise the meter provider: %w", err)
		}

		<-ctx.Done()

		if shutdownErr := provider.Shutdown(context.Background()); shutdownErr != nil {
			logger.Error("failed to shut down the meter provider", zap.Error(shutdownErr))
			return shutdownErr
		}

		return nil
	}
}

func waitForShutdownSignal(ctx context.Context) func() error {
	return func() error {
		term := make(chan os.Signal, 1)
		signal.Notify(term, os.Interrupt, syscall.SIGTERM)

		select {
		case <-ctx.Done():
			return nil
		case <-term:
			return errors.New("received shutdown signal")
		}
	}
}

func ginMode(development bool) string {
	if development {
		return gin.DebugMode
	}

	return gin.ReleaseMode
}
