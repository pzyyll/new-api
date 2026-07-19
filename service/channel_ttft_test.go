// ABOUTME: Tests TTFT observation gates for latency-aware channel routing.
// ABOUTME: Ensures only successful streams with first token update (channel, model) stats.
package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/channellatency"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObserveChannelTtftFromRelayGates(t *testing.T) {
	channellatency.ResetForTest()
	origEnabled := operation_setting.TtftRoutingEnabled
	origMin := operation_setting.TtftRoutingMinSamples
	origAlpha := operation_setting.TtftRoutingEwmaAlpha
	t.Cleanup(func() {
		operation_setting.TtftRoutingEnabled = origEnabled
		operation_setting.TtftRoutingMinSamples = origMin
		operation_setting.TtftRoutingEwmaAlpha = origAlpha
		channellatency.ResetForTest()
	})

	operation_setting.TtftRoutingEnabled = true
	operation_setting.TtftRoutingMinSamples = 1
	operation_setting.TtftRoutingEwmaAlpha = 1.0

	start := time.Now().Add(-800 * time.Millisecond)
	first := time.Now()

	// Disabled: no sample.
	operation_setting.TtftRoutingEnabled = false
	ObserveChannelTtftFromRelay(&relaycommon.RelayInfo{
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: first,
		ClientModelName:   "gpt-4o",
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelId: 11},
	})
	_, _, ok := channellatency.Snapshot(11, "gpt-4o")
	assert.False(t, ok)

	operation_setting.TtftRoutingEnabled = true

	// Non-stream: no sample.
	ObserveChannelTtftFromRelay(&relaycommon.RelayInfo{
		IsStream:          false,
		StartTime:         start,
		FirstResponseTime: first,
		ClientModelName:   "gpt-4o",
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelId: 11},
	})
	_, _, ok = channellatency.Snapshot(11, "gpt-4o")
	assert.False(t, ok)

	// No first token (FirstResponseTime not after StartTime): no sample.
	ObserveChannelTtftFromRelay(&relaycommon.RelayInfo{
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start.Add(-time.Second),
		ClientModelName:   "gpt-4o",
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelId: 11},
	})
	_, _, ok = channellatency.Snapshot(11, "gpt-4o")
	assert.False(t, ok)

	// Zero channel: no sample.
	ObserveChannelTtftFromRelay(&relaycommon.RelayInfo{
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: first,
		ClientModelName:   "gpt-4o",
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelId: 0},
	})
	_, _, ok = channellatency.Snapshot(11, "gpt-4o")
	assert.False(t, ok)

	// Success stream with ClientModelName: records under client model, not OriginModelName.
	ObserveChannelTtftFromRelay(&relaycommon.RelayInfo{
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: first,
		ClientModelName:   "gpt-4o",
		OriginModelName:   "gpt-4o-mapped-upstream",
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelId: 11},
	})
	avg, n, ok := channellatency.Snapshot(11, "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, int64(1), n)
	assert.Greater(t, avg, 0.0)
	_, _, okMapped := channellatency.Snapshot(11, "gpt-4o-mapped-upstream")
	assert.False(t, okMapped)
}
