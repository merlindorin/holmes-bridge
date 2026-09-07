package expose

import (
	"strings"
	"testing"

	holt "github.com/openotters/holt/pkg/expose"
)

func TestWebhookURLUnderSubdomainRouting(t *testing.T) {
	t.Parallel()

	// The address has to be complete: the whole point of --expose is producing
	// something you paste into incident.io without editing it.
	got, err := webhookURL(&holt.Peer{
		Name: "holmes-bridge",
		URL:  "https://holmes-bridge.example.com/",
	})
	if err != nil {
		t.Fatalf("webhookURL: %v", err)
	}

	if want := "https://holmes-bridge.example.com/webhooks/incidentio"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWebhookURLJoinsWithoutDoubleSlash(t *testing.T) {
	t.Parallel()

	// holt reports the URL with a trailing slash under subdomain routing, and
	// the path starts with one.
	for _, base := range []string{
		"https://holmes-bridge.example.com/",
		"https://holmes-bridge.example.com",
	} {
		got, err := webhookURL(&holt.Peer{Name: "holmes-bridge", URL: base})
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}

		if strings.Contains(strings.TrimPrefix(got, "https://"), "//") {
			t.Errorf("%s produced a doubled slash: %s", base, got)
		}
	}
}

func TestWebhookURLRejectsHeaderRouting(t *testing.T) {
	t.Parallel()

	// incident.io sends a fixed set of headers and offers no way to add one, so
	// a header-routed hub can never deliver to this peer. Failing at startup
	// beats every delivery silently missing its target.
	_, err := webhookURL(&holt.Peer{
		Name:        "holmes-bridge",
		URL:         "https://holt.example.com",
		RouteHeader: "x-tunnel-peer",
	})
	if err == nil {
		t.Fatal("header routing should be rejected")
	}

	for _, want := range []string{"x-tunnel-peer", "incident.io cannot send", "subdomain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
}

func TestWebhookURLRejectsUnknownAddress(t *testing.T) {
	t.Parallel()

	// A hub that could not be asked leaves the URL empty. Reporting that is
	// more useful than publishing a tunnel nobody can address.
	_, err := webhookURL(&holt.Peer{Name: "holmes-bridge"})
	if err == nil {
		t.Fatal("an empty URL should be rejected")
	}

	if !strings.Contains(err.Error(), "holmes-bridge") {
		t.Errorf("the error should name the peer, got: %v", err)
	}
}
