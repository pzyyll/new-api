// ABOUTME: OpenCode Go channel constants: model list and Anthropic-protocol set.
// ABOUTME: Per-model protocol routing decides OpenAI Chat vs Anthropic Messages upstream.

package opencodego

var ChannelName = "opencodego"

// ModelList is the known opencode-go fixture model IDs (chat only).
var ModelList = []string{
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

func isAnthropicProtocolModel(model string) bool {
	return anthropicProtocolModels[model]
}
