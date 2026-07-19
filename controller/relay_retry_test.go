// ABOUTME: Unit tests for same-channel retry eligibility and backoff integration points.
// ABOUTME: Covers transient status codes and transport failure error codes.
package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
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
