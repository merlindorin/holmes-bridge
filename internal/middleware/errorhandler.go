package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/merlindorin/holmes-bridge/internal/api"
)

// ErrorHandler renders anything a handler reported with c.Error as incident.io's
// error envelope, so a client sees the same body shape it would in production.
func ErrorHandler(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err

		var apiErr *api.Error
		if !errors.As(err, &apiErr) {
			logger.Error("unhandled error in request",
				zap.String("path", c.Request.URL.Path), zap.Error(err))

			apiErr = api.Internal("An unexpected error occurred.")
		}

		// Filled in here because this is the only layer that knows the request.
		apiErr.RequestID = requestid.Get(c)

		c.AbortWithStatusJSON(apiErr.Status, apiErr)
	}
}

// NotFound renders unmatched routes in the same envelope, so a typo'd path
// returns incident.io-shaped JSON rather than gin's plain-text 404.
func NotFound() gin.HandlerFunc {
	return func(c *gin.Context) {
		err := api.New(http.StatusNotFound, api.TypeNotFound, api.Single{
			Code:    "not_found",
			Message: "No route matches " + c.Request.Method + " " + c.Request.URL.Path,
		})
		err.RequestID = requestid.Get(c)

		c.AbortWithStatusJSON(err.Status, err)
	}
}
