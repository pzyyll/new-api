// ABOUTME: Middleware that logs the complete set of incoming request headers.
// ABOUTME: Only active when DEBUG=true to aid request debugging without leaking headers in production.
package middleware

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

func DebugHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.DebugEnabled {
			var sb strings.Builder
			fmt.Fprintf(&sb, "request headers for %s %s:", c.Request.Method, c.Request.URL.String())
			for name, values := range c.Request.Header {
				for _, value := range values {
					fmt.Fprintf(&sb, "\n  %s: %s", name, value)
				}
			}
			logger.LogDebug(c, sb.String())
		}
		c.Next()
	}
}
