// ABOUTME: Unit tests for same-channel retry eligibility and backoff integration points.
// ABOUTME: Covers transient status codes that may stay on the same channel.
package controller

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSameChannelRetryStatusCode(t *testing.T) {
	assert.True(t, isSameChannelRetryStatusCode(0))
	assert.True(t, isSameChannelRetryStatusCode(429))
	assert.True(t, isSameChannelRetryStatusCode(http.StatusBadGateway))
	assert.True(t, isSameChannelRetryStatusCode(http.StatusServiceUnavailable))
	assert.False(t, isSameChannelRetryStatusCode(http.StatusBadRequest))
	assert.False(t, isSameChannelRetryStatusCode(http.StatusUnauthorized))
	assert.False(t, isSameChannelRetryStatusCode(http.StatusInternalServerError))
	assert.False(t, isSameChannelRetryStatusCode(http.StatusGatewayTimeout))
}
