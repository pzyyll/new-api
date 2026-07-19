package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newResponsesChatTestContext(t *testing.T, body string, isStream bool) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "responses-test")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:           isStream,
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		DisablePing:        true,
	}
	return c, recorder, resp, info
}

func TestOaiResponsesToChatStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `"role":"assistant"`)
	require.Contains(t, got, `"content":"hello"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
	require.Contains(t, got, `"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`)
	require.Contains(t, got, `data: [DONE]`)
	requireOrderedSubstrings(t, got,
		`"role":"assistant"`,
		`"content":"hello"`,
		`"name":"lookup"`,
		`"arguments":"{\"q\":\"x\"}"`,
		`"finish_reason":"tool_calls"`,
		`"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`,
		`data: [DONE]`,
	)
}

func TestOaiResponsesToChatBufferedStreamHandlerReturnsJSONFromSSE(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"buffered text"}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.done","response":{"model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

	usage, err := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.TotalTokens)

	got := recorder.Body.String()
	require.NotContains(t, got, `data:`)
	require.Contains(t, got, `"object":"chat.completion"`)
	require.Contains(t, got, `"content":"buffered text"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
}

func TestOaiChatToResponsesStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	usage, err := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `event: response.created`)
	require.Contains(t, got, `event: response.output_text.delta`)
	require.Contains(t, got, `"delta":"hello"`)
	require.Contains(t, got, `event: response.function_call_arguments.delta`)
	require.Contains(t, got, `"delta":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `event: response.completed`)
	require.Contains(t, got, `"input_tokens":2`)
	require.Contains(t, got, `"output_tokens":3`)
	requireOrderedSubstrings(t, got,
		`event: response.created`,
		`event: response.output_item.added`,
		`event: response.output_text.delta`,
		`event: response.output_item.added`,
		`event: response.function_call_arguments.delta`,
		`event: response.output_text.done`,
		`event: response.function_call_arguments.done`,
		`event: response.completed`,
	)
}

func requireOrderedSubstrings(t *testing.T, s string, parts ...string) {
	t.Helper()

	offset := 0
	for _, part := range parts {
		idx := strings.Index(s[offset:], part)
		require.NotEqualf(t, -1, idx, "missing %q after byte offset %d", part, offset)
		offset += idx + len(part)
	}
}

func TestOaiResponsesToChatHandlerEmptyCompleted(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := `{"id":"resp_empty","model":"gpt-test","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking only"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	resp.Header.Set("Content-Type", "application/json")

	usage, err := OaiResponsesToChatHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	require.True(t, info.EmptyCompleted)
	require.True(t, info.AffinityUnusable)
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesToChatBufferedStreamHandlerEmptyCompleted(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_empty","model":"gpt-test","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking only"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	usage, err := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	require.True(t, info.EmptyCompleted)
	require.True(t, info.AffinityUnusable)
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesToChatStreamHandlerEmptyCompletedZeroWrite(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_empty","model":"gpt-test","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking only"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	require.True(t, info.EmptyCompleted)
	require.True(t, info.AffinityUnusable)
	require.False(t, info.ClientPayloadWritten)
	// Headers may be set for SSE, but no assistant payload should be committed.
	require.NotContains(t, recorder.Body.String(), `"content"`)
	require.NotContains(t, recorder.Body.String(), `thinking only`)
}

func TestOaiResponsesToChatStreamHandlerEmptyCompletedAfterCreated(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	// Real Grok-like sequence: created emits role-only start which must NOT commit the stream.
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_empty","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.completed","response":{"id":"resp_empty","model":"gpt-test","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking only"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.False(t, info.ClientPayloadWritten)
	require.False(t, types.IsSkipRetryError(err))
	require.NotContains(t, recorder.Body.String(), `thinking only`)
}

func TestOaiResponsesToChatStreamHandlerCapacityAfterReasoningSkipsRetry(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"planning search"}`,
		`data: {"type":"response.failed","response":{"error":{"message":"The model is currently at capacity due to high demand. Please use priority processing","type":"upstream_error","code":null}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeUpstreamCapacity, err.GetErrorCode())
	require.True(t, info.ClientPayloadWritten)
	require.True(t, types.IsSkipRetryError(err))
	require.Contains(t, recorder.Body.String(), `planning search`)
}

func TestOaiResponsesToChatStreamHandlerCapacityFailedEvent(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.failed","response":{"error":{"message":"The model is currently at capacity due to high demand. Please use priority processing","type":"upstream_error","code":null}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, _, resp, info := newResponsesChatTestContext(t, body, true)
	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeUpstreamCapacity, err.GetErrorCode())
	require.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	require.False(t, types.IsSkipRetryError(err))
	require.False(t, info.ClientPayloadWritten)
}

func TestOaiResponsesToChatStreamHandlerCapacityNullCodeWithoutType(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.failed","response":{"error":{"message":"The model is currently at capacity due to high demand","code":null}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, _, resp, info := newResponsesChatTestContext(t, body, true)
	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeUpstreamCapacity, err.GetErrorCode())
	_ = info
}
