package probe_test

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap/zaptest"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/ntfy"
)

// TestLivePublish pushes to a real ntfy server. It only runs when
// NTFY_PROBE_TOPIC is set, so the normal suite stays offline.
//
// Set NTFY_PROBE_SERVER and NTFY_PROBE_TOKEN for a self-hosted instance; most
// require credentials to publish.
func TestLivePublish(t *testing.T) {
	topic := os.Getenv("NTFY_PROBE_TOPIC")
	if topic == "" {
		t.Skip("set NTFY_PROBE_TOPIC to publish to a real ntfy server")
	}

	var opts []ntfy.Option
	if token := os.Getenv("NTFY_PROBE_TOKEN"); token != "" {
		opts = append(opts, ntfy.WithToken(token))
	}

	c, err := ntfy.New(os.Getenv("NTFY_PROBE_SERVER"), topic, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ntfy.NewNotifier(c, zaptest.NewLogger(t)).Notify(context.Background(), investigate.Notification{
		IncidentID: "01PROBE", Reference: "TEST-201", Name: "Checkout is completely down",
		Permalink: "https://example.com/i/201",
		Headline:  "The checkout-api deployment cannot start because postgres-primary has no backing pods.",
		ToolCalls: 14,
	})

	t.Logf("published to %s/%s", c.Server(), c.Topic())
}
