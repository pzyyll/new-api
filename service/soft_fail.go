// ABOUTME: Shared soft-failure control plane for empty-completed and capacity errors.
// ABOUTME: Marks affinity unusable, clears sticky binding, and records admin soft-fail markers.
package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// MarkClientPayloadWritten records that assistant payload was flushed to the client.
// Once set, in-request soft-fail channel switching is no longer safe.
func MarkClientPayloadWritten(info *relaycommon.RelayInfo) {
	if info == nil {
		return
	}
	info.ClientPayloadWritten = true
}

// MarkClientPayloadWrittenContext also mirrors the flag onto gin for middleware/log paths.
func MarkClientPayloadWrittenContext(c *gin.Context, info *relaycommon.RelayInfo) {
	MarkClientPayloadWritten(info)
	if c != nil {
		c.Set(ginKeyClientPayloadWritten, true)
	}
}

// IsClientPayloadWritten reports whether assistant payload was flushed for this attempt.
func IsClientPayloadWritten(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if info != nil && info.ClientPayloadWritten {
		return true
	}
	if c == nil {
		return false
	}
	if v, ok := c.Get(ginKeyClientPayloadWritten); ok {
		if b, ok := v.(bool); ok && b {
			return true
		}
	}
	return false
}

// ChatCompletionsStreamHasClientPayload reports whether a chat SSE chunk carries
// assistant-facing payload (text, reasoning, or tool calls). Role-only / empty
// start chunks are not payload and must not block empty-completed failover.
func ChatCompletionsStreamHasClientPayload(resp *dto.ChatCompletionsStreamResponse) bool {
	if resp == nil {
		return false
	}
	if resp.Usage != nil {
		return true
	}
	for _, choice := range resp.Choices {
		if strings.TrimSpace(choice.Delta.GetContentString()) != "" {
			return true
		}
		if strings.TrimSpace(choice.Delta.GetReasoningContent()) != "" {
			return true
		}
		if len(choice.Delta.ToolCalls) > 0 {
			return true
		}
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			// Finish-only after prior empty role start is still a terminal commit for
			// double-write safety once the client has observed a finished choice.
			// Empty completed is intercepted before terminal conversion, so this mainly
			// protects capacity/error paths that arrive after a finish frame.
			return true
		}
	}
	return false
}

// ApplySoftFailRetryPolicy clears affinity markers and, when the client stream is
// already committed, marks the error skip-retry so controller will not switch channels.
func ApplySoftFailRetryPolicy(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError) *types.NewAPIError {
	if err == nil {
		return nil
	}
	if !IsSoftFailErrorCode(err.GetErrorCode()) {
		return err
	}
	ApplySoftFailFromError(c, info, err)
	if IsClientPayloadWritten(c, info) {
		return types.MarkSkipRetry(err)
	}
	return err
}

// MarkSoftFailAffinity marks the current attempt as affinity-unusable, clears sticky
// binding when possible, and stores admin-facing soft-fail markers on gin context.
func MarkSoftFailAffinity(c *gin.Context, info *relaycommon.RelayInfo, class string) {
	if info != nil {
		info.AffinityUnusable = true
		info.SoftFailReason = class
		if class == SoftFailClassEmptyCompleted {
			info.EmptyCompleted = true
		}
	}
	if c != nil {
		c.Set(ginKeyChannelAffinityUnusable, true)
		if class != "" {
			c.Set(ginKeySoftFailClass, class)
		}
		if info != nil && info.ClientPayloadWritten {
			c.Set(ginKeyClientPayloadWritten, true)
		}
	}

	cleared := ClearCurrentChannelAffinityCache(c)
	if c != nil && cleared {
		c.Set(ginKeyAffinityCleared, true)
	}

	if c != nil && class != "" {
		channelID := 0
		modelName := ""
		clientWritten := false
		if info != nil {
			if info.ChannelMeta != nil {
				channelID = info.ChannelId
			}
			modelName = info.OriginModelName
			if modelName == "" {
				modelName = info.UpstreamModelName
			}
			clientWritten = info.ClientPayloadWritten
		}
		logger.LogWarn(c, fmt.Sprintf(
			"soft fail class=%s channel=#%d model=%s client_written=%t affinity_cleared=%t",
			class, channelID, modelName, clientWritten, cleared,
		))
	}
}

// MarkEmptyCompletedSoftFail applies empty-completed soft-fail flags and affinity clear.
func MarkEmptyCompletedSoftFail(c *gin.Context, info *relaycommon.RelayInfo) {
	MarkSoftFailAffinity(c, info, SoftFailClassEmptyCompleted)
}

// MarkCapacitySoftFail applies capacity soft-fail flags and affinity clear.
func MarkCapacitySoftFail(c *gin.Context, info *relaycommon.RelayInfo) {
	MarkSoftFailAffinity(c, info, SoftFailClassUpstreamCapacity)
}

// NewEmptyCompletedError builds the switch-channel empty-response soft fail.
func NewEmptyCompletedError() *types.NewAPIError {
	return types.NewOpenAIError(
		fmt.Errorf("empty completed response: no usable assistant output"),
		types.ErrorCodeEmptyResponse,
		http.StatusServiceUnavailable,
	)
}

// IsChannelAffinityUnusable reports whether the request marked affinity as unusable.
func IsChannelAffinityUnusable(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(ginKeyChannelAffinityUnusable); ok {
		if b, ok := v.(bool); ok && b {
			return true
		}
	}
	return false
}

// AppendSoftFailAdminInfo nests soft-fail markers under admin_info.
func AppendSoftFailAdminInfo(c *gin.Context, info *relaycommon.RelayInfo, adminInfo map[string]interface{}) {
	if adminInfo == nil {
		return
	}

	class := ""
	affinityCleared := false
	clientWritten := false

	if c != nil {
		if v, ok := c.Get(ginKeySoftFailClass); ok {
			if s, ok := v.(string); ok {
				class = s
			}
		}
		if v, ok := c.Get(ginKeyAffinityCleared); ok {
			if b, ok := v.(bool); ok {
				affinityCleared = b
			}
		}
		if v, ok := c.Get(ginKeyClientPayloadWritten); ok {
			if b, ok := v.(bool); ok {
				clientWritten = b
			}
		}
	}
	if info != nil {
		if info.SoftFailReason != "" && class == "" {
			class = info.SoftFailReason
		}
		if info.ClientPayloadWritten {
			clientWritten = true
		}
		if info.AffinityUnusable && class == "" && info.EmptyCompleted {
			class = SoftFailClassEmptyCompleted
		}
	}

	if class == "" && !affinityCleared && !clientWritten {
		return
	}
	if class != "" {
		adminInfo["soft_fail_class"] = class
	}
	if affinityCleared {
		adminInfo["affinity_cleared"] = true
	}
	if clientWritten {
		adminInfo["client_payload_written"] = true
	}
	if info != nil && info.EmptyCompleted {
		adminInfo["empty_completed"] = true
	}
}

// ApplySoftFailFromError marks affinity/log state when the returned error is a soft fail.
// Idempotent for the same class on one request (avoids duplicate warn logs).
func ApplySoftFailFromError(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError) {
	if err == nil {
		return
	}
	class := SoftFailClassFromErrorCode(err.GetErrorCode())
	if class == "" {
		return
	}
	if c != nil {
		if prev, ok := c.Get(ginKeySoftFailClass); ok {
			if prevClass, ok := prev.(string); ok && prevClass == class {
				// Still refresh client-written flag if it became true after first mark.
				if info != nil && info.ClientPayloadWritten {
					c.Set(ginKeyClientPayloadWritten, true)
				}
				return
			}
		}
	}
	MarkSoftFailAffinity(c, info, class)
}
