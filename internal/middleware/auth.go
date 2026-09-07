package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"

	"github.com/merlindorin/holmes-bridge/internal/api"
)

// RequireAPIKey enforces incident.io's bearer-token auth.
//
// Passing no keys disables the check, which is the convenient default for local
// use. It is deliberately opt-in the other way round — a mock that demanded a
// key before it would answer anything would mostly get in the way — but a key
// can be set when a test needs to prove the client sends one.
func RequireAPIKey(keys ...string) gin.HandlerFunc {
	accepted := make([][]byte, 0, len(keys))

	for _, k := range keys {
		if k != "" {
			accepted = append(accepted, []byte(k))
		}
	}

	return func(c *gin.Context) {
		if len(accepted) == 0 {
			c.Next()
			return
		}

		token, ok := bearer(c.GetHeader("Authorization"))
		if !ok {
			reject(c, "Expected an Authorization header of the form 'Bearer <api-key>'.")
			return
		}

		for _, key := range accepted {
			if subtle.ConstantTimeCompare([]byte(token), key) == 1 {
				c.Next()
				return
			}
		}

		reject(c, "The API key provided is not valid for this organisation.")
	}
}

func bearer(header string) (string, bool) {
	const prefix = "Bearer "

	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])

	return token, token != ""
}

func reject(c *gin.Context, message string) {
	err := api.New(http.StatusUnauthorized, api.TypeAuthentication, api.Single{
		Code:    "authentication_error",
		Message: message,
	})
	err.RequestID = requestid.Get(c)

	c.AbortWithStatusJSON(err.Status, err)
}
