// ABOUTME: Computes exponential backoff delays for same-channel relay retries.
// ABOUTME: Caps delay growth using configured base and max milliseconds.
package common

import "time"

// SameChannelRetryBackoff returns the wait duration before the given same-channel
// retry attempt. attempt is 0-based (0 = first same-channel retry after a failure).
func SameChannelRetryBackoff(attempt int) time.Duration {
	baseMs := SameChannelRetryBaseDelayMs
	maxMs := SameChannelRetryMaxDelayMs
	if baseMs <= 0 {
		return 0
	}
	if maxMs < baseMs {
		maxMs = baseMs
	}
	if attempt < 0 {
		attempt = 0
	}

	delayMs := baseMs
	for i := 0; i < attempt; i++ {
		if delayMs > maxMs/2 {
			delayMs = maxMs
			break
		}
		delayMs *= 2
	}
	if delayMs > maxMs {
		delayMs = maxMs
	}
	return time.Duration(delayMs) * time.Millisecond
}
