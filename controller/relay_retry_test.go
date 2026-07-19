// ABOUTME: Unit tests for same-channel retry eligibility and backoff integration points.
// ABOUTME: Covers transient status codes, soft fails, and transport failure error codes.
package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestShouldSameChannelRetryIncludesDoRequestFailed(t *testing.T) {
	err := types.NewOpenAIError(errors.New("dial timeout"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	require.True(t, shouldSameChannelRetry(nil, err))

	upstream500 := types.NewOpenAIError(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	require.False(t, shouldSameChannelRetry(nil, upstream500))

	taskTransport := &dto.TaskError{Code: string(types.ErrorCodeDoRequestFailed), StatusCode: http.StatusInternalServerError}
	require.True(t, shouldSameChannelRetryTask(nil, taskTransport))

	taskUpstream500 := &dto.TaskError{Code: "upstream_error", StatusCode: http.StatusInternalServerError}
	require.False(t, shouldSameChannelRetryTask(nil, taskUpstream500))
}

func TestSoftFailRetryPolicy(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	capacity := types.NewOpenAIError(errors.New("at capacity"), types.ErrorCodeUpstreamCapacity, http.StatusServiceUnavailable)
	empty := types.NewOpenAIError(errors.New("empty completed"), types.ErrorCodeEmptyResponse, http.StatusServiceUnavailable)

	base := httptest.NewRecorder()
	cBase, _ := gin.CreateTestContext(base)

	require.True(t, shouldSameChannelRetry(cBase, capacity))
	require.False(t, shouldSameChannelRetry(cBase, empty))
	require.True(t, shouldRetry(cBase, capacity, 1))
	require.True(t, shouldRetry(cBase, empty, 1))
	require.False(t, shouldRetry(cBase, empty, 0))

	// Affinity SkipRetryOnFailure is overridden for soft fails.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("channel_affinity_skip_retry_on_failure", true)
	require.True(t, service.ShouldSkipRetryAfterChannelAffinityFailure(c))
	require.True(t, shouldRetry(c, capacity, 1))
	require.True(t, shouldRetry(c, empty, 1))
	require.True(t, shouldSameChannelRetry(c, capacity))
	require.False(t, shouldSameChannelRetry(c, empty))

	// Non-soft fail still respects affinity skip.
	upstream500 := types.NewOpenAIError(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	require.False(t, shouldRetry(c, upstream500, 1))
	require.False(t, shouldSameChannelRetry(c, upstream500))

	// Committed client payload blocks same-request soft-fail retries.
	wCommitted := httptest.NewRecorder()
	cCommitted, _ := gin.CreateTestContext(wCommitted)
	cCommitted.Set("client_payload_written", true)
	require.False(t, shouldRetry(cCommitted, capacity, 1))
	require.False(t, shouldRetry(cCommitted, empty, 1))
	require.False(t, shouldSameChannelRetry(cCommitted, capacity))
}

func TestSoftFailShouldNotDisableChannel(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old })

	capacity := types.NewOpenAIError(errors.New("at capacity"), types.ErrorCodeUpstreamCapacity, http.StatusServiceUnavailable)
	empty := types.NewOpenAIError(errors.New("empty completed"), types.ErrorCodeEmptyResponse, http.StatusServiceUnavailable)
	require.False(t, service.ShouldDisableChannel(capacity))
	require.False(t, service.ShouldDisableChannel(empty))
}
