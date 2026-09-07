package webhooks_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/merlindorin/holmes-bridge/internal/domain/webhooks"
)

const testSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

func mustSigner(t *testing.T, secrets ...string) *webhooks.Signer {
	t.Helper()

	s, err := webhooks.NewSigner(secrets...)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	return s
}

func TestSignerRoundTrip(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	now := time.Now()
	body := []byte(`{"event_type":"public_incident.incident_created_v2"}`)

	sig := s.Sign("msg_01ABC", now, body)

	if err := s.Verify("msg_01ABC", unix(now.Unix()), sig, body); err != nil {
		t.Fatalf("Verify rejected a signature it produced: %v", err)
	}
}

func TestSignerRejectsTamperedBody(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	now := time.Now()
	sig := s.Sign("msg_01ABC", now, []byte(`{"a":1}`))

	err := s.Verify("msg_01ABC", unix(now.Unix()), sig, []byte(`{"a":2}`))
	if !errors.Is(err, webhooks.ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature for a tampered body, got %v", err)
	}
}

func TestSignerRejectsTamperedID(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	now := time.Now()
	body := []byte(`{"a":1}`)
	sig := s.Sign("msg_01ABC", now, body)

	err := s.Verify("msg_01XYZ", unix(now.Unix()), sig, body)
	if !errors.Is(err, webhooks.ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature for a swapped id, got %v", err)
	}
}

func TestSignerRejectsWrongSecret(t *testing.T) {
	t.Parallel()

	signer := mustSigner(t, testSecret)
	other := mustSigner(t, "whsec_Y29tcGxldGVseS1kaWZmZXJlbnQta2V5")

	now := time.Now()
	body := []byte(`{"a":1}`)

	err := other.Verify("msg_01ABC", unix(now.Unix()), signer.Sign("msg_01ABC", now, body), body)
	if !errors.Is(err, webhooks.ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature for a foreign secret, got %v", err)
	}
}

func TestSignerAcceptsRotatedSecret(t *testing.T) {
	t.Parallel()

	old := mustSigner(t, "whsec_dGhlLW91dGdvaW5nLXNlY3JldC1rZXkx")
	// A receiver mid-rotation trusts both the new and the outgoing secret.
	rotating := mustSigner(t, "whsec_dGhlLWluY29taW5nLXNlY3JldC1rZXkx", "whsec_dGhlLW91dGdvaW5nLXNlY3JldC1rZXkx")

	now := time.Now()
	body := []byte(`{"a":1}`)

	if err := rotating.Verify("msg_01ABC", unix(now.Unix()), old.Sign("msg_01ABC", now, body), body); err != nil {
		t.Fatalf("rotation should keep accepting the old secret: %v", err)
	}
}

func TestSignerRejectsStaleTimestamp(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	stale := time.Now().Add(-30 * time.Minute)
	body := []byte(`{"a":1}`)

	err := s.Verify("msg_01ABC", unix(stale.Unix()), s.Sign("msg_01ABC", stale, body), body)
	if !errors.Is(err, webhooks.ErrTimestampOutOfTolerance) {
		t.Fatalf("want ErrTimestampOutOfTolerance for a replayed delivery, got %v", err)
	}
}

func TestSignerRejectsFutureTimestamp(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	future := time.Now().Add(30 * time.Minute)
	body := []byte(`{"a":1}`)

	err := s.Verify("msg_01ABC", unix(future.Unix()), s.Sign("msg_01ABC", future, body), body)
	if !errors.Is(err, webhooks.ErrTimestampOutOfTolerance) {
		t.Fatalf("want ErrTimestampOutOfTolerance for a future delivery, got %v", err)
	}
}

func TestSignerToleranceDisabled(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret).WithTolerance(0)
	stale := time.Now().Add(-72 * time.Hour)
	body := []byte(`{"a":1}`)

	if err := s.Verify("msg_01ABC", unix(stale.Unix()), s.Sign("msg_01ABC", stale, body), body); err != nil {
		t.Fatalf("a zero tolerance should replay old fixtures: %v", err)
	}
}

func TestSignerAcceptsMultiCandidateHeader(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	now := time.Now()
	body := []byte(`{"a":1}`)

	// Senders mid-rotation emit several candidates in one header.
	header := "v1,bm90LXRoZS1yaWdodC1vbmU= " + s.Sign("msg_01ABC", now, body)

	if err := s.Verify("msg_01ABC", unix(now.Unix()), header, body); err != nil {
		t.Fatalf("a matching candidate anywhere in the list should verify: %v", err)
	}
}

func TestSignerRejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)
	now := time.Now()
	body := []byte(`{"a":1}`)

	// Same digest, but advertised under a scheme version we do not implement.
	header := "v2," + s.Sign("msg_01ABC", now, body)[len("v1,"):]

	err := s.Verify("msg_01ABC", unix(now.Unix()), header, body)
	if !errors.Is(err, webhooks.ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature for an unknown version, got %v", err)
	}
}

func TestSignerRejectsMissingHeaders(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)

	for name, tc := range map[string]struct{ id, ts, sig string }{
		"no id":        {"", "1", "v1,x"},
		"no timestamp": {"msg_01ABC", "", "v1,x"},
		"no signature": {"msg_01ABC", "1", ""},
	} {
		if err := s.Verify(tc.id, tc.ts, tc.sig, nil); !errors.Is(err, webhooks.ErrMissingHeaders) {
			t.Errorf("%s: want ErrMissingHeaders, got %v", name, err)
		}
	}
}

func TestSignerRejectsGarbageTimestamp(t *testing.T) {
	t.Parallel()

	s := mustSigner(t, testSecret)

	err := s.Verify("msg_01ABC", "not-a-number", "v1,x", nil)
	if !errors.Is(err, webhooks.ErrInvalidTimestamp) {
		t.Fatalf("want ErrInvalidTimestamp, got %v", err)
	}
}

func TestNewSignerRequiresASecret(t *testing.T) {
	t.Parallel()

	if _, err := webhooks.NewSigner(); !errors.Is(err, webhooks.ErrEmptySecret) {
		t.Fatalf("want ErrEmptySecret with no secrets, got %v", err)
	}

	if _, err := webhooks.NewSigner("", ""); !errors.Is(err, webhooks.ErrEmptySecret) {
		t.Fatalf("want ErrEmptySecret with only blank secrets, got %v", err)
	}
}

func TestSignerIgnoresSecretPrefix(t *testing.T) {
	t.Parallel()

	// The same secret with and without the displayed prefix must key the same
	// HMAC, so a caller can paste either form.
	prefixed := mustSigner(t, testSecret)
	bare := mustSigner(t, strings.TrimPrefix(testSecret, webhooks.SecretPrefix))

	now := time.Now()
	body := []byte(`{"a":1}`)

	if err := bare.Verify("msg_01ABC", unix(now.Unix()), prefixed.Sign("msg_01ABC", now, body), body); err != nil {
		t.Fatalf("prefix should be stripped: %v", err)
	}
}

// unix renders a Unix second count the way the webhook-timestamp header carries it.
func unix(sec int64) string {
	return strconv.FormatInt(sec, 10)
}

// TestSignerMatchesStandardWebhooksVector pins the signature scheme to the
// canonical Standard Webhooks test vector, which incident.io's own docs quote.
//
// Every other test here would still pass if the HMAC were keyed on the secret's
// literal bytes instead of its base64 decoding, because signing and verifying
// would agree with each other. Only this vector catches that, and getting it
// wrong means the mock's deliveries are rejected by every real verifier.
func TestSignerMatchesStandardWebhooksVector(t *testing.T) {
	t.Parallel()

	const (
		secret   = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
		id       = "msg_p5jXN8AQM9LWM0D4loKWxJek"
		unixSec  = int64(1614265330)
		body     = `{"test": 2432232314}`
		expected = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
	)

	s := mustSigner(t, secret)

	if got := s.Sign(id, time.Unix(unixSec, 0), []byte(body)); got != expected {
		t.Fatalf("signature drifted from the Standard Webhooks vector:\n got %s\nwant %s", got, expected)
	}

	// The tolerance window is disabled because the vector's timestamp is fixed
	// in 2021 and would otherwise read as a replay.
	if err := s.WithTolerance(0).Verify(id, unix(unixSec), expected, []byte(body)); err != nil {
		t.Fatalf("Verify rejected the canonical vector: %v", err)
	}
}

func TestNewSignerRejectsMalformedSecret(t *testing.T) {
	t.Parallel()

	// "not base64!" would previously have been accepted and keyed on its
	// literal bytes, silently producing signatures nothing else can verify.
	if _, err := webhooks.NewSigner("whsec_not base64!"); !errors.Is(err, webhooks.ErrMalformedSecret) {
		t.Fatalf("want ErrMalformedSecret, got %v", err)
	}
}

func TestGenerateSecretIsUsable(t *testing.T) {
	t.Parallel()

	secret, err := webhooks.GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}

	if !strings.HasPrefix(secret, webhooks.SecretPrefix) {
		t.Errorf("generated secret %q should carry the %q prefix", secret, webhooks.SecretPrefix)
	}

	s := mustSigner(t, secret)
	now := time.Now()
	body := []byte(`{"a":1}`)

	if verifyErr := s.Verify("msg_01ABC", unix(now.Unix()), s.Sign("msg_01ABC", now, body), body); verifyErr != nil {
		t.Fatalf("a generated secret should round-trip: %v", verifyErr)
	}
}
