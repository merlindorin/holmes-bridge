package globals

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricServer mounts the Prometheus scrape endpoint on the main router, which
// is where the OTel prometheus exporter publishes.
type MetricServer struct {
	Path string `env:"METRIC_PATH" help:"Path to expose Prometheus metrics on" default:"/metrics"`
}

func (o *MetricServer) Mount(g gin.IRouter) {
	g.GET(o.Path, gin.WrapH(promhttp.Handler()))
}
