package fixtures_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/merlindorin/holmes-bridge/internal/infra/fixtures"
)

const scenarioDir = "../../../fixtures/scenarios"

// Fixture data is served by an API that is wire-identical to incident.io, shows
// up in Slack channel names, incident references and LLM prompts, and gets
// screenshotted into documentation. Anything in here that reads like a real
// incident, or contains a real person's details, is a hazard rather than a
// cosmetic problem — hence these guards.

// reservedEmailDomain matches the domains RFC 2606 and RFC 6761 set aside for
// documentation and testing. Addresses on these can never reach a mailbox.
var reservedEmailDomain = regexp.MustCompile(`@([a-z0-9-]+\.)*(test|example|invalid|localhost)$`)

var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// stripComments removes YAML comments so a check reads the data a client would
// be served, not the notes left for whoever edits the file.
func stripComments(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

func loadAll(t *testing.T) map[string]*fixtures.Scenario {
	t.Helper()

	scenarios, err := fixtures.LoadDir(scenarioDir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	return scenarios
}

func TestFixtureEmailsAreUnroutable(t *testing.T) {
	t.Parallel()

	// A real address here would be served by GET /v2/users and could end up in
	// a prompt sent to a third-party model.
	for name, scenario := range loadAll(t) {
		for _, u := range scenario.Users {
			if u.Email == "" {
				continue
			}

			if !reservedEmailDomain.MatchString(strings.ToLower(u.Email)) {
				t.Errorf("%s: user %q has email %q, which is not on a reserved domain.\n"+
					"Use something under .test, .example or .invalid so it cannot reach a real mailbox.",
					name, u.Name, u.Email)
			}
		}
	}
}

func TestNoRealEmailAnywhereInScenarioFiles(t *testing.T) {
	t.Parallel()

	// The struct-level check above only sees fields the schema models. This one
	// reads the raw bytes, so an address hidden in a summary or an update also
	// gets caught.
	paths, err := filepath.Glob(filepath.Join(scenarioDir, "*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	for _, path := range paths {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}

		for _, match := range emailPattern.FindAllString(stripComments(string(body)), -1) {
			if !reservedEmailDomain.MatchString(strings.ToLower(match)) {
				t.Errorf("%s contains the address %q, which is not on a reserved domain",
					filepath.Base(path), match)
			}
		}
	}
}

func TestFixtureIncidentsAreMarkedAsTest(t *testing.T) {
	t.Parallel()

	// A reference reading "INC-201" is indistinguishable from a production one
	// in a Slack message or a log line, and is the kind of thing somebody acts
	// on by mistake.
	for name, scenario := range loadAll(t) {
		for _, in := range scenario.Incidents {
			if in.Reference != "" && !strings.HasPrefix(in.Reference, fixtures.ReferencePrefix) {
				t.Errorf("%s: incident reference %q should start with %q",
					name, in.Reference, fixtures.ReferencePrefix)
			}

			if !strings.Contains(strings.ToUpper(in.Name), "TEST") {
				t.Errorf("%s: incident name %q does not identify itself as test data.\n"+
					"Prefix it with [TEST] — this string is what lands in a Slack channel.",
					name, in.Name)
			}
		}
	}
}

func TestFixturesDoNotLinkToRealIncidentIO(t *testing.T) {
	t.Parallel()

	// Linking mock records to app.incident.io invites someone to click through
	// expecting to find the incident it describes.
	paths, _ := filepath.Glob(filepath.Join(scenarioDir, "*.yaml"))

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		// Values only. A comment explaining why we avoid the real domain is
		// exactly the kind of thing that should not trip this.
		if strings.Contains(stripComments(string(body)), "app.incident.io") {
			t.Errorf("%s links to app.incident.io; point it at the mock instead",
				filepath.Base(path))
		}
	}
}

func TestMintedReferencesAreMarkedAsTest(t *testing.T) {
	t.Parallel()

	// References the mock invents for incidents created through the API have to
	// carry the same marker as the ones in fixtures.
	scenarios := loadAll(t)

	expanded, err := scenarios["checkout-crashloop"].Expand(time.Now())
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	for _, in := range expanded.Incidents {
		if !strings.HasPrefix(in.Reference, fixtures.ReferencePrefix) {
			t.Errorf("expanded incident reference %q should start with %q",
				in.Reference, fixtures.ReferencePrefix)
		}
	}
}
