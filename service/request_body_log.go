// ABOUTME: Capture the raw client request JSON body for usage log details.
// ABOUTME: Behavior is gated on common.RequestDetailLogEnabled and only stores valid JSON payloads.
package service

import (
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// CaptureRequestBodyForLog reads the current request's JSON body and returns its
// raw bytes as a string. It returns an empty string when:
//   - request body logging is disabled,
//   - the context is nil or has no request,
//   - the content type is not JSON,
//   - the body cannot be read or is not valid JSON.
//
// The underlying body storage position is reset so downstream readers are unaffected.
func CaptureRequestBodyForLog(ctx *gin.Context) string {
	if !common.RequestDetailLogEnabled {
		return ""
	}
	if ctx == nil || ctx.Request == nil {
		return ""
	}
	if !isJSONContentType(ctx.Request.Header.Get("Content-Type")) {
		return ""
	}

	storage, err := common.GetBodyStorage(ctx)
	if err != nil || storage == nil {
		return ""
	}

	data, err := storage.Bytes()
	if err != nil {
		return ""
	}
	// Always restore the storage position so the next reader sees a clean stream.
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return ""
	}
	if len(data) == 0 {
		return ""
	}

	var probe any
	if err := common.Unmarshal(data, &probe); err != nil {
		return ""
	}

	return string(data)
}

func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	// Strip any parameters such as `; charset=utf-8`.
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	return contentType == "application/json" || strings.HasSuffix(contentType, "+json")
}
