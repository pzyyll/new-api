// ABOUTME: Classifies OpenAI Responses payloads for usable assistant output.
// ABOUTME: Detects empty-completed (reasoning-only) soft failures for multi-channel failover.
package oairesponses

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// IsUsableResponsesOutput reports whether a Responses payload contains client-usable
// assistant output: non-empty assistant message text or a tool/function call.
// Reasoning-only content is not usable.
func IsUsableResponsesOutput(resp *dto.OpenAIResponsesResponse) bool {
	if resp == nil {
		return false
	}
	if assistantMessageTextFromResponses(resp) != "" {
		return true
	}
	return hasResponsesToolCallOutput(resp)
}

// IsEmptyCompletedResponses reports a soft failure: status is completed and there is
// no usable assistant text or tool call (e.g. reasoning-only Grok completions).
// Incomplete/failed/cancelled statuses are not empty-completed.
func IsEmptyCompletedResponses(resp *dto.OpenAIResponsesResponse) bool {
	if resp == nil {
		return false
	}
	if !strings.EqualFold(responseStatusString(resp), "completed") {
		return false
	}
	return !IsUsableResponsesOutput(resp)
}

func assistantMessageTextFromResponses(resp *dto.OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Output) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, out := range resp.Output {
		if out.Type != responsesOutputTypeMessage {
			continue
		}
		if out.Role != "" && out.Role != "assistant" {
			continue
		}
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

func hasResponsesToolCallOutput(resp *dto.OpenAIResponsesResponse) bool {
	if resp == nil || len(resp.Output) == 0 {
		return false
	}
	for _, out := range resp.Output {
		if isResponsesToolOutputType(out.Type) {
			return true
		}
		// Image generation is a usable Responses tool output even though it is not
		// mapped through the chat function-call converter path.
		if out.Type == dto.ResponsesOutputTypeImageGenerationCall {
			return true
		}
	}
	return false
}
