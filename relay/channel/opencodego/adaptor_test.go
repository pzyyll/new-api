// ABOUTME: Tests for OpenCode Go client-driven protocol forwarding and header setup.
// ABOUTME: Locks Chat/Claude/Responses routing, auth, session affinity, and passthrough conversion.

package opencodego

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

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

func TestGetRequestURL_FollowsClientAPINotModel(t *testing.T) {
	models := []string{
		"gpt-5.6-luna", "gpt-5.6-luna-high",
		"deepseek-v4-flash", "deepseek-v4-pro",
		"minimax-m3", "qwen3.7-max", "glm-5.2", "unknown-model-xyz",
	}
	adaptor := &Adaptor{}

	t.Run("chat", func(t *testing.T) {
		for _, model := range models {
			info := testRelayInfo(model)
			info.RelayFormat = types.RelayFormatOpenAI
			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, testBaseURL+"/v1/chat/completions", url, model)
		}
	})

	t.Run("claude", func(t *testing.T) {
		for _, model := range models {
			info := testRelayInfo(model)
			info.RelayFormat = types.RelayFormatClaude
			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, testBaseURL+"/v1/messages", url, model)
		}
	})

	t.Run("responses", func(t *testing.T) {
		for _, model := range models {
			info := testRelayInfo(model)
			info.RelayFormat = types.RelayFormatOpenAIResponses
			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, testBaseURL+"/v1/responses", url, model)
		}
	})
}

func TestGetRequestURL_ResponsesRelayMode(t *testing.T) {
	adaptor := &Adaptor{}
	for _, model := range []string{"glm-5.2", "deepseek-v4-flash", "unknown-model-xyz"} {
		t.Run(model, func(t *testing.T) {
			info := testRelayInfo(model)
			info.RelayFormat = types.RelayFormatOpenAI
			info.RelayMode = relayconstant.RelayModeResponses
			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, testBaseURL+"/v1/responses", url)
		})
	}
}

func TestGetRequestURL_ResponsesCompact(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("gpt-5.6-luna")
	info.RelayMode = relayconstant.RelayModeResponsesCompact
	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, testBaseURL+"/v1/responses/compact", url)

	info = testRelayInfo("deepseek-v4-pro")
	info.RelayFormat = types.RelayFormatOpenAIResponsesCompaction
	url, err = adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, testBaseURL+"/v1/responses/compact", url)
}

func TestGetRequestURL_DefaultIsChatCompletions(t *testing.T) {
	adaptor := &Adaptor{}
	url, err := adaptor.GetRequestURL(testRelayInfo("minimax-m3"))
	require.NoError(t, err)
	assert.Equal(t, testBaseURL+"/v1/chat/completions", url)
}

func TestSetupRequestHeader_ClaudeAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("glm-5.2")
	info.RelayFormat = types.RelayFormatClaude
	c := testGinContext(nil)
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "sk-test-key", header.Get("x-api-key"))
	assert.Equal(t, defaultAnthropicVersion, header.Get("anthropic-version"))
	assert.Empty(t, header.Get("Authorization"))
}

func TestSetupRequestHeader_OpenAIAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("minimax-m3")
	info.RelayFormat = types.RelayFormatOpenAI
	c := testGinContext(nil)
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer sk-test-key", header.Get("Authorization"))
	assert.Empty(t, header.Get("x-api-key"))
}

func TestSetupRequestHeader_ResponsesAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("deepseek-v4-flash")
	info.RelayFormat = types.RelayFormatOpenAIResponses
	c := testGinContext(nil)
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer sk-test-key", header.Get("Authorization"))
	assert.Empty(t, header.Get("x-api-key"))
}

func TestConvertOpenAIRequest_PassthroughAnyModel(t *testing.T) {
	adaptor := &Adaptor{}
	c := testGinContext(nil)
	for _, model := range []string{"gpt-5.6-luna", "minimax-m3", "deepseek-v4-pro"} {
		info := testRelayInfo(model)
		req := &dto.GeneralOpenAIRequest{Model: model}
		converted, err := adaptor.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err, model)
		assert.Same(t, req, converted, model)
	}
}

func TestConvertClaudeRequest_PassthroughAnyModel(t *testing.T) {
	adaptor := &Adaptor{}
	c := testGinContext(nil)
	for _, model := range []string{"gpt-5.6-luna", "minimax-m3", "glm-5.2"} {
		info := testRelayInfo(model)
		req := &dto.ClaudeRequest{
			Model:    model,
			Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
		}
		converted, err := adaptor.ConvertClaudeRequest(c, info, req)
		require.NoError(t, err, model)
		assert.Same(t, req, converted, model)
	}
}

func TestConvertOpenAIResponsesRequest_Passthrough(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("deepseek-v4-flash")
	c := testGinContext(nil)

	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model: "deepseek-v4-flash",
		Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]`),
	})
	require.NoError(t, err)
	got, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok, "expected dto.OpenAIResponsesRequest, got %T", converted)
	assert.Equal(t, "deepseek-v4-flash", got.Model)
	assert.Equal(t, json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]`), got.Input)
}

func TestConvertOpenAIResponsesRequest_EffortSuffix(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("gpt-5.6-luna")
	c := testGinContext(nil)

	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-luna-high",
	})
	require.NoError(t, err)
	got, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok, "expected dto.OpenAIResponsesRequest, got %T", converted)
	assert.Equal(t, "gpt-5.6-luna", got.Model)
	require.NotNil(t, got.Reasoning)
	assert.Equal(t, "high", got.Reasoning.Effort)
	assert.Equal(t, "high", info.ReasoningEffort)
}

func TestSessionAffinity_ResponsesPromptCacheKey(t *testing.T) {
	adaptor := &Adaptor{}
	info := testRelayInfo("gpt-5.6-luna")
	c := testGinContext(nil)

	_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model:          "gpt-5.6-luna",
		PromptCacheKey: json.RawMessage(`"cache-key-1"`),
	})
	require.NoError(t, err)

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "cache-key-1", header.Get("x-opencode-session"))
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
	info.RelayFormat = types.RelayFormatClaude
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
	info.RelayFormat = types.RelayFormatClaude
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

	_, err = adaptor.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.Error(t, err)
}

func TestDoResponse_ClaudeSetsFinalRequestRelayFormat(t *testing.T) {
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

func TestFilterZenCostChunk_StripsPingCostChunk(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`,
		`event: ping`,
		`data: {"type":"ping","cost":"0"}`,
	}, "\n") + "\n"

	filtered := filterZenCostChunk(io.NopCloser(strings.NewReader(input)))
	defer filtered.Close()

	got, err := io.ReadAll(filtered)
	require.NoError(t, err)
	gotStr := string(got)

	assert.Contains(t, gotStr, `response.output_text.delta`)
	assert.Contains(t, gotStr, `response.completed`)
	assert.NotContains(t, gotStr, `"type":"ping"`)
}

func TestFilterZenCostChunk_KeepsContentMentioningPing(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"\"type\":\"ping\" in text"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
	}, "\n") + "\n"

	filtered := filterZenCostChunk(io.NopCloser(strings.NewReader(input)))
	defer filtered.Close()

	got, err := io.ReadAll(filtered)
	require.NoError(t, err)
	assert.Equal(t, input, string(got))
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
	assert.Len(t, adaptor.GetModelList(), 22)
	assert.Contains(t, adaptor.GetModelList(), "gpt-5.6-luna")
	assert.Contains(t, adaptor.GetModelList(), "grok-4.5")
	assert.Contains(t, adaptor.GetModelList(), "hy3")
}
