// Command incidentio-mock serves a wire-compatible subset of the incident.io
// REST API, backed by YAML fixtures and able to emit signed webhooks.
package main

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	c "github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/merlindorin/holmes-bridge/cmd/incidentio-mock/commands"
	"github.com/merlindorin/holmes-bridge/internal/globals"
)

const (
	name        = "incidentio-mock"
	description = "A fake incident.io, for developing and testing against"
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

		Serve: &commands.Serve{},
	}

	ctx := kong.Parse(
		&cli,
		kong.Name(name),
		kong.Description(description),
		kong.UsageOnError(),
		kong.Vars{
			"default_webhook_secret": commands.DefaultWebhookSecret,
			// The mock and the bridge run side by side, so they cannot share a
			// port. Both sit outside the 8080/8081 range because those are the
			// most contested ports on a development machine. Containers are
			// unaffected: the Helm charts set HTTP_PORT explicitly.
			"default_http_port": "18080",
		},
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

	Serve *commands.Serve `cmd:"" default:"withargs" help:"Start the mock incident.io API"`
}
