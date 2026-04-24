// ABOUTME: Middleware that logs raw incoming HTTP request headers and body.
// ABOUTME: Gated by the DEBUG environment variable for use during development and troubleshooting.
package middleware

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

const maxDebugBodyLogBytes = 1000 * 1024 // 1MB

// DebugRequestLog logs the raw HTTP request method, URL, headers, and body
// when DEBUG mode is enabled. The body is truncated to 1MB to avoid flooding logs.
func DebugRequestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.DebugEnabled {
			c.Next()
			return
		}

		ctx := c.Request.Context()

		// Log method and URL
		logger.LogDebug(ctx, ">>> Incoming Request: %s %s", c.Request.Method, c.Request.RequestURI)

		// Log headers
		logger.LogDebug(ctx, ">>> Headers:\n%s", formatHeaders(c.Request.Header))

		// Log body for non-GET requests
		if c.Request.Method != http.MethodGet && c.Request.Body != nil {
			storage, err := common.GetBodyStorage(c)
			if err != nil {
				logger.LogDebug(ctx, ">>> Body: [failed to read: %v]", err)
			} else {
				bodyBytes, err := storage.Bytes()
				if err != nil {
					logger.LogDebug(ctx, ">>> Body: [failed to read bytes: %v]", err)
				} else {
					bodyStr := string(bodyBytes)
					if len(bodyStr) > maxDebugBodyLogBytes {
						bodyStr = bodyStr[:maxDebugBodyLogBytes] + fmt.Sprintf("... [truncated, total %d bytes]", len(bodyBytes))
					}
					logger.LogDebug(ctx, ">>> Body (%d bytes): %s", len(bodyBytes), bodyStr)
				}
				// Reset body position for downstream handlers
				if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
					logger.LogDebug(ctx, ">>> Warning: failed to reset body position: %v", seekErr)
				}
			}
		}

		c.Next()
	}
}

func formatHeaders(headers http.Header) string {
	var sb strings.Builder
	for name, values := range headers {
		for _, v := range values {
			fmt.Fprintf(&sb, "  %s: %s\n", name, v)
		}
	}
	return sb.String()
}
