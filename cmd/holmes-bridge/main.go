// Command holmes-bridge connects incident.io to HolmesGPT: it receives
// incident.io webhooks, asks HolmesGPT to investigate, and writes the analysis
// back onto the incident.
package main

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	c "github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands"
	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/globals"
)

const (
	name        = "holmes-bridge"
	description = "Investigate incident.io incidents with HolmesGPT"
)

//nolint:gochecknoglobals // these globals exist to be overridden at build time
var (
	license string

	version     = "dev"
	commit      = "dirty"
	date        = "latest"
	buildSource = "source"
)

func main() {
	cli := CMD{
		Commons: &c.Commons{
			Version: c.NewVersion(name, version, commit, buildSource, date),
			Licence: c.NewLicence(license),
		},
		HTTPServer:   &globals.HTTPServer{},
		MetricServer: &globals.MetricServer{},

		Serve:       &incidentio.Serve{},
		Investigate: &incidentio.Investigate{},
		Identity:    &incidentio.Identity{},
		Incidents:   &incidentio.Incidents{},
		Show:        &incidentio.Show{},
	}

	ctx := kong.Parse(
		&cli,
		kong.Name(name),
		kong.Description(description),
		kong.UsageOnError(),
		// The mock and the bridge run side by side, so they cannot share a
		// port. Both sit outside the 8080/8081 range because those are the
		// most contested ports on a development machine. Containers are
		// unaffected: the Helm charts set HTTP_PORT explicitly.
		kong.Vars{"default_http_port": "18081"},
		// Titles the sections of --help, so a group of settings can be found
		// without scanning thirty unrelated flags.
		commands.HelpGroups(),
		kong.Configuration(
			kongyaml.Loader,
			fmt.Sprintf("/etc/%s/config.yaml", name),
			fmt.Sprintf("~/.config/%s/config.yaml", name),
		),
	)

	ctx.BindTo(context.Background(), (*context.Context)(nil))
	ctx.FatalIfErrorf(ctx.Run(cli.Commons, cli.HTTPServer, cli.MetricServer))
}

// CMD is the command tree kong parses into.
type CMD struct {
	*c.Commons
	*globals.HTTPServer   `embed:"" prefix:"http-"`
	*globals.MetricServer `embed:"" prefix:"otel-"`

	Serve       *incidentio.Serve       `cmd:"" default:"withargs" help:"Receive incident.io webhooks and investigate"`
	Investigate *incidentio.Investigate `cmd:"" help:"Investigate one incident now and print the result"`

	// Checking incident.io on its own, rather than inferring its health from a
	// failed investigation.
	Identity  *incidentio.Identity  `cmd:"" help:"Check the API key and show what it can do"`
	Incidents *incidentio.Incidents `cmd:"" help:"List incidents"`
	Show      *incidentio.Show      `cmd:"" help:"Show one incident, with the alerts and updates the bridge would read"`
}
