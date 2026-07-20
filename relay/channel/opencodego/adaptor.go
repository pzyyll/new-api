// ABOUTME: OpenCode Go channel adaptor: per-model dual-protocol relay to zen/go.
// ABOUTME: Routes Anthropic-protocol models to /messages, others to /chat/completions.

package opencodego

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

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
	if isAnthropicProtocolModel(model) {
		return fmt.Sprintf("%s/v1/messages", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/chat/completions", baseURL), nil
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
	if isAnthropicProtocolModel(model) {
		return request, nil
	}
	return (&openai.Adaptor{}).ConvertClaudeRequest(c, info, request)
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
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
	if isAnthropicProtocolModel(model) {
		return (&claude.Adaptor{}).DoResponse(c, resp, info)
	}
	return (&openai.Adaptor{}).DoResponse(c, resp, info)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
