package commands

import (
	"testing"

	"github.com/merlindorin/holmes-bridge/internal/infra/ntfy"
)

func TestNtfyServerResolvesTheEffectiveAddress(t *testing.T) {
	t.Parallel()

	// A key present but empty in .env reaches kong as "" rather than as unset,
	// so the flag default never applies. Everything downstream has to agree on
	// where notifications actually go.
	for name, tc := range map[string]struct{ configured, want string }{
		"empty falls back to the default": {"", ntfy.DefaultServer},
		"explicit default":                {ntfy.DefaultServer, ntfy.DefaultServer},
		"self-hosted":                     {"https://ntfy.example.com", "https://ntfy.example.com"},
	} {
		o := &Ntfy{Server: tc.configured}
		if got := o.server(); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
