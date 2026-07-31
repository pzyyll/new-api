// ABOUTME: OpenCode Go channel constants: model list and per-model protocol sets.
// ABOUTME: Protocol routing decides Anthropic Messages vs OpenAI Responses vs Chat upstream.

package opencodego

import (
	"github.com/QuantumNous/new-api/setting/reasoning"
)

var ChannelName = "opencodego"

// ModelList is the known opencode-go fixture model IDs.
var ModelList = []string{
	// gpt (Responses API)
	"gpt-5.6-luna",
	// grok
	"grok-4.5",
	// deepseek
	"deepseek-v4-flash",
	"deepseek-v4-pro",
	// glm
	"glm-5",
	"glm-5.1",
	"glm-5.2",
	// kimi
	"kimi-k2.5",
	"kimi-k2.6",
	"kimi-k2.7-code",
	// mimo
	"mimo-v2-omni",
	"mimo-v2-pro",
	"mimo-v2.5",
	"mimo-v2.5-pro",
	// minimax (Anthropic protocol)
	"minimax-m2.5",
	"minimax-m2.7",
	"minimax-m3",
	// qwen (Anthropic protocol)
	"qwen3.5-plus",
	"qwen3.6-plus",
	"qwen3.7-plus",
	"qwen3.7-max",
	// hy3
	"hy3",
}

// anthropicProtocolModels are upstream models that must use Anthropic Messages
// (`/messages` + x-api-key). All other models use OpenAI Chat Completions.
var anthropicProtocolModels = map[string]bool{
	"minimax-m2.5": true,
	"minimax-m2.7": true,
	"minimax-m3":   true,
	"qwen3.5-plus": true,
	"qwen3.6-plus": true,
	"qwen3.7-plus": true,
	"qwen3.7-max":  true,
}

// responsesProtocolModels are upstream models that must use the OpenAI Responses
// API (`/v1/responses` + Bearer auth). All other non-Anthropic models use Chat Completions.
var responsesProtocolModels = map[string]bool{
	"gpt-5.6-luna": true,
}

func isAnthropicProtocolModel(model string) bool {
	return anthropicProtocolModels[model]
}

// isResponsesProtocolModel matches with the new-api reasoning-effort suffix
// convention (e.g. gpt-5.6-luna-high) so URL routing and conversion checks see
// the base model, mirroring how the openai channel resolves effort variants.
func isResponsesProtocolModel(model string) bool {
	baseModel, _, _ := reasoning.TrimEffortSuffixWithSuffixes(model, reasoning.OpenAIEffortSuffixes)
	return responsesProtocolModels[baseModel]
}
