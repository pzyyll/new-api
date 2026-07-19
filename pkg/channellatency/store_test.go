// ABOUTME: Unit tests for channel TTFT EWMA storage and weight factor math.
// ABOUTME: Covers cold start, clamping, peer median, and effective weights.
package channellatency

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLatencyFactorColdStartAndClamp(t *testing.T) {
	origMin := operation_setting.TtftRoutingMinSamples
	origMinF := operation_setting.TtftRoutingMinFactor
	origMaxF := operation_setting.TtftRoutingMaxFactor
	origRef := operation_setting.TtftRoutingRefMs
	t.Cleanup(func() {
		operation_setting.TtftRoutingMinSamples = origMin
		operation_setting.TtftRoutingMinFactor = origMinF
		operation_setting.TtftRoutingMaxFactor = origMaxF
		operation_setting.TtftRoutingRefMs = origRef
	})

	operation_setting.TtftRoutingMinSamples = 5
	operation_setting.TtftRoutingMinFactor = 0.5
	operation_setting.TtftRoutingMaxFactor = 2.0
	operation_setting.TtftRoutingRefMs = 500

	assert.Equal(t, 1.0, LatencyFactor(100, 4, 0))
	assert.InDelta(t, 2.0, LatencyFactor(100, 5, 0), 1e-9)  // 500/100=5 clamped to 2
	assert.InDelta(t, 0.5, LatencyFactor(2000, 5, 0), 1e-9) // 500/2000=0.25 clamped to 0.5
	assert.InDelta(t, 1.0, LatencyFactor(500, 5, 0), 1e-9)
	assert.InDelta(t, 2.0, LatencyFactor(100, 5, 200), 1e-9) // peer ref 200 -> 2.0
}

func TestEffectiveWeight(t *testing.T) {
	assert.Equal(t, 0, EffectiveWeight(0, 2))
	assert.Equal(t, 20, EffectiveWeight(10, 2))
	assert.Equal(t, 1, EffectiveWeight(1, 0.1))
	assert.Equal(t, 10, EffectiveWeight(10, 0))
}

func TestObserveAndWeightForSelection(t *testing.T) {
	ResetForTest()
	origEnabled := operation_setting.TtftRoutingEnabled
	origMin := operation_setting.TtftRoutingMinSamples
	origAlpha := operation_setting.TtftRoutingEwmaAlpha
	origRef := operation_setting.TtftRoutingRefMs
	t.Cleanup(func() {
		operation_setting.TtftRoutingEnabled = origEnabled
		operation_setting.TtftRoutingMinSamples = origMin
		operation_setting.TtftRoutingEwmaAlpha = origAlpha
		operation_setting.TtftRoutingRefMs = origRef
		ResetForTest()
	})

	operation_setting.TtftRoutingEnabled = true
	operation_setting.TtftRoutingMinSamples = 3
	operation_setting.TtftRoutingEwmaAlpha = 1.0 // last sample wins for determinism
	operation_setting.TtftRoutingRefMs = 500
	operation_setting.TtftRoutingMinFactor = 0.5
	operation_setting.TtftRoutingMaxFactor = 2.0

	for i := 0; i < 3; i++ {
		Observe(7, "gpt-4o", 100)
	}
	avg, n, ok := Snapshot(7, "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, int64(3), n)
	assert.InDelta(t, 100.0, avg, 1e-9)

	// Fast channel should get higher effective weight than base 10.
	assert.Equal(t, 20, WeightForSelection(7, "gpt-4o", 10, 0))

	operation_setting.TtftRoutingEnabled = false
	assert.Equal(t, 10, WeightForSelection(7, "gpt-4o", 10, 0))
}

func TestSnapshotNoChannelOnlyFallback(t *testing.T) {
	ResetForTest()
	origEnabled := operation_setting.TtftRoutingEnabled
	origMin := operation_setting.TtftRoutingMinSamples
	origAlpha := operation_setting.TtftRoutingEwmaAlpha
	t.Cleanup(func() {
		operation_setting.TtftRoutingEnabled = origEnabled
		operation_setting.TtftRoutingMinSamples = origMin
		operation_setting.TtftRoutingEwmaAlpha = origAlpha
		ResetForTest()
	})

	operation_setting.TtftRoutingEnabled = true
	operation_setting.TtftRoutingMinSamples = 1
	operation_setting.TtftRoutingEwmaAlpha = 1.0

	// Channel-only samples must not leak into a named model key.
	Observe(9, "", 100)
	_, _, ok := Snapshot(9, "gpt-4o")
	assert.False(t, ok)

	// Different models on the same channel stay independent.
	Observe(9, "gpt-4o", 100)
	Observe(9, "claude-3.5", 2000)
	avg4o, n4o, ok4o := Snapshot(9, "gpt-4o")
	avgClaude, nClaude, okClaude := Snapshot(9, "claude-3.5")
	require.True(t, ok4o)
	require.True(t, okClaude)
	assert.Equal(t, int64(1), n4o)
	assert.Equal(t, int64(1), nClaude)
	assert.InDelta(t, 100.0, avg4o, 1e-9)
	assert.InDelta(t, 2000.0, avgClaude, 1e-9)
}

func TestPeerRefMsMedian(t *testing.T) {
	ResetForTest()
	origEnabled := operation_setting.TtftRoutingEnabled
	origMin := operation_setting.TtftRoutingMinSamples
	origAlpha := operation_setting.TtftRoutingEwmaAlpha
	t.Cleanup(func() {
		operation_setting.TtftRoutingEnabled = origEnabled
		operation_setting.TtftRoutingMinSamples = origMin
		operation_setting.TtftRoutingEwmaAlpha = origAlpha
		ResetForTest()
	})

	operation_setting.TtftRoutingEnabled = true
	operation_setting.TtftRoutingMinSamples = 2
	operation_setting.TtftRoutingEwmaAlpha = 1.0

	for i := 0; i < 2; i++ {
		Observe(1, "m", 100)
		Observe(2, "m", 300)
		Observe(3, "m", 500)
	}
	assert.InDelta(t, 300.0, PeerRefMs([]int{1, 2, 3}, "m"), 1e-9)
	assert.Equal(t, 0.0, PeerRefMs([]int{99}, "m"))
}
