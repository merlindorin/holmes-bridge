package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/merlindorin/holmes-bridge/internal/middleware"
)

func router(keys ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.RequireAPIKey(keys...))
	r.GET("/v1/identity", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	return r
}

func call(r *gin.Engine, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/identity", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w
}

func TestRequireAPIKeyDisabledWithoutKeys(t *testing.T) {
	t.Parallel()

	// The default mock is open, so a developer can curl it without ceremony.
	if got := call(router(), "").Code; got != http.StatusOK {
		t.Errorf("no configured keys should allow anonymous access, got %d", got)
	}
}

func TestRequireAPIKeyAcceptsAConfiguredKey(t *testing.T) {
	t.Parallel()

	if got := call(router("inc_test_key"), "Bearer inc_test_key").Code; got != http.StatusOK {
		t.Errorf("a valid key should be accepted, got %d", got)
	}

	// incident.io's own clients vary the header casing.
	if got := call(router("inc_test_key"), "bearer inc_test_key").Code; got != http.StatusOK {
		t.Errorf("the scheme should be case-insensitive, got %d", got)
	}
}

func TestRequireAPIKeyRejectsBadCredentials(t *testing.T) {
	t.Parallel()

	for name, auth := range map[string]string{
		"missing header": "",
		"wrong key":      "Bearer nope",
		"no scheme":      "inc_test_key",
		"wrong scheme":   "Token inc_test_key",
		"empty token":    "Bearer ",
	} {
		if got := call(router("inc_test_key"), auth).Code; got != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d", name, got)
		}
	}
}

func TestRequireAPIKeyAcceptsAnyOfSeveral(t *testing.T) {
	t.Parallel()

	r := router("key-one", "key-two")

	for _, key := range []string{"key-one", "key-two"} {
		if got := call(r, "Bearer "+key).Code; got != http.StatusOK {
			t.Errorf("%s should be accepted, got %d", key, got)
		}
	}
}
