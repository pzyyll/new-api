// ABOUTME: Relays OpenAI Responses results and stream events.
// ABOUTME: Preserves response usage and soft-failure retry safeguards.
package openai

import (
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); openAIErrorUsable(oaiError) {
		return nil, service.ApplySoftFailRetryPolicy(c, info, service.NormalizeSoftUpstreamError(types.WithOpenAIError(*oaiError, resp.StatusCode)))
	}

	if relayconvert.IsEmptyCompletedResponses(&responsesResponse) {
		return nil, service.ApplySoftFailRetryPolicy(c, info, service.NewEmptyCompletedError())
	}

	info.ObserveResponseModel(responsesResponse.Model)
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)
	service.MarkClientPayloadWrittenContext(c, info)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)
	var streamErr *types.NewAPIError
	// Buffer lifecycle-only frames so empty/capacity soft fails can still zero-write retry.
	var lifecycleBuf []string

	flushLifecycle := func() {
		if len(lifecycleBuf) == 0 {
			return
		}
		for _, raw := range lifecycleBuf {
			var buffered dto.ResponsesStreamResponse
			if err := common.UnmarshalJsonStr(raw, &buffered); err != nil {
				continue
			}
			if buffered.Response != nil {
				raw = string(rewriteSGLangResponsesCreatedAt(info, []byte(raw), "response.created_at", buffered.Response.CreatedAt))
			}
			sendResponsesStreamData(c, buffered, raw)
			accumulator.Observe(&buffered)
		}
		lifecycleBuf = nil
		// Once lifecycle frames are flushed the client stream is committed.
		service.MarkClientPayloadWrittenContext(c, info)
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}

		if (streamResponse.Type == "response.error" || streamResponse.Type == "response.failed") && streamResponse.Response != nil {
			if oaiErr := streamResponse.Response.GetOpenAIError(); openAIErrorUsable(oaiErr) {
				normalizedErr := service.NormalizeSoftUpstreamError(types.WithOpenAIError(*oaiErr, http.StatusInternalServerError))
				if service.IsSoftFailErrorCode(normalizedErr.GetErrorCode()) {
					normalizedErr = service.ApplySoftFailRetryPolicy(c, info, normalizedErr)
					if !service.IsClientPayloadWritten(c, info) {
						lifecycleBuf = nil
						streamErr = normalizedErr
						sr.Stop(streamErr)
						return
					}
				}
			}
		}

		if streamResponse.Type == "response.completed" || streamResponse.Type == "response.done" {
			// Usage-only completed frames are common after deltas; only treat as empty
			// when no client payload has been flushed yet.
			if streamResponse.Response != nil &&
				relayconvert.IsEmptyCompletedResponses(streamResponse.Response) &&
				!service.IsClientPayloadWritten(c, info) {
				// Drop buffered lifecycle frames so zero-write failover remains possible.
				lifecycleBuf = nil
				streamErr = service.ApplySoftFailRetryPolicy(c, info, service.NewEmptyCompletedError())
				sr.Stop(streamErr)
				return
			}
		}

		switch streamResponse.Type {
		case "response.created", "response.in_progress":
			lifecycleBuf = append(lifecycleBuf, data)
			return
		}

		if streamResponse.Response != nil {
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", streamResponse.Response.CreatedAt))
		}
		flushLifecycle()
		sendResponsesStreamData(c, streamResponse, data)
		accumulator.Observe(&streamResponse)
		service.MarkClientPayloadWrittenContext(c, info)
	})

	if streamErr != nil {
		return nil, service.ApplySoftFailRetryPolicy(c, info, streamErr)
	}

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	info.StreamStatus.RequireTerminal()
	return accumulator.Finish(), nil
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}
