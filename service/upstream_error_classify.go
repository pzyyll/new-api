// ABOUTME: Classifies OpenAI-style upstream errors for capacity/null-code soft failures.
// ABOUTME: Normalizes capacity messages into retryable upstream_capacity / HTTP 503.
package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// SoftFailKind is the soft-failure class derived from upstream error payloads.
type SoftFailKind string

const (
	SoftFailKindNone     SoftFailKind = ""
	SoftFailKindCapacity SoftFailKind = "upstream_capacity"

	// SoftFailClassEmptyCompleted is the admin/log marker for empty completed responses.
	SoftFailClassEmptyCompleted = "empty_completed"
	// SoftFailClassUpstreamCapacity is the admin/log marker for capacity soft fails.
	SoftFailClassUpstreamCapacity = "upstream_capacity"

	ginKeyChannelAffinityUnusable = "channel_affinity_unusable"
	ginKeySoftFailClass           = "soft_fail_class"
	ginKeyAffinityCleared         = "affinity_cleared"
	ginKeyClientPayloadWritten    = "client_payload_written"
)

// IsSoftFailErrorCode reports internal soft-failure codes that must retry across
// channels, clear affinity, and never auto-ban.
func IsSoftFailErrorCode(code types.ErrorCode) bool {
	switch code {
	case types.ErrorCodeEmptyResponse, types.ErrorCodeUpstreamCapacity:
		return true
	default:
		return false
	}
}

// IsNullOrUnknownUpstreamCode reports codes that are effectively absent/unknown.
func IsNullOrUnknownUpstreamCode(code any) bool {
	if code == nil {
		return true
	}
	switch v := code.(type) {
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		return s == "" || s == "null" || s == "unknown_error" || s == "<nil>"
	case types.ErrorCode:
		return IsNullOrUnknownUpstreamCode(string(v))
	case fmt.Stringer:
		return IsNullOrUnknownUpstreamCode(v.String())
	default:
		s := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", v)))
		return s == "" || s == "null" || s == "unknown_error" || s == "<nil>"
	}
}

// HasUpstreamCapacityKeywords reports whether message text matches configured
// capacity keywords (option UpstreamCapacityKeywords; case-insensitive substrings).
func HasUpstreamCapacityKeywords(message string) bool {
	lower := strings.ToLower(message)
	if lower == "" {
		return false
	}
	for _, keyword := range operation_setting.UpstreamCapacityKeywords {
		if keyword == "" {
			continue
		}
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func isCapacityRetryableHTTPStatus(status int) bool {
	switch status {
	case 0, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	default:
		return false
	}
}

// ClassifyUpstreamOpenAIError classifies an OpenAI-style upstream error as a soft fail.
// Capacity requires keyword match plus null/unknown code and/or a retryable status.
func ClassifyUpstreamOpenAIError(message string, code any, httpStatus int) SoftFailKind {
	if !HasUpstreamCapacityKeywords(message) {
		return SoftFailKindNone
	}
	if IsNullOrUnknownUpstreamCode(code) || isCapacityRetryableHTTPStatus(httpStatus) {
		return SoftFailKindCapacity
	}
	return SoftFailKindNone
}

// NormalizeSoftUpstreamError rewrites capacity soft failures to a stable internal
// code and HTTP 503 while preserving the upstream message text.
func NormalizeSoftUpstreamError(err *types.NewAPIError) *types.NewAPIError {
	if err == nil {
		return nil
	}

	message := err.Error()
	var code any = err.GetErrorCode()
	if openAIErr, ok := err.RelayError.(types.OpenAIError); ok {
		if openAIErr.Message != "" {
			message = openAIErr.Message
		}
		if openAIErr.Code != nil {
			code = openAIErr.Code
		}
	}

	kind := ClassifyUpstreamOpenAIError(message, code, err.StatusCode)
	if kind != SoftFailKindCapacity {
		return err
	}

	normalized := types.NewOpenAIError(errors.New(message), types.ErrorCodeUpstreamCapacity, http.StatusServiceUnavailable)
	if openAIErr, ok := err.RelayError.(types.OpenAIError); ok {
		openAIErr.Message = message
		openAIErr.Code = string(types.ErrorCodeUpstreamCapacity)
		if openAIErr.Type == "" {
			openAIErr.Type = "upstream_error"
		}
		normalized.RelayError = openAIErr
		normalized.Metadata = openAIErr.Metadata
	}
	return normalized
}

// SoftFailClassFromErrorCode maps an error code to the soft-fail admin marker class.
func SoftFailClassFromErrorCode(code types.ErrorCode) string {
	switch code {
	case types.ErrorCodeEmptyResponse:
		return SoftFailClassEmptyCompleted
	case types.ErrorCodeUpstreamCapacity:
		return SoftFailClassUpstreamCapacity
	default:
		return ""
	}
}
