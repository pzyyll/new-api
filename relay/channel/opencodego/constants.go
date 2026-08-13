// ABOUTME: OpenCode Go channel constants: known model list and channel name.
// ABOUTME: Upstream protocol is chosen by the client request, not by model ID.

package opencodego

var ChannelName = "opencodego"

// ModelList is the known opencode-go fixture model IDs.
var ModelList = []string{
	// gpt
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
	// minimax
	"minimax-m2.5",
	"minimax-m2.7",
	"minimax-m3",
	// qwen
	"qwen3.5-plus",
	"qwen3.6-plus",
	"qwen3.7-plus",
	"qwen3.7-max",
	// hy3
	"hy3",
}
