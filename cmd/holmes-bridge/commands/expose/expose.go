// Package expose publishes a pipeline's webhook endpoint through a holt reverse
// tunnel, and announces the URL to register with whatever delivers to it.
package expose

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openotters/holt/pkg/expose"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/serving"
)

// Group titles these flags in --help.
const Group = "expose"

// WebhookPath is the route incident.io delivers to. It is also what gets
// appended to the tunnel address, so the logged URL is one you can paste
// straight into incident.io rather than a base you have to complete.
const WebhookPath = "/webhooks/incidentio"

// Expose groups the holt options. holt is a reverse HTTP tunnel: the bridge
// dials out to a hub and serves its handler back down that connection, so
// incident.io can reach a bridge running on a laptop or inside a cluster with
// no inbound path.
//
// Everything here is optional. With a ~/.holt/config.yaml in place, --expose on
// its own is the whole configuration.
type Expose struct {
	Enabled bool `name:"expose" env:"EXPOSE" group:"expose" help:"Publish the webhook endpoint through a holt tunnel and log the URL to register with incident.io"`

	Peer string `name:"expose-peer" env:"EXPOSE_PEER" default:"holmes-bridge" group:"expose" help:"Peer id to enroll as. Doubles as the hostname, and keeping it fixed keeps the webhook URL stable across restarts."`

	Profile string `name:"expose-profile" env:"EXPOSE_PROFILE" group:"expose" help:"Profile from ~/.holt/config.yaml. Empty uses the file's default_profile."`

	ConfigFile string `name:"expose-config" env:"EXPOSE_CONFIG" group:"expose" help:"holt config file. Empty uses ~/.holt/config.yaml."`

	AdminURL string `name:"expose-admin-url" env:"EXPOSE_ADMIN_URL" group:"expose" help:"Enroll against this hub instead of the profile's admin_url."`

	All bool `name:"expose-all" env:"EXPOSE_ALL" group:"expose" help:"Publish every route through the tunnel, including the unauthenticated POST /investigations/{id}. Development only."`
}

// EnrollFor opens the tunnel when --expose is set and announces the URL for the
// given path, returning the peer for the caller to serve. It returns
// (nil, nil) when exposure is off.
//
// The path differs per pipeline — incident.io delivers to one route and
// Alertmanager to another — but everything else about enrolling is the same.
//
// Enrolment is deliberately separate from serving: it happens once, before
// anything is listening, so the URL is part of startup output and its failures
// are not entangled with the servers'.
func (e *Expose) EnrollFor(
	ctx context.Context, logger *zap.Logger, handler http.Handler, path string,
) (*expose.Peer, error) {
	if !e.Enabled {
		return nil, nil //nolint:nilnil // "no tunnel, no error" is the honest result
	}

	peer, err := e.tunnel(ctx, logger, handler)
	if err != nil {
		return nil, err
	}

	url, err := webhookURL(peer, path)
	if err != nil {
		return nil, err
	}

	announce(logger, peer, url, path)

	return peer, nil
}

// tunnel enrolls with the hub and returns the peer, without dialing yet.
//
// Enrolment happens before the HTTP server starts so the webhook URL can be
// logged as part of startup: the whole point of this flag is to learn the
// address you have to paste into incident.io.
func (e *Expose) tunnel(ctx context.Context, logger *zap.Logger, handler http.Handler) (*expose.Peer, error) {
	opts := []expose.Option{
		expose.WithLogger(logger.Named("holt")),
		// A generated id would change on every restart, and with it the
		// hostname — which means re-registering the webhook in incident.io
		// every time the process comes back.
		expose.WithPeerName(e.Peer),
	}

	if e.Profile != "" {
		opts = append(opts, expose.WithProfile(e.Profile))
	}

	if e.ConfigFile != "" {
		opts = append(opts, expose.WithConfigFile(e.ConfigFile))
	}

	if e.AdminURL != "" {
		opts = append(opts, expose.WithAdminURL(e.AdminURL))
	}

	peer, err := expose.New(ctx, handler, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to enroll with the holt hub: %w", err)
	}

	return peer, nil
}

// webhookURL is the address to register with the sender, or an error explaining
// why this tunnel cannot serve one.
func webhookURL(peer *expose.Peer, path string) (string, error) {
	// Header routing means the hub picks the peer from a request header rather
	// than the hostname. incident.io sends its webhooks with a fixed set of
	// headers and offers no way to add one, so such a tunnel can never route a
	// delivery to this bridge. Better to say so at startup than to leave
	// someone wondering why incident.io reports every attempt as failed.
	if peer.RouteHeader != "" {
		return "", fmt.Errorf(
			"this holt hub routes by the %q header, which incident.io cannot send: "+
				"its webhooks carry a fixed set of headers.\n"+
				"Run the hub with subdomain routing (--proxy-routing subdomain or both, "+
				"plus --proxy-domain) so this peer gets a hostname of its own",
			peer.RouteHeader)
	}

	if peer.URL == "" {
		return "", fmt.Errorf(
			"the hub did not report a public address for peer %q. "+
				"It may not be configured with --proxy-domain, or its admin endpoint "+
				"may be unreachable from here", peer.Name)
	}

	return strings.TrimRight(peer.URL, "/") + path, nil
}

// announce prints the URL where it cannot be missed. This is the one piece of
// output the flag exists to produce, and it competes with structured logs.
func announce(logger *zap.Logger, peer *expose.Peer, url, path string) {
	logger.Info("webhook endpoint published through holt",
		zap.String("peer", peer.Name),
		zap.String("hub", peer.TunnelURL),
		zap.String("webhook_url", url))

	if path == WebhookPath {
		fmt.Fprintf(serving.Banner(), `
  ┌─ holt tunnel ────────────────────────────────────────────────
  │
  │  Register this URL in incident.io (Settings → Webhooks):
  │
  │      %s
  │
  │  Subscribe it to:
  │      public_incident.incident_created_v2
  │      public_incident.incident_status_updated_v2
  │
  │  Then copy the signing secret into --webhook-secret.
  │
  └──────────────────────────────────────────────────────────────

`, url)

		return
	}

	fmt.Fprintf(serving.Banner(), `
  ┌─ holt tunnel ────────────────────────────────────────────────
  │
  │  Point Alertmanager at this URL:
  │
  │      %s
  │
  │  receivers:
  │    - name: holmes
  │      webhook_configs:
  │        - url: %s
  │          send_resolved: true
  │          http_config:
  │            authorization:
  │              type: Bearer
  │              credentials: <--alertmanager-token>
  │
  └──────────────────────────────────────────────────────────────

`, url, url)
}
