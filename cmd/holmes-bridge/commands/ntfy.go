package commands

import (
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/ntfy"
)

// groupNtfy titles these flags in --help.
const groupNtfy = "ntfy"

// Ntfy pushes each finished investigation to a phone, so a responder sees what
// HolmesGPT found without watching logs.
//
// Notifications are off until a topic is set.
type Ntfy struct {
	Topic string `name:"ntfy-topic" env:"NTFY_TOPIC" group:"ntfy" help:"Topic to publish to. Empty disables notifications. On public ntfy.sh the topic IS the credential, so use something long and random."`

	Server string `name:"ntfy-server" env:"NTFY_SERVER" group:"ntfy" help:"ntfy server" default:"https://ntfy.sh"`

	Token string `name:"ntfy-token" env:"NTFY_TOKEN" group:"ntfy" help:"Access token (tk_...)"`

	User string `name:"ntfy-user" env:"NTFY_USER" group:"ntfy" help:"Username, for servers using basic auth"`

	Password string `name:"ntfy-password" env:"NTFY_PASSWORD" group:"ntfy" help:"Password, for servers using basic auth"`

	OnFailure bool `name:"ntfy-on-failure" env:"NTFY_ON_FAILURE" group:"ntfy" help:"Also push when an investigation fails" default:"true"`
}

// server is the address actually used.
//
// A key present but empty in .env reaches kong as "" rather than as unset, so
// the flag default never applies. Resolving it here means the client and the
// startup checks agree on where notifications are going — otherwise the
// "pointed at the public default" warning silently misses the case where the
// server was left blank, which is exactly how .env ships.
func (o *Ntfy) server() string {
	if o.Server == "" {
		return ntfy.DefaultServer
	}

	return o.Server
}

// client builds the raw publisher.
//
// Notify swallows publish errors on purpose — a push must never fail an
// investigation — so a command that exists to *diagnose* notifications needs
// the client directly in order to report what went wrong.
func (o *Ntfy) client() (*ntfy.Client, error) {
	opts := []ntfy.Option{}

	switch {
	case o.Token != "":
		opts = append(opts, ntfy.WithToken(o.Token))
	case o.User != "":
		opts = append(opts, ntfy.WithBasicAuth(o.User, o.Password))
	}

	return ntfy.New(o.server(), o.Topic, opts...)
}

// notifier builds the publisher, or nil when no topic is configured.
//
// The nil is returned as the interface type deliberately: a typed nil would
// satisfy investigate.Notifier and then panic on the first push.
func (o *Ntfy) notifier(logger *zap.Logger) (investigate.Notifier, error) {
	if o.Topic == "" {
		return nil, nil //nolint:nilnil // "no notifier, no error" is the honest result
	}

	opts := []ntfy.Option{}

	switch {
	case o.Token != "":
		opts = append(opts, ntfy.WithToken(o.Token))
	case o.User != "":
		opts = append(opts, ntfy.WithBasicAuth(o.User, o.Password))
	}

	client, err := ntfy.New(o.server(), o.Topic, opts...)
	if err != nil {
		return nil, err
	}

	// Two misconfigurations reject every push, and both only show up as a
	// warning after the first investigation finishes — long after the mistake.
	switch {
	case o.Token == "" && o.User == "":
		// Self-hosted servers generally require credentials to publish.
		logger.Warn("no ntfy credentials set — most servers require them to publish. "+
			"If yours does, every notification will be rejected. Set --ntfy-token (NTFY_TOKEN).",
			zap.String("server", client.Server()),
			zap.String("topic", client.Topic()))

	case o.Token != "" && o.server() == ntfy.DefaultServer:
		// A token is issued by one server and meaningless to another. Sending a
		// self-hosted token to ntfy.sh is rejected with a 401 that reads like a
		// bad token rather than the wrong address.
		logger.Warn("an ntfy token is set but the server is still the public default — "+
			"a token only works against the server that issued it. "+
			"Set --ntfy-server (NTFY_SERVER) if this token is for a self-hosted ntfy.",
			zap.String("server", client.Server()))
	}

	logger.Info("pushing investigations to ntfy",
		zap.String("server", client.Server()),
		zap.String("topic", client.Topic()),
		zap.Bool("authenticated", o.Token != "" || o.User != ""),
		zap.Bool("on_failure", o.OnFailure))

	return ntfy.NewNotifier(client, logger, ntfy.WithFailureNotifications(o.OnFailure)), nil
}
