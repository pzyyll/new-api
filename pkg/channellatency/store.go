// ABOUTME: Maintains per-channel (and model) EWMA of successful stream first-token latency.
// ABOUTME: Supplies latency factors used to bias same-priority channel weights.
package channellatency

import (
	"math"
	"sort"
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

type entry struct {
	mu      sync.Mutex
	ewmaMs  float64
	samples int64
}

type store struct {
	entries sync.Map // key string -> *entry
}

var defaultStore = &store{}

func entryKey(channelId int, model string) string {
	id := strconv.Itoa(channelId)
	if model == "" {
		return id
	}
	return id + "\x00" + model
}

// Observe records a successful stream TTFT sample for the channel (and model).
// No-op when routing is disabled, channelId is 0, or ttftMs is negative.
func Observe(channelId int, model string, ttftMs int64) {
	if !operation_setting.TtftRoutingEnabled {
		return
	}
	if channelId == 0 || ttftMs < 0 {
		return
	}
	operation_setting.ClampTtftRoutingConfig()
	alpha := operation_setting.TtftRoutingEwmaAlpha

	key := entryKey(channelId, model)
	raw, _ := defaultStore.entries.LoadOrStore(key, &entry{})
	e := raw.(*entry)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.samples == 0 {
		e.ewmaMs = float64(ttftMs)
	} else {
		e.ewmaMs = alpha*float64(ttftMs) + (1-alpha)*e.ewmaMs
	}
	e.samples++
}

// Snapshot returns the EWMA average and sample count for the exact
// (channelId, model) key. No cross-model or channel-only fallback.
func Snapshot(channelId int, model string) (avgMs float64, samples int64, ok bool) {
	if channelId == 0 {
		return 0, 0, false
	}
	raw, loaded := defaultStore.entries.Load(entryKey(channelId, model))
	if !loaded {
		return 0, 0, false
	}
	e := raw.(*entry)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.samples == 0 {
		return 0, 0, false
	}
	return e.ewmaMs, e.samples, true
}

// LatencyFactor returns a multiplier for base weight. Cold start and invalid
// averages yield 1.0. Faster than ref => factor > 1 (clamped).
func LatencyFactor(avgMs float64, samples int64, peerRefMs float64) float64 {
	operation_setting.ClampTtftRoutingConfig()
	if samples < operation_setting.TtftRoutingMinSamples || avgMs <= 0 {
		return 1.0
	}
	ref := peerRefMs
	if ref <= 0 {
		ref = float64(operation_setting.TtftRoutingRefMs)
	}
	factor := ref / avgMs
	minF := operation_setting.TtftRoutingMinFactor
	maxF := operation_setting.TtftRoutingMaxFactor
	if factor < minF {
		return minF
	}
	if factor > maxF {
		return maxF
	}
	return factor
}

// EffectiveWeight multiplies base weight by factor. Positive bases stay at least 1.
// Zero base stays 0 so existing zero-weight smoothing can apply.
func EffectiveWeight(baseWeight int, factor float64) int {
	if baseWeight <= 0 {
		return 0
	}
	if factor <= 0 {
		factor = 1
	}
	out := int(math.Round(float64(baseWeight) * factor))
	if out < 1 {
		return 1
	}
	return out
}

// PeerRefMs returns the median avg TTFT among candidates that have enough samples.
// Falls back to 0 when no peer qualifies (caller uses TtftRoutingRefMs).
func PeerRefMs(channelIDs []int, model string) float64 {
	operation_setting.ClampTtftRoutingConfig()
	minSamples := operation_setting.TtftRoutingMinSamples
	avgs := make([]float64, 0, len(channelIDs))
	for _, id := range channelIDs {
		avg, n, ok := Snapshot(id, model)
		if !ok || n < minSamples || avg <= 0 {
			continue
		}
		avgs = append(avgs, avg)
	}
	if len(avgs) == 0 {
		return 0
	}
	sort.Float64s(avgs)
	mid := len(avgs) / 2
	if len(avgs)%2 == 1 {
		return avgs[mid]
	}
	return (avgs[mid-1] + avgs[mid]) / 2
}

// WeightForSelection returns the weight to use for random selection when TTFT
// routing is enabled. When disabled, returns baseWeight unchanged.
func WeightForSelection(channelId int, model string, baseWeight int, peerRefMs float64) int {
	if !operation_setting.TtftRoutingEnabled {
		return baseWeight
	}
	avg, samples, ok := Snapshot(channelId, model)
	if !ok {
		return baseWeight
	}
	factor := LatencyFactor(avg, samples, peerRefMs)
	return EffectiveWeight(baseWeight, factor)
}

// ResetForTest clears the default store. Tests only.
func ResetForTest() {
	defaultStore.entries = sync.Map{}
}
