// ABOUTME: Tests for OpenCode Go per-model protocol routing and header setup.
// ABOUTME: Locks dual-protocol auth, session affinity, and conversion delegation.

package opencodego

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBaseURL = "https://opencode.ai/zen/go"

func init() {
	gin.SetMode(gin.TestMode)
}

func testRelayInfo(upstreamModel string) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    testBaseURL,
			ApiKey:            "sk-test-key",
			UpstreamModelName: upstreamModel,
		},
	}
	return info
}

func testGinContext(headers map[string]string) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

func TestGetRequestURL_AnthropicProtocolModels(t *testing.T) {
	anthropicModels := []string{
		"minimax-m2.5", "minimax-m2.7", "minimax-m3",
		"qwen3.5-plus", "qwen3.6-plus", "qwen3.7-plus", "qwen3.7-max",
	}
	adaptor := &Adaptor{}
	for _, model := range anthropicModels {
		t.Run(model, func(t *testing.T) {
			url, err := adaptor.GetRequestURL(testRelayInfo(model))
			require.NoError(t, err)
			assert.Equal(t, testBaseURL+"/v1/messages", url)
		})
	}
}

func TestGetRequestURL_OpenAIProtocolAndUnknown(t *testing.T) {
	cases := []string{"glm-5.2", "kimi-k2.6", "deepseek-v4-pro", "unknown-model-xyz"}
	adaptor := &Adaptor{}
	for _, model := range cases {
		t.Run(model, func(t *testing.T) {
			url, err := adaptor.GetRequestURL(testRelayInfo(model))
			require.NoError(t, err)
			assert.Equal(t, testBaseURL+"/v1/chat/completions", url)
		})
	}
}

func TestGetRequestURL_FallbackToOriginModelName(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "minimax-m3",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: testBaseURL,
			ApiKey:         "sk-test-key",
		},
	}
	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, testBaseURL+"/v1/messages", url)

	info.OriginModelName = "glm-5.2"
	url, err = adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, testBaseURL+"/v1/chat/completions", url)
}

func TestSetupRequestHeader_AnthropicAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	c := testGinContext(nil)
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "sk-test-key", header.Get("x-api-key"))
	assert.Equal(t, defaultAnthropicVersion, header.Get("anthropic-version"))
	assert.Empty(t, header.Get("Authorization"))
}

func TestSetupRequestHeader_OpenAIAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(nil)
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer sk-test-key", header.Get("Authorization"))
	assert.Empty(t, header.Get("x-api-key"))
}

func TestSessionAffinity_PromptCacheKey(t *testing.T) {
	for _, model := range []string{"minimax-m3", "glm-5.2"} {
		t.Run(model, func(t *testing.T) {
			adaptor := &Adaptor{}
			info := testRelayInfo(model)
			c := testGinContext(nil)

			_, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
				Model:          model,
				PromptCacheKey: "abc",
			})
			require.NoError(t, err)

			header := http.Header{}
			require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
			assert.Equal(t, "abc", header.Get("x-opencode-session"))
		})
	}
}

func TestSessionAffinity_ClaudeMetadataUserID(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	c := testGinContext(nil)

	_, err := adaptor.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{
		Model:    "minimax-m3",
		Metadata: []byte(`{"user_id":"user-from-meta"}`),
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "user-from-meta", header.Get("x-opencode-session"))
}

func TestSessionAffinity_ClientXSessionIdFallback(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(map[string]string{"x-session-id": "sid"})

	_, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{Model: "glm-5.2"})
	require.NoError(t, err)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "sid", header.Get("x-opencode-session"))
}

func TestSessionAffinity_ClientXOpencodeSessionFallback(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(map[string]string{"x-opencode-session": "native-sid"})

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "native-sid", header.Get("x-opencode-session"))
}

func TestSessionAffinity_XOpencodeSessionWinsOverXSessionId(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(map[string]string{
		"x-opencode-session": "native-sid",
		"x-session-id":       "generic-sid",
	})

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "native-sid", header.Get("x-opencode-session"))
}

func TestSessionAffinity_BodyWinsOverClientHeader(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(map[string]string{
		"x-opencode-session": "native-sid",
		"x-session-id":       "sid",
	})

	_, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
		Model:          "glm-5.2",
		PromptCacheKey: "from-body",
	})
	require.NoError(t, err)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "from-body", header.Get("x-opencode-session"))
}

func TestSessionAffinity_PassThroughPromptCacheKey(t *testing.T) {
	origin := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = true
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = origin
	}()

	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(map[string]string{
		"x-opencode-session": "native-sid",
		"x-session-id":       "sid",
	})
	setRequestBody(t, c, `{"model":"glm-5.2","prompt_cache_key":"pass-through-key"}`)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "pass-through-key", header.Get("x-opencode-session"))
}

func TestSessionAffinity_PassThroughClaudeMetadataUserID(t *testing.T) {
	origin := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = true
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = origin
	}()

	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	c := testGinContext(nil)
	setRequestBody(t, c, `{"model":"minimax-m3","metadata":{"user_id":"pass-through-user"}}`)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "pass-through-user", header.Get("x-opencode-session"))
}

func TestSessionAffinity_PassThroughDisabledSkipsBody(t *testing.T) {
	origin := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = origin
	}()

	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	// Channel-level pass-through also off (zero value).
	c := testGinContext(map[string]string{"x-session-id": "sid"})
	setRequestBody(t, c, `{"model":"glm-5.2","prompt_cache_key":"should-be-ignored"}`)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	// Convert* was not called; without pass-through body parse, header wins.
	assert.Equal(t, "sid", header.Get("x-opencode-session"))
}

func TestSessionAffinity_ChannelPassThroughBodyEnabled(t *testing.T) {
	origin := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = origin
	}()

	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	info.ChannelSetting.PassThroughBodyEnabled = true
	c := testGinContext(nil)
	setRequestBody(t, c, `{"model":"glm-5.2","prompt_cache_key":"channel-pass"}`)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "channel-pass", header.Get("x-opencode-session"))
}

func TestSessionAffinity_AbsentWhenNoSource(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(nil)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Empty(t, header.Get("x-opencode-session"))
}

func setRequestBody(t *testing.T, c *gin.Context, body string) {
	t.Helper()
	storage, err := common.CreateBodyStorage([]byte(body))
	require.NoError(t, err)
	c.Set(common.KeyBodyStorage, storage)
}

func TestUserAgent_Passthrough(t *testing.T) {
	for _, model := range []string{"minimax-m3", "glm-5.2"} {
		t.Run(model, func(t *testing.T) {
			adaptor := &Adaptor{}
			info := testRelayInfo(model)
			c := testGinContext(map[string]string{"User-Agent": "my-agent/1.0"})
			header := http.Header{}

			require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
			assert.Equal(t, "my-agent/1.0", header.Get("User-Agent"))
		})
	}
}

func TestUserAgent_UnsetWhenClientEmpty(t *testing.T) {
	for _, model := range []string{"minimax-m3", "glm-5.2"} {
		t.Run(model, func(t *testing.T) {
			adaptor := &Adaptor{}
			info := testRelayInfo(model)
			c := testGinContext(nil)
			header := http.Header{}

			require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
			assert.Empty(t, header.Get("User-Agent"))
		})
	}
}

func TestConvertOpenAIRequest_AnthropicDelegatesToClaude(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	c := testGinContext(nil)

	converted, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
		Model: "minimax-m3",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	})
	require.NoError(t, err)
	_, ok := converted.(*dto.ClaudeRequest)
	assert.True(t, ok, "expected *dto.ClaudeRequest, got %T", converted)
}

func TestConvertOpenAIRequest_OpenAIPassthrough(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(nil)
	req := &dto.GeneralOpenAIRequest{Model: "glm-5.2"}

	converted, err := adaptor.ConvertOpenAIRequest(c, info, req)
	require.NoError(t, err)
	assert.Same(t, req, converted)
}

func TestConvertClaudeRequest_AnthropicPassthrough(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	c := testGinContext(nil)
	req := &dto.ClaudeRequest{
		Model:    "minimax-m3",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}

	converted, err := adaptor.ConvertClaudeRequest(c, info, req)
	require.NoError(t, err)
	assert.Same(t, req, converted)
}

func TestConvertClaudeRequest_OpenAIDelegates(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(nil)

	converted, err := adaptor.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{
		Model:    "glm-5.2",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	openAIReq, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "expected *dto.GeneralOpenAIRequest, got %T", converted)
	assert.Equal(t, "glm-5.2", openAIReq.Model)
}

func TestUnsupportedMethodsReturnNotImplemented(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	c := testGinContext(nil)

	_, err := adaptor.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)

	_, err = adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)

	_, err = adaptor.ConvertImageRequest(c, info, dto.ImageRequest{})
	assert.Error(t, err)

	_, err = adaptor.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)

	_, err = adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)

	_, err = adaptor.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.Error(t, err)
}

func TestDoResponse_AnthropicSetsFinalRequestRelayFormat(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	info.RelayFormat = types.RelayFormatClaude

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"minimax-m3","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	_, newAPIErr := adaptor.DoResponse(c, resp, info)
	require.Nil(t, newAPIErr)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.FinalRequestRelayFormat)
}

func TestFilterInferenceCostBody_StripsInferenceCostLines(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"hi"}}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":5}}}`,
		`data: {"choices":[],"x-opencode-type":"inference-cost","cost":"0.001"}`,
	}, "\n") + "\n"

	filtered := filterInferenceCostBody(io.NopCloser(strings.NewReader(input)))
	defer filtered.Close()

	got, err := io.ReadAll(filtered)
	require.NoError(t, err)
	gotStr := string(got)

	assert.Contains(t, gotStr, `prompt_tokens_details`)
	assert.Contains(t, gotStr, `cached_tokens`)
	assert.NotContains(t, gotStr, `x-opencode-type`)
	assert.NotContains(t, gotStr, `inference-cost`)
}

func TestFilterInferenceCostBody_PassThroughNormalLines(t *testing.T) {
	input := "data: {\"id\":\"1\"}\ndata: [DONE]\n"
	filtered := filterInferenceCostBody(io.NopCloser(strings.NewReader(input)))
	defer filtered.Close()

	got, err := io.ReadAll(filtered)
	require.NoError(t, err)
	assert.Equal(t, input, string(got))
}

func TestGetModelListAndChannelName(t *testing.T) {
	adaptor := &Adaptor{}
	assert.Equal(t, ChannelName, adaptor.GetChannelName())
	assert.Len(t, adaptor.GetModelList(), 19)
}
