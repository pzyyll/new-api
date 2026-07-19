// ABOUTME: Unit tests for upstream response body capture used by usage logs.
// ABOUTME: Covers switch gating, content-type/mode skips, and stream chunk append.
package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureResponseBodyForLog_Disabled(t *testing.T) {
	prev := common.RequestDetailLogEnabled
	t.Cleanup(func() { common.RequestDetailLogEnabled = prev })
	common.RequestDetailLogEnabled = false

	info := &relaycommon.RelayInfo{}
	info.SetResponseBodyForLog(`{"ok":true}`)
	assert.Equal(t, "", CaptureResponseBodyForLog(info))
}

func TestMaybeCaptureUpstreamHTTPResponseForLog_NonStreamJSON(t *testing.T) {
	prev := common.RequestDetailLogEnabled
	t.Cleanup(func() { common.RequestDetailLogEnabled = prev })
	common.RequestDetailLogEnabled = true

	info := &relaycommon.RelayInfo{IsStream: false}
	body := `{"id":"chatcmpl-1","choices":[]}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	MaybeCaptureUpstreamHTTPResponseForLog(info, resp)
	assert.Equal(t, body, CaptureResponseBodyForLog(info))

	// Body must remain readable for downstream handlers.
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}

func TestMaybeCaptureUpstreamHTTPResponseForLog_SkipsSuccessfulStream(t *testing.T) {
	prev := common.RequestDetailLogEnabled
	t.Cleanup(func() { common.RequestDetailLogEnabled = prev })
	common.RequestDetailLogEnabled = true

	info := &relaycommon.RelayInfo{IsStream: true}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"a\":1}\n\n")),
	}

	MaybeCaptureUpstreamHTTPResponseForLog(info, resp)
	assert.Equal(t, "", CaptureResponseBodyForLog(info))

	// Stream body must not be consumed.
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(got), "data:")
}

func TestMaybeCaptureUpstreamHTTPResponseForLog_CapturesStreamErrorJSON(t *testing.T) {
	prev := common.RequestDetailLogEnabled
	t.Cleanup(func() { common.RequestDetailLogEnabled = prev })
	common.RequestDetailLogEnabled = true

	info := &relaycommon.RelayInfo{IsStream: true}
	body := `{"error":{"message":"capacity"}}`
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	MaybeCaptureUpstreamHTTPResponseForLog(info, resp)
	assert.Equal(t, body, CaptureResponseBodyForLog(info))
}

func TestMaybeCaptureUpstreamHTTPResponseForLog_SkipsImageMode(t *testing.T) {
	prev := common.RequestDetailLogEnabled
	t.Cleanup(func() { common.RequestDetailLogEnabled = prev })
	common.RequestDetailLogEnabled = true

	info := &relaycommon.RelayInfo{
		IsStream:  false,
		RelayMode: relayconstant.RelayModeImagesGenerations,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"xxx"}]}`)),
	}

	MaybeCaptureUpstreamHTTPResponseForLog(info, resp)
	assert.Equal(t, "", CaptureResponseBodyForLog(info))
}

func TestAppendUpstreamStreamChunkForLog(t *testing.T) {
	prev := common.RequestDetailLogEnabled
	t.Cleanup(func() { common.RequestDetailLogEnabled = prev })
	common.RequestDetailLogEnabled = true

	info := &relaycommon.RelayInfo{IsStream: true}
	AppendUpstreamStreamChunkForLog(info, `{"delta":"a"}`)
	AppendUpstreamStreamChunkForLog(info, `{"delta":"b"}`)

	assert.Equal(t, "{\"delta\":\"a\"}\n{\"delta\":\"b\"}\n", CaptureResponseBodyForLog(info))
}

func TestIsLoggableUpstreamResponseContentType(t *testing.T) {
	assert.True(t, isLoggableUpstreamResponseContentType("application/json"))
	assert.True(t, isLoggableUpstreamResponseContentType("application/json; charset=utf-8"))
	assert.True(t, isLoggableUpstreamResponseContentType("text/event-stream"))
	assert.True(t, isLoggableUpstreamResponseContentType(""))
	assert.False(t, isLoggableUpstreamResponseContentType("image/png"))
	assert.False(t, isLoggableUpstreamResponseContentType("audio/mpeg"))
	assert.False(t, isLoggableUpstreamResponseContentType("application/octet-stream"))
}
