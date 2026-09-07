package webhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// incident.io signs webhooks with the Standard Webhooks scheme (as implemented
// by Svix): the signed content is "<id>.<timestamp>.<body>", the MAC is
// HMAC-SHA256, and the header carries a space-separated list of "v1,<base64>"
// candidates so a secret can be rotated without dropping deliveries.
//
// The HMAC key is the base64 *decoding* of the secret's body, not its literal
// bytes. Getting that wrong still round-trips against itself, so it is only
// caught by the shared test vector in signature_test.go — which is why that
// vector is pinned.
const (
	// HeaderID carries the unique delivery identifier.
	HeaderID = "webhook-id"
	// HeaderTimestamp carries the Unix delivery timestamp, in seconds.
	HeaderTimestamp = "webhook-timestamp"
	// HeaderSignature carries the versioned signature list.
	HeaderSignature = "webhook-signature"

	// SecretPrefix is the prefix incident.io puts on the base64 secret.
	SecretPrefix = "whsec_"

	// signatureVersion is the only scheme version defined today.
	signatureVersion = "v1"

	// DefaultTolerance bounds how far a delivery timestamp may drift before
	// it is rejected as a replay.
	DefaultTolerance = 5 * time.Minute
)

var (
	// ErrMissingEventType is returned for an envelope with no event_type.
	ErrMissingEventType = errors.New("webhook envelope is missing event_type")
	// ErrMissingHeaders is returned when a signature header is absent.
	ErrMissingHeaders = errors.New("webhook is missing signature headers")
	// ErrInvalidTimestamp is returned when webhook-timestamp is unparseable.
	ErrInvalidTimestamp = errors.New("webhook timestamp is not a unix timestamp")
	// ErrTimestampOutOfTolerance is returned when a delivery is too old or too
	// far in the future to be genuine.
	ErrTimestampOutOfTolerance = errors.New("webhook timestamp is outside tolerance")
	// ErrNoMatchingSignature is returned when no candidate signature verifies.
	ErrNoMatchingSignature = errors.New("no webhook signature matched")
	// ErrEmptySecret is returned when a signer is built without a secret.
	ErrEmptySecret = errors.New("webhook signing secret is empty")
	// ErrMalformedSecret is returned when a secret is not valid base64.
	ErrMalformedSecret = errors.New("webhook signing secret is not valid base64")
)

// Signer produces and verifies incident.io webhook signatures.
//
// It holds every secret it was given: Sign always uses the first, while Verify
// accepts any of them, which is what makes secret rotation non-disruptive.
type Signer struct {
	secrets   [][]byte
	tolerance time.Duration
}

// NewSigner builds a Signer from one or more "whsec_"-prefixed secrets, as
// shown in the incident.io dashboard.
//
// The prefix is stripped if present and the remainder is base64-decoded to key
// the HMAC. Decoding is strict: a secret that is not valid base64 is rejected
// rather than silently keyed on its literal bytes, because that fallback would
// produce signatures that verify locally and nowhere else.
func NewSigner(secrets ...string) (*Signer, error) {
	keys := make([][]byte, 0, len(secrets))

	for _, secret := range secrets {
		if secret == "" {
			continue
		}

		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, SecretPrefix))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedSecret, err)
		}

		keys = append(keys, key)
	}

	if len(keys) == 0 {
		return nil, ErrEmptySecret
	}

	return &Signer{secrets: keys, tolerance: DefaultTolerance}, nil
}

// GenerateSecret returns a new random signing secret in incident.io's display
// form, for seeding local configuration.
func GenerateSecret() (string, error) {
	key := make([]byte, 24)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("failed to generate webhook secret: %w", err)
	}

	return SecretPrefix + base64.StdEncoding.EncodeToString(key), nil
}

// WithTolerance returns a copy of the signer that accepts a different amount of
// clock drift. A non-positive tolerance disables the timestamp check, which is
// useful when replaying recorded fixtures.
func (s *Signer) WithTolerance(d time.Duration) *Signer {
	clone := *s
	clone.tolerance = d

	return &clone
}

// signedContent is the exact byte sequence the MAC is taken over.
func signedContent(id string, ts int64, body []byte) []byte {
	prefix := id + "." + strconv.FormatInt(ts, 10) + "."

	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	out = append(out, body...)

	return out
}

func mac(key []byte, content []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(content)

	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Sign returns the value for the webhook-signature header covering this
// delivery, using the signer's primary secret.
func (s *Signer) Sign(id string, ts time.Time, body []byte) string {
	return signatureVersion + "," + mac(s.secrets[0], signedContent(id, ts.Unix(), body))
}

// Verify checks the headers and body of an inbound delivery.
//
// It returns nil only when the timestamp is within tolerance and at least one
// candidate signature matches one of the configured secrets. Comparison is
// constant-time.
func (s *Signer) Verify(id, timestamp, signature string, body []byte) error {
	if id == "" || timestamp == "" || signature == "" {
		return ErrMissingHeaders
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidTimestamp, timestamp)
	}

	if s.tolerance > 0 {
		if drift := time.Since(time.Unix(ts, 0)); drift > s.tolerance || drift < -s.tolerance {
			return fmt.Errorf("%w: drifted by %s", ErrTimestampOutOfTolerance, drift.Round(time.Second))
		}
	}

	content := signedContent(id, ts, body)

	// The header is a space-separated list of "<version>,<base64>" candidates.
	// Every one is checked against every secret, and no early return is taken
	// on a version mismatch, so timing does not leak which candidate matched.
	matched := false

	for _, candidate := range strings.Split(signature, " ") {
		version, digest, found := strings.Cut(candidate, ",")
		if !found || version != signatureVersion {
			continue
		}

		for _, key := range s.secrets {
			if hmac.Equal([]byte(digest), []byte(mac(key, content))) {
				matched = true
			}
		}
	}

	if !matched {
		return ErrNoMatchingSignature
	}

	return nil
}

// Verifier checks an inbound delivery. Signer is the real implementation;
// InsecureAcceptAll exists so local development can bypass it explicitly rather
// than by configuring a secret that silently matches nothing.
type Verifier interface {
	Verify(id, timestamp, signature string, body []byte) error
}

// InsecureAcceptAll accepts every delivery without checking anything.
//
// It exists only for local development against the mock. Anything reachable by
// an untrusted caller must use a Signer: with this in place, any caller who can
// reach the port can make the bridge act.
type InsecureAcceptAll struct{}

// Verify always succeeds.
func (InsecureAcceptAll) Verify(string, string, string, []byte) error { return nil }

var (
	_ Verifier = (*Signer)(nil)
	_ Verifier = InsecureAcceptAll{}
)
