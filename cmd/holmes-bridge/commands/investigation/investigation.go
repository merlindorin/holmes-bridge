// Package investigation carries the settings that decide what gets
// investigated, how hard the bridge tries, and what happens to the answer.
package investigation

import (
	"fmt"
	"time"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
)

// Group titles these flags in --help.
const Group = "investigation"

// Investigation is how the bridge decides what to investigate, how hard to try,
// and what to do with the answer.
type Investigation struct {
	WriteBack string `name:"write-back" env:"WRITE_BACK" group:"investigation" help:"Where to record an analysis: update, timeline, or none. Start with none against a real org." default:"update"`

	MinSeverityRank int64 `name:"min-severity-rank" env:"MIN_SEVERITY_RANK" group:"investigation" help:"Skip incidents whose severity ranks below this. 0 investigates everything."`

	MaxConcurrent int `name:"max-concurrent" env:"MAX_CONCURRENT" group:"investigation" help:"Simultaneous investigations. Bounds model spend and load on the cluster being inspected, not HolmesGPT, which parallelises fine." default:"2"`

	QueueTimeout time.Duration `name:"queue-timeout" env:"QUEUE_TIMEOUT" group:"investigation" help:"How long an investigation waits for a free slot before giving up. Stops a storm building a backlog of analyses nobody will read." default:"5m"`

	Cooldown time.Duration `name:"cooldown" env:"COOLDOWN" group:"investigation" help:"Leave an incident alone for this long after investigating it. Guards against re-triggering on the bridge's own write-back." default:"30m"`

	Attempts int `name:"attempts" env:"ATTEMPTS" group:"investigation" help:"Attempts per investigation before giving up" default:"2"`

	SystemPrompt string `name:"system-prompt" env:"SYSTEM_PROMPT" group:"investigation" help:"Go text/template for the system prompt that shapes every answer. Empty uses the built-in default. Receives .Source, which is \"incident\" or \"chat\"."`

	Timeout time.Duration `name:"investigation-timeout" env:"INVESTIGATION_TIMEOUT" group:"investigation" help:"Bound on a single investigation" default:"10m"`
}

// Config converts the flags into the service's configuration.
func (o *Investigation) Config() (investigate.Config, error) {
	writeBack := investigate.WriteBack(o.WriteBack)
	if !writeBack.Valid() {
		return investigate.Config{}, fmt.Errorf(
			"--write-back must be one of update, timeline or none (got %q)", o.WriteBack)
	}

	return investigate.Config{
		SystemPrompt:    o.SystemPrompt,
		WriteBack:       writeBack,
		MaxConcurrent:   o.MaxConcurrent,
		MinSeverityRank: o.MinSeverityRank,
		Attempts:        o.Attempts,
		Timeout:         o.Timeout,
		Cooldown:        o.Cooldown,
		QueueTimeout:    o.QueueTimeout,
	}, nil
}
