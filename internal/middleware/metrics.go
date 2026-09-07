package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/merlindorin/holmes-bridge/internal/metrics"
)

// Metrics records the HTTP golden signals for every request.
func Metrics(m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()

		// FullPath is the route template rather than the concrete path, which
		// keeps incident IDs out of the metric labels.
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		inFlight := metric.WithAttributes(attribute.String("route", route))
		m.HTTPInFlight.Add(c.Request.Context(), 1, inFlight)

		defer func() {
			m.HTTPInFlight.Add(c.Request.Context(), -1, inFlight)

			attrs := metric.WithAttributes(
				attribute.String("route", route),
				attribute.String("method", c.Request.Method),
				attribute.String("status", strconv.Itoa(c.Writer.Status())),
			)

			m.HTTPRequests.Add(c.Request.Context(), 1, attrs)
			m.HTTPDuration.Record(c.Request.Context(), time.Since(started).Seconds(), attrs)
		}()

		c.Next()
	}
}
