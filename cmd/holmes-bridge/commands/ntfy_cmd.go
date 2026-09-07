package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/merlindorin/holmes-bridge/internal/globals"
	"github.com/merlindorin/holmes-bridge/internal/infra/ntfy"
)

// NtfyCmd groups the notification commands.
//
// Notifications are the one integration whose failures are invisible during
// normal operation: a push that does not go out is logged and dropped, because
// it must never fail an investigation. These commands make that path testable
// on its own, before a real incident depends on it.
type NtfyCmd struct {
	Serve  NtfyServe  `cmd:"" help:"Receive Alertmanager webhooks, investigate, and push the result to ntfy"`
	Test   NtfyTest   `cmd:"" help:"Send a test notification and report whether it was accepted"`
	Config NtfyConfig `cmd:"" help:"Show the resolved ntfy settings, without publishing anything"`
}

// NtfyTest publishes one notification and says exactly what happened.
type NtfyTest struct {
	Ntfy `embed:""`

	Message string `help:"Body of the test notification" default:"Test notification from holmes-bridge. If you can read this, notifications are working."`

	Failure bool `help:"Send it shaped as a failed investigation, to check the high-priority path"`
}

// Run publishes the test notification.
func (c *NtfyTest) Run(
	ctx context.Context, _ *cmd.Commons, _ *globals.HTTPServer, _ *globals.MetricServer,
) error {
	if c.Topic == "" {
		return errors.New("no topic set: pass --ntfy-topic, or set NTFY_TOPIC in .env")
	}

	client, err := c.Ntfy.client()
	if err != nil {
		return err
	}

	n := ntfy.Notification{
		Topic:    client.Topic(),
		Title:    "TEST-000 · holmes-bridge test",
		Message:  c.Message,
		Priority: ntfy.PriorityDefault,
		Tags:     []string{"mag"},
		Markdown: true,
	}

	if c.Failure {
		n.Title = "TEST-000 · investigation failed (test)"
		n.Priority = ntfy.PriorityHigh
		n.Tags = []string{"rotating_light"}
	}

	publishCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if publishErr := client.Publish(publishCtx, n); publishErr != nil {
		return fmt.Errorf("the notification was not accepted: %w", publishErr)
	}

	fmt.Fprintf(os.Stdout, "Published to %s/%s\nCheck the device subscribed to that topic.\n",
		client.Server(), client.Topic())

	return nil
}

// authNone is how the config command reports that no credentials are set.
const authNone = "none"

// NtfyConfig prints what the settings resolve to, publishing nothing.
type NtfyConfig struct {
	Ntfy `embed:""`
}

// Run reports the resolved configuration.
func (c *NtfyConfig) Run(_ *cmd.Commons, _ *globals.HTTPServer, _ *globals.MetricServer) error {
	if c.Topic == "" {
		fmt.Fprintln(os.Stdout, "Notifications are OFF: no topic set.")
		fmt.Fprintln(os.Stdout, "Set --ntfy-topic, or NTFY_TOPIC in .env, to enable them.")

		return nil
	}

	auth := authNone

	switch {
	case c.Token != "":
		auth = "token"
	case c.User != "":
		auth = "basic (" + c.User + ")"
	}

	fmt.Fprintf(os.Stdout, "server:      %s\n", c.server())
	fmt.Fprintf(os.Stdout, "topic:       %s\n", c.Topic)
	fmt.Fprintf(os.Stdout, "auth:        %s\n", auth)
	fmt.Fprintf(os.Stdout, "on failure:  %t\n", c.OnFailure)

	// The two configurations that reject every push.
	if auth == authNone {
		fmt.Fprintln(os.Stdout,
			"\nwarning: most servers require credentials to publish. Set --ntfy-token.")
	}

	if c.Token != "" && c.server() == ntfy.DefaultServer {
		fmt.Fprintln(os.Stdout,
			"\nwarning: a token is set but the server is the public default. A token only\n"+
				"works against the server that issued it — set --ntfy-server if this token\n"+
				"is for a self-hosted ntfy.")
	}

	return nil
}
