// ABOUTME: Unit tests for capacity/null-code upstream error classification.
// ABOUTME: Covers the xAI capacity fixture and non-capacity false-positive guards.
package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const xaiCapacityMessage = "Error Code null: The model is currently at capacity due to high demand. Please try again in a few minutes, or use a higher service tier for priority processing: https://docs.x.ai/developers/advanced-api-usage/priority-processing"

func TestIsNullOrUnknownUpstreamCode(t *testing.T) {
	t.Parallel()
	assert.True(t, IsNullOrUnknownUpstreamCode(nil))
	assert.True(t, IsNullOrUnknownUpstreamCode(""))
	assert.True(t, IsNullOrUnknownUpstreamCode("null"))
	assert.True(t, IsNullOrUnknownUpstreamCode("NULL"))
	assert.True(t, IsNullOrUnknownUpstreamCode("unknown_error"))
	assert.False(t, IsNullOrUnknownUpstreamCode("rate_limit_exceeded"))
	assert.False(t, IsNullOrUnknownUpstreamCode("invalid_api_key"))
}

func TestClassifyUpstreamOpenAIErrorCapacity(t *testing.T) {
	// Not parallel: shares package-level UpstreamCapacityKeywords with config tests.

	tests := []struct {
		name       string
		message    string
		code       any
		status     int
		wantKind   SoftFailKind
	}{
		{
			name:     "xai capacity null code status 500",
			message:  xaiCapacityMessage,
			code:     nil,
			status:   http.StatusInternalServerError,
			wantKind: SoftFailKindCapacity,
		},
		{
			name:     "xai capacity null string status 503",
			message:  xaiCapacityMessage,
			code:     "null",
			status:   http.StatusServiceUnavailable,
			wantKind: SoftFailKindCapacity,
		},
		{
			name:     "xai capacity unknown_error status 429",
			message:  xaiCapacityMessage,
			code:     "unknown_error",
			status:   http.StatusTooManyRequests,
			wantKind: SoftFailKindCapacity,
		},
		{
			name:     "capacity keywords with real code but 503",
			message:  "model is overloaded",
			code:     "server_error",
			status:   http.StatusServiceUnavailable,
			wantKind: SoftFailKindCapacity,
		},
		{
			name:     "null code invalid api key 401",
			message:  "invalid api key",
			code:     nil,
			status:   http.StatusUnauthorized,
			wantKind: SoftFailKindNone,
		},
		{
			name:     "null code empty message 500",
			message:  "",
			code:     nil,
			status:   http.StatusInternalServerError,
			wantKind: SoftFailKindNone,
		},
		{
			name:     "normal 429 rate limit without capacity keywords",
			message:  "Rate limit reached for requests",
			code:     "rate_limit_exceeded",
			status:   http.StatusTooManyRequests,
			wantKind: SoftFailKindNone,
		},
		{
			name:     "capacity keywords with non-null code and 400",
			message:  "at capacity",
			code:     "invalid_request",
			status:   http.StatusBadRequest,
			wantKind: SoftFailKindNone,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantKind, ClassifyUpstreamOpenAIError(tt.message, tt.code, tt.status))
		})
	}
}

func TestNormalizeSoftUpstreamErrorCapacity(t *testing.T) {
	// Not parallel: shares package-level UpstreamCapacityKeywords with config tests.

	original := types.WithOpenAIError(types.OpenAIError{
		Message: xaiCapacityMessage,
		Type:    "upstream_error",
		Code:    nil,
	}, http.StatusInternalServerError)

	normalized := NormalizeSoftUpstreamError(original)
	require.NotNil(t, normalized)
	assert.Equal(t, types.ErrorCodeUpstreamCapacity, normalized.GetErrorCode())
	assert.Equal(t, http.StatusServiceUnavailable, normalized.StatusCode)
	assert.Contains(t, normalized.Error(), "at capacity")
	assert.True(t, IsSoftFailErrorCode(normalized.GetErrorCode()))
}

func TestNormalizeSoftUpstreamErrorLeavesNonCapacity(t *testing.T) {
	t.Parallel()

	original := types.NewOpenAIError(errors.New("Rate limit reached"), types.ErrorCode("rate_limit_exceeded"), http.StatusTooManyRequests)
	normalized := NormalizeSoftUpstreamError(original)
	require.NotNil(t, normalized)
	assert.Equal(t, types.ErrorCode("rate_limit_exceeded"), normalized.GetErrorCode())
	assert.Equal(t, http.StatusTooManyRequests, normalized.StatusCode)
}

func TestUpstreamCapacityKeywordsAreConfigurable(t *testing.T) {
	orig := append([]string(nil), operation_setting.UpstreamCapacityKeywords...)
	t.Cleanup(func() {
		operation_setting.UpstreamCapacityKeywords = orig
	})

	operation_setting.UpstreamCapacityKeywordsFromString("custom-overload-token\n")
	require.True(t, HasUpstreamCapacityKeywords("provider says custom-overload-token now"))
	require.False(t, HasUpstreamCapacityKeywords("at capacity due to high demand"))
	require.Equal(t, SoftFailKindCapacity, ClassifyUpstreamOpenAIError(
		"hit custom-overload-token", nil, http.StatusInternalServerError,
	))
}
