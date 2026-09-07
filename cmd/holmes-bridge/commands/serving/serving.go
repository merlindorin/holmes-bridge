// Package serving holds the plumbing every pipeline shares on its way to
// serving traffic: the routers, the lifecycle of the servers around them, where
// human-facing startup output goes, and the two timeouts that have to agree
// across pipelines.
package serving

import (
	"io"
	"os"
	"time"
)

// PreflightTimeout bounds each startup connectivity check. No such failure is
// fatal — the bridge stays up so its probes and metrics keep working, and so a
// dependency that is merely slow to start still gets used.
const PreflightTimeout = 10 * time.Second

// WriteTimeoutHeadroom is the slack left on top of the investigation timeout so
// the response itself has time to be written after the work finishes.
const WriteTimeoutHeadroom = 30 * time.Second

// Banner is where human-facing startup output goes: stderr, so it stays
// separate from anything a caller might be parsing on stdout, and beside the
// structured logs rather than inside them.
func Banner() io.Writer { return os.Stderr }
