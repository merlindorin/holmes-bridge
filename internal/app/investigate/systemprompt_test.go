package investigate_test

import (
	"strings"
	"testing"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
)

func TestSystemPromptDefaultsWhenEmpty(t *testing.T) {
	t.Parallel()

	if got := investigate.SystemPrompt(""); !strings.Contains(got, "on-call engineer") {
		t.Errorf("an empty template should give the built-in default, got %q", got)
	}
}

func TestSystemPromptRendersTheTemplate(t *testing.T) {
	t.Parallel()

	got := investigate.SystemPrompt("source is {{ .Source }}",
		investigate.SystemPromptData{Source: "chat"})
	if got != "source is chat" {
		t.Errorf("got %q", got)
	}
}

func TestSystemPromptFallsBackOnABadTemplate(t *testing.T) {
	t.Parallel()

	// A broken override should degrade the wording, not take the bridge down
	// in the middle of an incident.
	for _, bad := range []string{"{{ .Source", "{{ .Nope.Missing }}"} {
		got := investigate.SystemPrompt(bad)
		if !strings.Contains(got, "on-call engineer") {
			t.Errorf("%q should fall back to the default, got %q", bad, got)
		}
	}
}
