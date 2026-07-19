// ABOUTME: Configures latency-aware (TTFT) weighting for same-priority channel picks.
// ABOUTME: Defaults keep the feature off until operators enable it.
package operation_setting

// TtftRoutingEnabled multiplies configured channel weights by a TTFT factor
// within the same priority only. Off by default.
var TtftRoutingEnabled = false

// TtftRoutingMinSamples is the minimum EWMA samples before a channel's TTFT
// biases selection. Below this, factor is 1.0.
var TtftRoutingMinSamples int64 = 20

// TtftRoutingEwmaAlpha is the EWMA smoothing factor in (0, 1]. Higher reacts faster.
var TtftRoutingEwmaAlpha = 0.2

// TtftRoutingMinFactor / MaxFactor clamp the latency multiplier.
var TtftRoutingMinFactor = 0.5
var TtftRoutingMaxFactor = 2.0

// TtftRoutingRefMs is the fallback reference TTFT (ms) when peer median is unavailable.
var TtftRoutingRefMs int64 = 500

func ClampTtftRoutingConfig() {
	if TtftRoutingMinSamples < 1 {
		TtftRoutingMinSamples = 1
	}
	if TtftRoutingEwmaAlpha <= 0 || TtftRoutingEwmaAlpha > 1 {
		TtftRoutingEwmaAlpha = 0.2
	}
	if TtftRoutingMinFactor <= 0 {
		TtftRoutingMinFactor = 0.5
	}
	if TtftRoutingMaxFactor < TtftRoutingMinFactor {
		TtftRoutingMaxFactor = TtftRoutingMinFactor
	}
	if TtftRoutingRefMs < 1 {
		TtftRoutingRefMs = 500
	}
}
