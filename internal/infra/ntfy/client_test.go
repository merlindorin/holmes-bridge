package ntfy_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"go.uber.org/zap/zaptest"

	"github.com/merlindorin/holmes-bridge/internal/app/investigate"
	"github.com/merlindorin/holmes-bridge/internal/infra/ntfy"
)

// capture records what the ntfy server received.
type capture struct {
	body   map[string]any
	auth   string
	status int
}

func server(t *testing.T, got *capture) *httptest.Server {
	t.Helper()

	if got.status == 0 {
		got.status = http.StatusOK
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		got.auth = r.Header.Get("Authorization")

		w.WriteHeader(got.status)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestPublishSendsTheTopicAndMessage(t *testing.T) {
	t.Parallel()

	got := &capture{}
	srv := server(t, got)

	c, err := ntfy.New(srv.URL, "holmes")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = c.Publish(context.Background(), ntfy.Notification{
		Title: "TEST-201", Message: "pool exhausted", Priority: ntfy.PriorityHigh,
		Tags: []string{"mag"}, Click: "https://example.com/i/201", Markdown: true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// The topic comes from the client, not the caller, so a notification cannot
	// be published to the wrong one by mistake.
	if got.body["topic"] != "holmes" {
		t.Errorf("topic: got %v, want holmes", got.body["topic"])
	}

	for k, want := range map[string]any{
		"title":    "TEST-201",
		"message":  "pool exhausted",
		"priority": float64(ntfy.PriorityHigh),
		"click":    "https://example.com/i/201",
		"markdown": true,
	} {
		if got.body[k] != want {
			t.Errorf("%s: got %v, want %v", k, got.body[k], want)
		}
	}
}

func TestPublishAuthentication(t *testing.T) {
	t.Parallel()

	got := &capture{}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes", ntfy.WithToken("tk_secret"))
	if err := c.Publish(context.Background(), ntfy.Notification{Message: "x"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got.auth != "Bearer tk_secret" {
		t.Errorf("token auth: got %q", got.auth)
	}

	c, _ = ntfy.New(srv.URL, "holmes", ntfy.WithBasicAuth("phil", "hunter2"))
	if err := c.Publish(context.Background(), ntfy.Notification{Message: "x"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Errorf("basic auth: got %q", got.auth)
	}
}

func TestPublishReportsServerErrors(t *testing.T) {
	t.Parallel()

	got := &capture{status: http.StatusForbidden}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes")

	err := c.Publish(context.Background(), ntfy.Notification{Message: "x"})
	if err == nil {
		t.Fatal("a 403 should be reported")
	}

	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error should name the status, got: %v", err)
	}
}

func TestNewRequiresATopic(t *testing.T) {
	t.Parallel()

	if _, err := ntfy.New("https://ntfy.sh", "  "); err == nil {
		t.Fatal("a blank topic should be rejected")
	}
}

func TestNotifierPushesTheSummaryNotTheWholeAnalysis(t *testing.T) {
	t.Parallel()

	got := &capture{}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes")
	n := ntfy.NewNotifier(c, zaptest.NewLogger(t))

	n.Notify(context.Background(), investigate.Notification{
		IncidentID: "01INC", Reference: "TEST-201", Name: "Checkout is down",
		Permalink: "https://example.com/i/201",
		Headline:  "The connection pool is exhausted.",
		Analysis:  "**Summary**\nThe connection pool is exhausted.\n\n**Evidence**\n- a very long list…",
		ToolCalls: 14,
	})

	title, _ := got.body["title"].(string)
	if !strings.Contains(title, "TEST-201") || !strings.Contains(title, "Checkout is down") {
		t.Errorf("title should name the incident, got %q", title)
	}

	message, _ := got.body["message"].(string)
	if !strings.Contains(message, "connection pool is exhausted") {
		t.Errorf("message should carry the summary, got %q", message)
	}

	// The Evidence section belongs on the incident, not on a lock screen.
	if strings.Contains(message, "Evidence") {
		t.Errorf("message should not carry the whole analysis, got %q", message)
	}

	if !strings.Contains(message, "AI-generated") {
		t.Errorf("message should carry the provenance caveat, got %q", message)
	}

	if got.body["click"] != "https://example.com/i/201" {
		t.Errorf("click should open the incident, got %v", got.body["click"])
	}
}

func TestNotifierMarksFailuresUrgent(t *testing.T) {
	t.Parallel()

	got := &capture{}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes")
	n := ntfy.NewNotifier(c, zaptest.NewLogger(t))

	n.Notify(context.Background(), investigate.Notification{
		IncidentID: "01INC", Reference: "TEST-201",
		Headline: "holmesgpt could not complete the investigation",
		Err:      errors.New("holmesgpt could not complete the investigation"),
	})

	if got.body["priority"] != float64(ntfy.PriorityHigh) {
		t.Errorf("a failure should raise the priority, got %v", got.body["priority"])
	}

	title, _ := got.body["title"].(string)
	if !strings.Contains(title, "failed") {
		t.Errorf("the title should say it failed, got %q", title)
	}
}

func TestNotifierCanStaySilentOnFailure(t *testing.T) {
	t.Parallel()

	got := &capture{}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes")
	n := ntfy.NewNotifier(c, zaptest.NewLogger(t), ntfy.WithFailureNotifications(false))

	n.Notify(context.Background(), investigate.Notification{
		IncidentID: "01INC", Err: errors.New("boom"),
	})

	if got.body != nil {
		t.Errorf("nothing should have been published, got %v", got.body)
	}
}

func TestNotifierSwallowsPublishFailures(t *testing.T) {
	t.Parallel()

	// A push that cannot go out must not be able to affect an investigation:
	// the analysis is already on the incident either way.
	c, _ := ntfy.New("http://127.0.0.1:1", "holmes")
	n := ntfy.NewNotifier(c, zaptest.NewLogger(t))

	n.Notify(context.Background(), investigate.Notification{
		IncidentID: "01INC", Reference: "TEST-201", Headline: "something",
	})
}

func TestNotifierDoesNotSplitMultiByteCharacters(t *testing.T) {
	t.Parallel()

	got := &capture{}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes")
	n := ntfy.NewNotifier(c, zaptest.NewLogger(t))

	// An analysis full of multi-byte characters, long enough to be truncated.
	// Byte-slicing here would emit invalid UTF-8 and render as replacement
	// characters on the phone.
	n.Notify(context.Background(), investigate.Notification{
		IncidentID: "01INC",
		Reference:  "TEST-201",
		Headline:   strings.Repeat("café ☕ naïve résumé 🔍 ", 200),
	})

	message, _ := got.body["message"].(string)
	if message == "" {
		t.Fatal("nothing was published")
	}

	if !utf8.ValidString(message) {
		t.Errorf("the truncated message is not valid UTF-8: %q", message[max(0, len(message)-40):])
	}

	if strings.ContainsRune(message, utf8.RuneError) {
		t.Error("the truncated message contains a replacement character")
	}
}

func TestPublishExplainsAnonymousRejection(t *testing.T) {
	t.Parallel()

	// A 401 with no credentials attached reads as "your token is wrong" when the
	// real answer is "you did not send one".
	got := &capture{status: http.StatusUnauthorized}
	srv := server(t, got)

	c, _ := ntfy.New(srv.URL, "holmes")

	err := c.Publish(context.Background(), ntfy.Notification{Message: "x"})
	if err == nil {
		t.Fatal("a 401 should be reported")
	}

	if !strings.Contains(err.Error(), "requires them") {
		t.Errorf("the error should say credentials are required, got: %v", err)
	}

	// With credentials the advice is the opposite.
	authed := &capture{status: http.StatusUnauthorized}
	srv2 := server(t, authed)

	c2, _ := ntfy.New(srv2.URL, "holmes", ntfy.WithToken("tk_bad"))

	err = c2.Publish(context.Background(), ntfy.Notification{Message: "x"})
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("with a token the error should point at the token, got: %v", err)
	}
}
