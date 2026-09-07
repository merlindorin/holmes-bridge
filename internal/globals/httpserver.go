package globals

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// HTTPServer carries the flags every binary in this repo needs to expose an
// HTTP listener, and owns its graceful shutdown.
type HTTPServer struct {
	Host string `env:"HTTP_HOST" help:"Host to bind the server to" default:"0.0.0.0"`
	// The default is supplied per binary through kong.Vars, because the mock and
	// the bridge are meant to run side by side: a shared 8080 default means the
	// second one to start dies on "address already in use".
	//
	// Local defaults are the conventional port prefixed with 1 (18080, 18081),
	// because 8080 and 8081 are the most contested ports on a dev machine.
	Port              int           `env:"HTTP_PORT" help:"Port to bind the server to" default:"${default_http_port}"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" help:"Max duration for reading the entire request" default:"30s"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READHEADER_TIMEOUT" help:"Max duration for reading request headers" default:"5s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" help:"Max duration for writing the response" default:"30s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" help:"Max duration to wait for the next request when keep-alives are enabled" default:"120s"`
	MaxHeaderBytes    int           `env:"HTTP_MAX_HEADER_BYTES" help:"Max number of bytes to read parsing request headers" default:"1048576"`
	GracefulPeriod    time.Duration `env:"HTTP_GRACEFUL_PERIOD" help:"Period to wait for graceful shutdown" default:"5s"`
}

func (srv *HTTPServer) Server(h http.Handler) *http.Server {
	return &http.Server{
		Addr:              srv.Addr(),
		Handler:           h,
		ReadTimeout:       srv.ReadTimeout,
		ReadHeaderTimeout: srv.ReadHeaderTimeout,
		WriteTimeout:      srv.WriteTimeout,
		IdleTimeout:       srv.IdleTimeout,
		MaxHeaderBytes:    srv.MaxHeaderBytes,
	}
}

func (srv *HTTPServer) Addr() string {
	return fmt.Sprintf("%s:%d", srv.Host, srv.Port)
}

// describeListenError turns the stock "bind: address already in use" into
// something that names the port, the flag that changes it, and how to find the
// process holding it. That error otherwise says nothing about which of the two
// services collided, or with what.
func (srv *HTTPServer) describeListenError(err error) error {
	if !errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("failed to listen on %s: %w", srv.Addr(), err)
	}

	return fmt.Errorf(
		"cannot listen on %s: something else is already using port %d.\n"+
			"Find it with:  lsof -nP -iTCP:%d -sTCP:LISTEN\n"+
			"Or pick another port with --http-port (or HTTP_PORT): %w",
		srv.Addr(), srv.Port, srv.Port, err)
}

// Start returns an errgroup-friendly func that serves until ctx is cancelled,
// then drains in-flight requests within GracefulPeriod.
func (srv *HTTPServer) Start(ctx context.Context, l *zap.Logger, router http.Handler) func() error {
	return func() error {
		server := srv.Server(router)

		// Bind before announcing. ListenAndServe would let us log "listening"
		// and only then discover the port is taken, which prints a line that is
		// simply untrue next to the error that follows it.
		// ListenConfig rather than net.Listen, so a cancelled context aborts the
		// bind instead of leaving it to finish into a server nobody will run.
		var lc net.ListenConfig

		listener, err := lc.Listen(ctx, "tcp", srv.Addr())
		if err != nil {
			return srv.describeListenError(err)
		}

		l.Info("HTTP server listening", zap.String("address", listener.Addr().String()))

		errCh := make(chan error, 1)

		go func() {
			if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				errCh <- serveErr
			}

			close(errCh)
		}()

		select {
		case serveErr := <-errCh:
			return serveErr
		case <-ctx.Done():
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), srv.GracefulPeriod)
		defer cancel()

		l.Info("Shutting down HTTP server...")

		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			l.Error("failed to shutdown HTTP server", zap.Error(shutdownErr))
			return shutdownErr
		}

		return nil
	}
}
