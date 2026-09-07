// Package commands titles the sections of --help. Each pipeline and each
// integration lives in its own package beside this one, so a change to how the
// bridge talks to ntfy does not touch the file that knows about incident.io.
package commands

import (
	"github.com/alecthomas/kong"

	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/expose"
	holmescmd "github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/holmes"
	incidentiocmd "github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/incidentio"
	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/investigation"
	"github.com/merlindorin/holmes-bridge/cmd/holmes-bridge/commands/ntfy"
)

// Group keys, re-exported from the package that owns each set of flags so main
// can bind them as kong vars and title the sections of --help. The struct tags
// reference them through ${...} so the key and its title cannot drift apart.
const (
	GroupIncidentIO    = incidentiocmd.Group
	GroupHolmes        = holmescmd.Group
	GroupNtfy          = ntfy.Group
	GroupInvestigation = investigation.Group
	GroupExpose        = expose.Group
	GroupAlertmanager  = ntfy.GroupAlertmanager
)

// HelpGroups titles each section of the flag list, so a reader can find the
// ntfy settings without scanning thirty unrelated flags.
func HelpGroups() kong.Option {
	return kong.ExplicitGroups([]kong.Group{
		{
			Key:         GroupIncidentIO,
			Title:       "incident.io:",
			Description: "Where incidents are read from and analyses written back to.",
		},
		{
			Key:         GroupHolmes,
			Title:       "HolmesGPT:",
			Description: "The server that performs the investigations.",
		},
		{
			Key:         GroupInvestigation,
			Title:       "Investigation:",
			Description: "What to investigate, how hard to try, and what to do with the answer.",
		},
		{
			Key:         GroupNtfy,
			Title:       "Notifications (ntfy):",
			Description: "Push each finished investigation to a phone. Off until a topic is set.",
		},
		{
			Key:         GroupAlertmanager,
			Title:       "Alertmanager:",
			Description: "Receiving alerts directly, with no incident.io in the loop.",
		},
		{
			Key:         GroupExpose,
			Title:       "Exposure (holt):",
			Description: "Publish the webhook endpoint through a reverse tunnel, for incident.io to reach.",
		},
	})
}
