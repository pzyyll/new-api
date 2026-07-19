// ABOUTME: Unit tests for same-channel exponential backoff delay calculation.
// ABOUTME: Verifies base growth, max capping, and non-positive base handling.
package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSameChannelRetryBackoff(t *testing.T) {
	originalBase := SameChannelRetryBaseDelayMs
	originalMax := SameChannelRetryMaxDelayMs
	t.Cleanup(func() {
		SameChannelRetryBaseDelayMs = originalBase
		SameChannelRetryMaxDelayMs = originalMax
	})

	SameChannelRetryBaseDelayMs = 200
	SameChannelRetryMaxDelayMs = 2000

	assert.Equal(t, 200*time.Millisecond, SameChannelRetryBackoff(0))
	assert.Equal(t, 400*time.Millisecond, SameChannelRetryBackoff(1))
	assert.Equal(t, 800*time.Millisecond, SameChannelRetryBackoff(2))
	assert.Equal(t, 1600*time.Millisecond, SameChannelRetryBackoff(3))
	assert.Equal(t, 2000*time.Millisecond, SameChannelRetryBackoff(4))
	assert.Equal(t, 2000*time.Millisecond, SameChannelRetryBackoff(5))

	SameChannelRetryBaseDelayMs = 0
	assert.Equal(t, time.Duration(0), SameChannelRetryBackoff(0))
}
