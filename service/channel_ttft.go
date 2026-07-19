// ABOUTME: Records successful stream first-token latency for channel routing bias.
// ABOUTME: Keys stats by channel id and origin model name (e.g. gpt-4o).
package service

import (
	"github.com/QuantumNous/new-api/pkg/channellatency"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ObserveChannelTtftFromRelay stores TTFT for (channel_id, client_model_name)
// after a successful stream response with a real first token.
// Uses ClientModelName so model mapping / compact rewrites of OriginModelName
// cannot poison the routing key.
func ObserveChannelTtftFromRelay(info *relaycommon.RelayInfo) {
	if info == nil || !info.IsStream || !info.HasSendResponse() {
		return
	}
	channelId := 0
	if info.ChannelMeta != nil {
		channelId = info.ChannelId
	}
	if channelId == 0 {
		return
	}
	modelName := info.ClientModelName
	if modelName == "" {
		modelName = info.OriginModelName
	}
	ttftMs := info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
	channellatency.Observe(channelId, modelName, ttftMs)
}
