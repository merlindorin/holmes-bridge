package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/gin-contrib/requestid"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/merlindorin/go-shared/pkg/cmd"
	"github.com/openotters/holt/pkg/expose"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	bridgeV1 "github.com/merlindorin/holmes-bridge/api/bridge/v1"
	"github.com/merlindorin/holmes-bridge/api/private"
	privateV1 "github.com/merlindorin/holmes-bridge/api/private/v1"
	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
	"github.com/merlindorin/holmes-bridge/internal/globals"
	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/middleware"
	"github.com/merlindorin/holmes-bridge/internal/otel"
)

// defaultTriggers are the events worth spending an investigation on: an
// incident opening, and an incident moving to a new status. Edits and alert
// events are deliberately excluded — they fire constantly and rarely mean the
// picture has changed.
//
//nolint:gochecknoglobals // a package-level default set is the clearest form
var defaultTriggers = []string{
	string(webhooks.PublicIncidentIncidentCreatedV2),
	string(webhooks.PublicIncidentIncidentStatusUpdatedV2),
}

// Serve runs the bridge as a webhook receiver.
type Serve struct {
	Connections `embed:""`

	WebhookSecret []string `name:"webhook-secret" env:"WEBHOOK_SECRET" sep:"none" help:"incident.io webhook signing secret. Repeatable, so a secret can be rotated without downtime."`
	Triggers      []string `name:"trigger" env:"TRIGGERS" sep:"none" help:"Webhook event that starts an investigation. Repeatable, and REPLACES the defaults (incident created + status updated) rather than adding to them — list every event you want."`

	SkipSignatureVerification bool `name:"skip-signature-verification" env:"SKIP_SIGNATURE_VERIFICATION" help:"Accept unsigned webhooks. Never use this outside local development."`

	Expose `embed:""`

	Timeout time.Duration `default:"30s" help:"Per-request timeout for the bridge's own endpoints"`
}

// Run boots the bridge and blocks until the process is asked to stop.
func (s *Serve) Run(
	ctx context.Context,
	common *cmd.Commons,
	httpServer *globals.HTTPServer,
	metricServer *globals.MetricServer,
) error {
	name := common.Version.Name()
	version := common.Version.Version()
	logger := common.MustLogger().Named(name)

	triggers, err := s.triggerEvents()
	if err != nil {
		return err
	}

	verifier, err := s.buildVerifier(logger)
	if err != nil {
		return err
	}

	m, err := metrics.New()
	if err != nil {
		return fmt.Errorf("failed to initialise metrics: %w", err)
	}

	deps, err := s.build(logger, m)
	if err != nil {
		return err
	}

	// POST /investigations/{id} answers synchronously, and an investigation runs
	// for minutes. The default 30s write timeout would cut every one of those
	// responses off mid-flight — the work completes and posts, but the caller
	// sees a dropped connection and no analysis. Raise the ceiling to fit.
	//
	// The webhook path is unaffected: it acknowledges in milliseconds.
	if want := s.Investigation.Timeout + writeTimeoutHeadroom; httpServer.WriteTimeout < want {
		logger.Info("raising the HTTP write timeout to fit a synchronous investigation",
			zap.Duration("was", httpServer.WriteTimeout),
			zap.Duration("now", want),
			zap.Duration("investigation_timeout", s.Investigation.Timeout))

		httpServer.WriteTimeout = want
	}

	logger.Info("Starting holmes-bridge",
		zap.String("version", version),
		zap.String("address", httpServer.Addr()),
		zap.String("incidentio_url", s.IncidentIO.URL),
		zap.String("holmes_url", s.Holmes.URL),
		zap.String("write_back", s.Investigation.WriteBack),
		zap.Any("triggers", triggers),
		zap.Int("max_concurrent", s.Investigation.MaxConcurrent),
		zap.Duration("cooldown", s.Investigation.Cooldown))

	warnAboutCooldown(logger, triggers, s.Investigation.Cooldown)

	s.preflight(ctx, logger, deps)

	gin.SetMode(ginMode(common.Development))

	router := localRouter(name, logger, m)
	metricServer.Mount(router)

	// Readiness tracks HolmesGPT: with it down, the bridge can accept a webhook
	// but cannot do the one thing it exists for.
	private.RegisterHandlers(router, privateV1.NewServer(logger, map[string]privateV1.ReadinessCheck{
		"holmesgpt": func() error {
			checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			return deps.holmes.Health(checkCtx)
		},
	}))

	bridge := bridgeV1.NewServer(logger, verifier, deps.service, m, triggers)
	bridge.Mount(router)

	// Enrol before the errgroup exists, on the parent context.
	//
	// errgroup.WithContext cancels with a *cause*, and net/http reports that
	// cause when a request is cancelled. So if enrolment shared the group's
	// context, a local port-bind failure from the HTTP server would surface as
	// "Post https://hub/api/enroll: cannot listen on 0.0.0.0:18081" — a local
	// error wearing a remote error's clothing. Keeping the two apart means each
	// failure is reported as itself.
	peer, err := s.enroll(ctx, logger, s.exposedRoutes(logger, m, bridge, router))
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

// exposedRoutes picks what the tunnel carries: the signature-verified webhook
// endpoint by default, or the whole router under --expose-all.
func (s *Serve) exposedRoutes(
	logger *zap.Logger, m *metrics.Metrics, bridge *bridgeV1.Server, router http.Handler,
) http.Handler {
	if !s.All {
		return publicRouter(logger, m, bridge)
	}

	logger.Warn("--expose-all: publishing every route through the tunnel, " +
		"including POST /investigations/{id}, which takes no credential. " +
		"Anyone who can reach the tunnel URL can start investigations.")

	return router
}

// localRouter is what the process serves on its own port: everything, since
// reaching it already means being inside.
func localRouter(name string, logger *zap.Logger, m *metrics.Metrics) *gin.Engine {
	r := gin.New()
	r.Use(otelgin.Middleware(name))
	r.Use(requestid.New())
	r.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(logger, true))
	r.Use(middleware.ErrorHandler(logger))
	r.Use(middleware.Metrics(m))
	r.NoRoute(middleware.NotFound())

	return r
}

// publicRouter is what the tunnel serves: the signature-verified webhook
// endpoint and nothing else.
func publicRouter(logger *zap.Logger, m *metrics.Metrics, bridge *bridgeV1.Server) http.Handler {
	r := gin.New()
	r.Use(requestid.New())
	r.Use(ginzap.Ginzap(logger.Named("public"), time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(logger.Named("public"), true))
	r.Use(middleware.ErrorHandler(logger))
	r.Use(middleware.Metrics(m))
	r.NoRoute(middleware.NotFound())

	bridge.MountPublic(r)

	return r
}

// enroll registers with the holt hub when --expose is set and announces the
// webhook URL, returning the peer for the caller to serve. It returns
// (nil, nil) when exposure is off.
//
// Enrolment is deliberately separate from serving: it happens once, before
// anything is listening, so the URL is part of startup output and its failures
// are not entangled with the servers'.
func (s *Serve) enroll(
	ctx context.Context, logger *zap.Logger, handler http.Handler,
) (*expose.Peer, error) {
	if !s.Enabled {
		return nil, nil //nolint:nilnil // "no tunnel, no error" is the honest result
	}

	peer, err := s.tunnel(ctx, logger, handler)
	if err != nil {
		return nil, err
	}

	url, err := webhookURL(peer)
	if err != nil {
		return nil, err
	}

	announce(logger, peer, url)

	return peer, nil
}

func (s *Serve) triggerEvents() ([]webhooks.EventType, error) {
	names := s.Triggers
	if len(names) == 0 {
		names = defaultTriggers
	}

	out := make([]webhooks.EventType, 0, len(names))
	seen := map[webhooks.EventType]bool{}

	for _, name := range names {
		event := webhooks.EventType(name)
		if !event.Valid() {
			return nil, fmt.Errorf("unknown trigger event %q.\nSee GET /_mock/webhooks/subscriptions "+
				"on the mock, or docs.incident.io, for the vocabulary", name)
		}

		if seen[event] {
			continue
		}

		seen[event] = true

		out = append(out, event)
	}

	return out, nil
}

// secondaryTriggers fire repeatedly during an incident that is already open,
// rather than marking a new one.
//
//nolint:gochecknoglobals // a package-level lookup list is the clearest form
var secondaryTriggers = []webhooks.EventType{
	webhooks.PublicIncidentActionCreatedV1,
	webhooks.PublicIncidentActionUpdatedV1,
	webhooks.PublicIncidentFollowUpCreatedV1,
	webhooks.PublicIncidentFollowUpCreatedV2,
	webhooks.PublicIncidentFollowUpUpdatedV1,
	webhooks.PublicIncidentFollowUpUpdatedV2,
	webhooks.PublicIncidentIncidentUpdatedV2,
}

// warnAboutCooldown points out an interaction that is otherwise silent: these
// events arrive while an incident is already open, which is exactly when the
// cooldown is running. Most of them get skipped, and the only trace is a debug
// line — easy to mistake for the trigger not working at all.
func warnAboutCooldown(logger *zap.Logger, triggers []webhooks.EventType, cooldown time.Duration) {
	if cooldown <= 0 {
		return
	}

	for _, t := range triggers {
		if !slices.Contains(secondaryTriggers, t) {
			continue
		}

		logger.Info("a configured trigger fires during an incident that is already open, "+
			"so the cooldown will suppress most of them; lower --cooldown if you want each one investigated",
			zap.String("trigger", string(t)),
			zap.Duration("cooldown", cooldown))
	}
}

// buildVerifier assembles the verifier for inbound webhooks.
func (s *Serve) buildVerifier(logger *zap.Logger) (webhooks.Verifier, error) {
	if s.SkipSignatureVerification {
		// Anyone who can reach the port can now trigger investigations, so this
		// is an error-level line rather than a debug one.
		logger.Error("SIGNATURE VERIFICATION IS DISABLED — any caller that can reach this port " +
			"can trigger investigations. Do not run this way outside local development.")

		return webhooks.InsecureAcceptAll{}, nil
	}

	if len(s.WebhookSecret) == 0 {
		return nil, errors.New(
			"--webhook-secret is required: without it inbound webhooks cannot be verified. " +
				"Use --skip-signature-verification only for local development")
	}

	signer, err := webhooks.NewSigner(s.WebhookSecret...)
	if err != nil {
		return nil, fmt.Errorf("invalid --webhook-secret: %w", err)
	}

	return signer, nil
}

// writeTimeoutHeadroom is the slack left on top of the investigation timeout so
// the response itself has time to be written after the work finishes.
const writeTimeoutHeadroom = 30 * time.Second

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
