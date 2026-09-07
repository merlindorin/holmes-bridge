package serving

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/requestid"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/metrics"
	"github.com/merlindorin/holmes-bridge/internal/middleware"
	"github.com/merlindorin/holmes-bridge/internal/otel"
)

// LocalRouter is what the process serves on its own port: everything, since
// reaching it already means being inside.
func LocalRouter(name string, logger *zap.Logger, m *metrics.Metrics) *gin.Engine {
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

// PublicRouter is what a tunnel serves: no tracing, no /metrics, and named
// "public" in the logs so a request arriving from outside is distinguishable
// from one on the local port. Callers mount the one endpoint they publish.
func PublicRouter(logger *zap.Logger, m *metrics.Metrics) *gin.Engine {
	r := gin.New()
	r.Use(requestid.New())
	r.Use(ginzap.Ginzap(logger.Named("public"), time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(logger.Named("public"), true))
	r.Use(middleware.ErrorHandler(logger))
	r.Use(middleware.Metrics(m))
	r.NoRoute(middleware.NotFound())

	return r
}

// RunMeterProvider keeps the meter provider alive for as long as the process
// runs, and shuts it down cleanly on the way out.
func RunMeterProvider(ctx context.Context, name, version string, logger *zap.Logger) func() error {
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

// WaitForShutdownSignal returns when the process is asked to stop, so the
// errgroup it runs in can tear the rest of the servers down with it.
func WaitForShutdownSignal(ctx context.Context) func() error {
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

// SetGinMode keeps gin's own verbosity in step with the bridge's.
func SetGinMode(development bool) {
	if development {
		gin.SetMode(gin.DebugMode)
		return
	}

	gin.SetMode(gin.ReleaseMode)
}
