// ABOUTME: Capture upstream response bodies for usage log details (debug).
// ABOUTME: Non-stream JSON bodies and stream SSE data payloads; gated by RequestDetailLogEnabled.
package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

// CaptureResponseBodyForLog returns the upstream response body stored on RelayInfo
// when request detail logging is enabled.
func CaptureResponseBodyForLog(info *relaycommon.RelayInfo) string {
	if !common.RequestDetailLogEnabled || info == nil {
		return ""
	}
	return info.GetResponseBodyForLog()
}

// AppendUpstreamStreamChunkForLog appends one upstream SSE data payload (JSON text)
// after the stream ends it is stored as newline-delimited plain text.
func AppendUpstreamStreamChunkForLog(info *relaycommon.RelayInfo, data string) {
	if !common.RequestDetailLogEnabled || info == nil {
		return
	}
	if shouldSkipResponseBodyLogByMode(info.RelayMode) {
		return
	}
	if data == "" {
		return
	}
	info.AppendResponseBodyForLog(data)
}

// MaybeCaptureUpstreamHTTPResponseForLog reads and re-wraps a non-stream (or
// stream-error) upstream HTTP body so later handlers still see the full body.
// Successful streams are left untouched; StreamScannerHandler records chunks.
func MaybeCaptureUpstreamHTTPResponseForLog(info *relaycommon.RelayInfo, resp *http.Response) {
	if !common.RequestDetailLogEnabled || info == nil || resp == nil || resp.Body == nil {
		return
	}
	// Always reset so retries do not keep a previous attempt's body.
	info.ResetResponseBodyForLog()

	if shouldSkipResponseBodyLogByMode(info.RelayMode) {
		return
	}
	// Successful streams are captured as SSE data lines in StreamScannerHandler.
	if info.IsStream && resp.StatusCode < http.StatusBadRequest {
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if !isLoggableUpstreamResponseContentType(contentType) {
		return
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return
	}
	if len(body) > 0 {
		info.SetResponseBodyForLog(string(body))
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
}

func shouldSkipResponseBodyLogByMode(relayMode int) bool {
	switch relayMode {
	case relayconstant.RelayModeImagesGenerations,
		relayconstant.RelayModeImagesEdits,
		relayconstant.RelayModeAudioSpeech,
		relayconstant.RelayModeAudioTranscription,
		relayconstant.RelayModeAudioTranslation,
		relayconstant.RelayModeVideoFetchByID,
		relayconstant.RelayModeVideoSubmit,
		relayconstant.RelayModeRealtime,
		relayconstant.RelayModeMidjourneyImagine,
		relayconstant.RelayModeMidjourneyDescribe,
		relayconstant.RelayModeMidjourneyBlend,
		relayconstant.RelayModeMidjourneyChange,
		relayconstant.RelayModeMidjourneySimpleChange,
		relayconstant.RelayModeMidjourneyNotify,
		relayconstant.RelayModeMidjourneyTaskFetch,
		relayconstant.RelayModeMidjourneyTaskImageSeed,
		relayconstant.RelayModeMidjourneyTaskFetchByCondition,
		relayconstant.RelayModeMidjourneyAction,
		relayconstant.RelayModeMidjourneyModal,
		relayconstant.RelayModeMidjourneyShorten,
		relayconstant.RelayModeSwapFace,
		relayconstant.RelayModeMidjourneyUpload,
		relayconstant.RelayModeMidjourneyVideo,
		relayconstant.RelayModeMidjourneyEdits:
		return true
	default:
		return false
	}
}

func isLoggableUpstreamResponseContentType(contentType string) bool {
	if contentType == "" {
		// Many upstreams omit Content-Type on JSON errors; still capture.
		return true
	}
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	switch {
	case contentType == "application/json",
		strings.HasSuffix(contentType, "+json"),
		contentType == "text/json",
		contentType == "text/plain",
		contentType == "text/event-stream",
		strings.HasPrefix(contentType, "text/"):
		return true
	case strings.HasPrefix(contentType, "image/"),
		strings.HasPrefix(contentType, "audio/"),
		strings.HasPrefix(contentType, "video/"),
		strings.HasPrefix(contentType, "application/octet-stream"),
		strings.HasPrefix(contentType, "multipart/"):
		return false
	default:
		// Be conservative for unknown binary-ish types.
		return strings.Contains(contentType, "json") || strings.HasPrefix(contentType, "text/")
	}
}
