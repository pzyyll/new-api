// ABOUTME: OpenCode Go channel adaptor: per-model dual-protocol relay to zen/go.
// ABOUTME: Routes Anthropic-protocol models to /messages, gpt-5.6-luna to /responses, rest to /chat/completions.

package opencodego

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const defaultAnthropicVersion = "2023-06-01"

type Adaptor struct {
	// sessionAffinity is populated during Convert* from the request body and
	// applied as x-opencode-session in SetupRequestHeader (same adaptor instance).
	sessionAffinity string
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {}

func getUpstreamModelName(info *relaycommon.RelayInfo, fallback string) string {
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		return info.UpstreamModelName
	}
	return fallback
}

func isPassThroughEnabled(info *relaycommon.RelayInfo) bool {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return true
	}
	return info != nil && info.ChannelSetting.PassThroughBodyEnabled
}

// sessionAffinityFromRequestBody extracts OpenAI prompt_cache_key or Claude
// metadata.user_id from the cached raw body without full DTO unmarshal.
// Used when pass-through skips Convert*, so a.sessionAffinity is never set.
func sessionAffinityFromRequestBody(c *gin.Context) string {
	if c == nil {
		return ""
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage == nil {
		return ""
	}
	body, err := storage.Bytes()
	if err != nil || len(body) == 0 {
		return ""
	}
	if v := gjson.GetBytes(body, "prompt_cache_key"); v.Exists() && v.Type == gjson.String {
		if s := v.String(); s != "" {
			return s
		}
	}
	if v := gjson.GetBytes(body, "metadata.user_id"); v.Exists() && v.Type == gjson.String {
		if s := v.String(); s != "" {
			return s
		}
	}
	return ""
}

// resolveSessionAffinity: body field (Convert* or pass-through parse) →
// client x-opencode-session → client x-session-id → empty.
func (a *Adaptor) resolveSessionAffinity(c *gin.Context, info *relaycommon.RelayInfo) string {
	if a.sessionAffinity != "" {
		return a.sessionAffinity
	}
	if isPassThroughEnabled(info) {
		if id := sessionAffinityFromRequestBody(c); id != "" {
			return id
		}
	}
	if c == nil || c.Request == nil {
		return ""
	}
	if id := c.Request.Header.Get("x-opencode-session"); id != "" {
		return id
	}
	return c.Request.Header.Get("x-session-id")
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	// Channel base is stored without /v1 (platform convention, e.g.
	// https://opencode.ai/zen/go); append /v1 here like other OpenAI-style channels.
	baseURL := info.ChannelBaseUrl
	model := getUpstreamModelName(info, info.OriginModelName)
	relayMode := 0
	if info != nil {
		relayMode = info.RelayMode
	}
	switch {
	case isAnthropicProtocolModel(model):
		return fmt.Sprintf("%s/v1/messages", baseURL), nil
	case isResponsesProtocolModel(model) ||
		relayMode == relayconstant.RelayModeResponses ||
		relayMode == relayconstant.RelayModeResponsesCompact:
		return fmt.Sprintf("%s/v1/responses", baseURL), nil
	default:
		return fmt.Sprintf("%s/v1/chat/completions", baseURL), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)

	model := getUpstreamModelName(info, info.OriginModelName)
	if isAnthropicProtocolModel(model) {
		req.Set("x-api-key", info.ApiKey)
		anthropicVersion := c.Request.Header.Get("anthropic-version")
		if anthropicVersion == "" {
			anthropicVersion = defaultAnthropicVersion
		}
		req.Set("anthropic-version", anthropicVersion)
		claude.CommonClaudeHeadersOperation(c, req, info)
	} else {
		req.Set("Authorization", "Bearer "+info.ApiKey)
	}

	if ua := c.Request.Header.Get("User-Agent"); ua != "" {
		req.Set("User-Agent", ua)
	}

	if sessionID := a.resolveSessionAffinity(c, info); sessionID != "" {
		req.Set("x-opencode-session", sessionID)
	}

	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if request.PromptCacheKey != "" {
		a.sessionAffinity = request.PromptCacheKey
	}

	model := getUpstreamModelName(info, request.Model)
	if isResponsesProtocolModel(model) {
		return nil, fmt.Errorf("model %s is served via the Responses API; use POST /v1/responses instead of /v1/chat/completions", model)
	}
	if isAnthropicProtocolModel(model) {
		return (&claude.Adaptor{}).ConvertOpenAIRequest(c, info, request)
	}
	return request, nil
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if len(request.Metadata) > 0 {
		var meta dto.ClaudeMetadata
		if err := common.Unmarshal(request.Metadata, &meta); err == nil && meta.UserId != "" {
			a.sessionAffinity = meta.UserId
		}
	}

	model := getUpstreamModelName(info, request.Model)
	if isResponsesProtocolModel(model) {
		return nil, fmt.Errorf("model %s is served via the Responses API; use POST /v1/responses instead of /v1/messages", model)
	}
	if isAnthropicProtocolModel(model) {
		return request, nil
	}
	return (&openai.Adaptor{}).ConvertClaudeRequest(c, info, request)
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	if len(request.PromptCacheKey) > 0 {
		var key string
		if err := common.Unmarshal(request.PromptCacheKey, &key); err == nil && key != "" {
			a.sessionAffinity = key
		}
	}

	// 转换模型推理力度后缀（new-api 约定，如 gpt-5.6-luna-high）
	effort, originModel := reasoning.ParseOpenAIReasoningEffortFromModelSuffix(request.Model)
	if effort != "" {
		if request.Reasoning == nil {
			request.Reasoning = &dto.Reasoning{
				Effort: effort,
			}
		} else {
			request.Reasoning.Effort = effort
		}
		request.Model = originModel
	}
	if info != nil && request.Reasoning != nil && request.Reasoning.Effort != "" {
		info.ReasoningEffort = request.Reasoning.Effort
	}
	return request, nil
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	model := getUpstreamModelName(info, info.OriginModelName)

	resp.Body = filterInferenceCostBody(resp.Body)

	if isAnthropicProtocolModel(model) {
		return (&claude.Adaptor{}).DoResponse(c, resp, info)
	}

	if info != nil && info.RelayMode == relayconstant.RelayModeResponses {
		resp.Body = filterZenCostChunk(resp.Body)
	}

	return (&openai.Adaptor{}).DoResponse(c, resp, info)
}

// filterInferenceCostBody returns a ReadCloser that strips SSE data lines
// whose JSON payload contains "x-opencode-type" (e.g. the upstream
// inference-cost meta-message). Other lines pass through unchanged.
func filterInferenceCostBody(rc io.ReadCloser) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer rc.Close()
		defer pw.Close()
		scanner := bufio.NewScanner(rc)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, `"x-opencode-type"`) {
				continue
			}
			if _, err := io.WriteString(pw, line+"\n"); err != nil {
				return
			}
		}
		_ = scanner.Err()
	}()
	return pr
}

// filterZenCostChunk returns a ReadCloser that strips SSE data lines whose
// JSON payload is the zen subscription-cost meta chunk (`{"type":"ping",...}`
// with a "cost" field). Other lines pass through unchanged.
func filterZenCostChunk(rc io.ReadCloser) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer rc.Close()
		defer pw.Close()
		scanner := bufio.NewScanner(rc)
		for scanner.Scan() {
			line := scanner.Text()
			if isZenCostChunkLine(line) {
				continue
			}
			if _, err := io.WriteString(pw, line+"\n"); err != nil {
				return
			}
		}
		_ = scanner.Err()
	}()
	return pr
}

// isZenCostChunkLine reports whether an SSE line is the zen gateway's trailing
// cost meta-chunk. Only exact top-level matches are dropped so response text
// containing the same substring is never affected.
func isZenCostChunkLine(line string) bool {
	if len(line) < 6 || line[:5] != "data:" {
		return false
	}
	payload := strings.TrimSpace(line[5:])
	return gjson.Get(payload, "type").String() == "ping" && gjson.Get(payload, "cost").Exists()
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
