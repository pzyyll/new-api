// ABOUTME: Middleware that logs the complete set of incoming request headers.
// ABOUTME: Only active when DEBUG=true to aid request debugging without leaking headers in production.
package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

func DebugHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.DebugEnabled {
			headers, _ := common.Marshal(c.Request.Header)
			logger.LogDebug(c, "request headers for %s %s: %s", c.Request.Method, c.Request.URL.String(), headers)
		}
		c.Next()
	}
}
