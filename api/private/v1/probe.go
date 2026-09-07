// Package v1 implements the operational probes both binaries serve.
package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/api/private"
)

// ReadinessCheck reports whether one dependency is usable. Returning an error
// fails the readiness probe and names the dependency in the response.
type ReadinessCheck func() error

// Server implements the generated private server interface.
type Server struct {
	logger *zap.Logger
	checks map[string]ReadinessCheck
}

var _ private.ServerInterface = (*Server)(nil)

// NewServer builds the probe handlers. Readiness checks are optional: with none
// registered, readiness tracks liveness.
func NewServer(logger *zap.Logger, checks map[string]ReadinessCheck) *Server {
	return &Server{logger: logger.Named("probe"), checks: checks}
}

// GetLiveness reports that the process is up. It deliberately checks nothing
// else: a liveness probe that fails on a dependency outage gets the container
// killed for something restarting cannot fix.
func (s *Server) GetLiveness(c *gin.Context) {
	c.JSON(http.StatusOK, private.ProbeResponse{Status: "ok"})
}

// GetReadiness reports whether the process can serve traffic.
func (s *Server) GetReadiness(c *gin.Context) {
	details := map[string]string{}

	for name, check := range s.checks {
		if err := check(); err != nil {
			details[name] = err.Error()
		}
	}

	if len(details) > 0 {
		s.logger.Warn("readiness check failed", zap.Any("details", details))
		c.JSON(http.StatusServiceUnavailable, private.ProbeResponse{
			Status: "unavailable", Details: &details,
		})

		return
	}

	c.JSON(http.StatusOK, private.ProbeResponse{Status: "ok"})
}
