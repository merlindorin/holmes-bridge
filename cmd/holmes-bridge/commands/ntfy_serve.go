package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-contrib/requestid"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/merlindorin/go-shared/pkg/cmd"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	alertsV1 "github.com/merlindorin/holmes-bridge/api/alerts/v1"
	"github.com/merlindorin/holmes-bridge/api/private"
	privateV1 "github.com/merlindorin/holmes-bridge/api/private/v1"
	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/globals"
	"github.com/merlindorin/holmes-bridge/internal/infra/holmes"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/middleware"
)

// NtfyServe runs the Alertmanager pipeline: an alert fires, HolmesGPT
// investigates, and the conclusion is pushed to a phone.
//
// incident.io is not involved. There is no incident to read, nothing is written
// back, and the notification is the whole output — which makes this the shortest
// path from "Prometheus is unhappy" to "here is why", for anyone who does not
// run incident.io or does not want an incident declared for everything.
type NtfyServe struct {
	Holmes        `embed:""`
	Ntfy          `embed:""`
	Investigation `embed:""`
	Expose        `embed:""`

	Token []string `name:"alertmanager-token" env:"ALERTMANAGER_TOKENS" sep:"none" group:"alertmanager" help:"Bearer token Alertmanager must send. Repeatable. Alertmanager cannot sign its webhooks, so without one a reachable endpoint is open to anyone."`

	Timeout time.Duration `default:"30s" group:"alertmanager" help:"Per-request timeout for the bridge's own endpoints"`
}

// Run boots the receiver and blocks until the process is asked to stop.
func (s *NtfyServe) Run(
	ctx context.Context,
	common *cmd.Commons,
	httpServer *globals.HTTPServer,
	metricServer *globals.MetricServer,
) error {
	name := common.Version.Name()
	version := common.Version.Version()
	logger := common.MustLogger().Named(name)

	if s.Ntfy.Topic == "" {
		return errors.New(
			"no ntfy topic set, and the notification is this pipeline's only output.\n" +
				"Set --ntfy-topic (NTFY_TOPIC), or use `serve` for the incident.io pipeline")
	}

	cfg, err := s.Investigation.config()
	if err != nil {
		return err
	}

	m, err := metrics.New()
	if err != nil {
		return fmt.Errorf("failed to initialise metrics: %w", err)
	}

	notifier, err := s.Ntfy.notifier(logger)
	if err != nil {
		return err
	}

	holmesClient := s.Holmes.client()

	// No incident.io client: this pipeline never reads or writes one. The
	// service tolerates a nil one because only the incident path uses it.
	service := investigate.New(logger, nil, holmesClient, m, cfg, notifier)

	logger.Info("Starting holmes-bridge (alertmanager → ntfy)",
		zap.String("version", version),
		zap.String("address", httpServer.Addr()),
		zap.String("holmes_url", s.Holmes.URL),
		zap.Int("max_concurrent", s.Investigation.MaxConcurrent),
		zap.Duration("cooldown", s.Investigation.Cooldown),
		zap.Bool("authenticated", len(s.Token) > 0))

	if len(s.Token) == 0 {
		logger.Warn("no --alertmanager-token set. Alertmanager cannot sign its webhooks, " +
			"so anyone who can reach this endpoint can start investigations and spend on a model.")
	}

	s.Holmes.check(ctx, logger, holmesClient)

	// A synchronous manual trigger is not offered here, but an investigation
	// still outlives the default write timeout when the process is slow to
	// answer a probe under load.
	if want := s.Investigation.Timeout + writeTimeoutHeadroom; httpServer.WriteTimeout < want {
		httpServer.WriteTimeout = want
	}

	gin.SetMode(ginMode(common.Development))

	router := localRouter(name, logger, m)
	metricServer.Mount(router)
	private.RegisterHandlers(router, privateV1.NewServer(logger, holmesReadiness(holmesClient)))

	receiver := alertsV1.NewServer(logger, service, m, s.Token...)
	receiver.Mount(router)

	peer, err := s.Expose.enrollFor(ctx, logger, s.exposedRoutes(logger, m, receiver, router), alertmanagerPath)
	if err != nil {
		return err
	}

	errs, ctx := errgroup.WithContext(ctx)
	errs.Go(waitForShutdownSignal(ctx))
	errs.Go(runMeterProvider(ctx, name, version, logger))
	errs.Go(httpServer.Start(ctx, logger, router))

	if peer != nil {
		errs.Go(func() error {
			if serveErr := peer.Serve(ctx); serveErr != nil && !errors.Is(serveErr, context.Canceled) {
				return fmt.Errorf("holt tunnel stopped: %w", serveErr)
			}

			return nil
		})
	}

	if waitErr := errs.Wait(); waitErr != nil {
		logger.Info("Shutting down", zap.Error(waitErr))
	}

	return nil
}

// holmesReadiness makes the probe track the one dependency that matters: with
// Holmes down the bridge can accept a webhook but cannot do anything with it.
func holmesReadiness(client *holmes.Client) map[string]privateV1.ReadinessCheck {
	return map[string]privateV1.ReadinessCheck{
		"holmesgpt": func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			return client.Health(ctx)
		},
	}
}

// exposedRoutes picks what the tunnel carries: the receiver alone by default.
func (s *NtfyServe) exposedRoutes(
	logger *zap.Logger, m *metrics.Metrics, receiver *alertsV1.Server, router http.Handler,
) http.Handler {
	if s.Expose.All {
		logger.Warn("--expose-all: publishing every route through the tunnel, including /metrics")

		return router
	}

	r := gin.New()
	r.Use(requestid.New())
	r.Use(ginzap.Ginzap(logger.Named("public"), time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(logger.Named("public"), true))
	r.Use(middleware.ErrorHandler(logger))
	r.Use(middleware.Metrics(m))
	r.NoRoute(middleware.NotFound())

	receiver.MountPublic(r)

	return r
}

// alertmanagerPath is the route Alertmanager delivers to.
const alertmanagerPath = "/webhooks/alertmanager"
