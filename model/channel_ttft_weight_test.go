// ABOUTME: Regression tests for TTFT-biased same-priority channel selection.
// ABOUTME: Verifies channel+model keys and that faster channels get higher pick share.
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/channellatency"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChannelPrefersFasterTtftWithinSamePriority(t *testing.T) {
	origEnabled := operation_setting.TtftRoutingEnabled
	origMin := operation_setting.TtftRoutingMinSamples
	origAlpha := operation_setting.TtftRoutingEwmaAlpha
	origRef := operation_setting.TtftRoutingRefMs
	origMinF := operation_setting.TtftRoutingMinFactor
	origMaxF := operation_setting.TtftRoutingMaxFactor
	origCache := common.MemoryCacheEnabled
	t.Cleanup(func() {
		operation_setting.TtftRoutingEnabled = origEnabled
		operation_setting.TtftRoutingMinSamples = origMin
		operation_setting.TtftRoutingEwmaAlpha = origAlpha
		operation_setting.TtftRoutingRefMs = origRef
		operation_setting.TtftRoutingMinFactor = origMinF
		operation_setting.TtftRoutingMaxFactor = origMaxF
		common.MemoryCacheEnabled = origCache
		channellatency.ResetForTest()
	})

	common.MemoryCacheEnabled = false
	operation_setting.TtftRoutingEnabled = true
	operation_setting.TtftRoutingMinSamples = 3
	operation_setting.TtftRoutingEwmaAlpha = 1.0
	operation_setting.TtftRoutingRefMs = 500
	operation_setting.TtftRoutingMinFactor = 0.5
	operation_setting.TtftRoutingMaxFactor = 2.0
	channellatency.ResetForTest()
	truncateTables(t)

	priority := int64(100)
	weight := uint(10)
	fast := &Channel{
		Status:   common.ChannelStatusEnabled,
		Name:     "fast-ttft",
		Key:      "fast-key",
		Models:   "gpt-4o",
		Group:    "default",
		Priority: &priority,
		Weight:   &weight,
	}
	slow := &Channel{
		Status:   common.ChannelStatusEnabled,
		Name:     "slow-ttft",
		Key:      "slow-key",
		Models:   "gpt-4o",
		Group:    "default",
		Priority: &priority,
		Weight:   &weight,
	}
	require.NoError(t, fast.Insert())
	require.NoError(t, slow.Insert())

	for i := 0; i < 3; i++ {
		channellatency.Observe(fast.Id, "gpt-4o", 100)
		channellatency.Observe(slow.Id, "gpt-4o", 2000)
	}

	// Independent model key: samples on claude must not affect gpt-4o weights.
	for i := 0; i < 3; i++ {
		channellatency.Observe(slow.Id, "claude-3.5", 50)
	}
	assert.Equal(t, 20, channellatency.WeightForSelection(fast.Id, "gpt-4o", 10, 0))
	assert.Equal(t, 5, channellatency.WeightForSelection(slow.Id, "gpt-4o", 10, 0))

	const trials = 200
	fastWins := 0
	for i := 0; i < trials; i++ {
		selected, err := GetChannel("default", "gpt-4o", nil, true, "", "")
		require.NoError(t, err)
		require.NotNil(t, selected)
		if selected.Id == fast.Id {
			fastWins++
		}
	}
	// Expected share ~ 30/45 ≈ 0.67 with +10 smoothing; require a clear majority.
	assert.Greater(t, fastWins, trials*3/5)
}
