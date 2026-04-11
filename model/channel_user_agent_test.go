// ABOUTME: Unit tests for per-channel User-Agent glob matching.
// ABOUTME: Tests the MatchUserAgent method and the underlying globMatch function.
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		// Basic wildcard
		{"codex*", "codex-cli/1.0.0", true},
		{"codex*", "Codex-CLI/1.0.0", true}, // case-insensitive
		{"codex*", "my-codex-tool", false},  // no match, codex not at start
		{"*codex*", "my-codex-tool", true},  // match with surrounding wildcards

		// Question mark
		{"codex-?", "codex-a", true},
		{"codex-?", "codex-ab", false},

		// Exact match
		{"codex-cli", "codex-cli", true},
		{"codex-cli", "Codex-CLI", true}, // case-insensitive
		{"codex-cli", "codex-cli/1.0", false},

		// Star matches slash (unlike path.Match)
		{"codex*", "codex-cli/1.0.0/beta", true},

		// Empty pattern and input
		{"", "", true},
		{"*", "", true},
		{"*", "anything", true},
		{"?", "", false},

		// Multiple stars
		{"*codex*claude*", "my-codex-and-claude-tool", true},
		{"*codex*claude*", "my-claude-and-codex-tool", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.input, func(t *testing.T) {
			got := globMatch(tt.pattern, tt.input)
			if got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.input, got, tt.want)
			}
		})
	}
}

func TestChannelMatchUserAgent(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name      string
		userAgent *string
		clientUA  string
		want      bool
	}{
		{"nil field accepts all", nil, "anything", true},
		{"empty string accepts all", strPtr(""), "anything", true},
		{"whitespace-only accepts all", strPtr("  "), "anything", true},
		{"single pattern match", strPtr("codex*"), "codex-cli/1.0.0", true},
		{"single pattern no match", strPtr("codex*"), "claude-code/2.0", false},
		{"multiple patterns first matches", strPtr("codex*,claude-code*"), "codex-cli/1.0", true},
		{"multiple patterns second matches", strPtr("codex*,claude-code*"), "claude-code/2.0", true},
		{"multiple patterns none match", strPtr("codex*,claude-code*"), "curl/7.0", false},
		{"patterns with whitespace", strPtr(" codex* , claude-code* "), "codex-cli/1.0", true},
		{"empty client UA with pattern", strPtr("codex*"), "", false},
		{"empty client UA without pattern", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &Channel{UserAgent: tt.userAgent}
			got := ch.MatchUserAgent(tt.clientUA)
			if got != tt.want {
				t.Errorf("MatchUserAgent(%q) = %v, want %v", tt.clientUA, got, tt.want)
			}
		})
	}
}

func TestGetChannel_DBFallbackSkipsHigherPriorityNonMatchingChannel(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Ability{}))
	initCol()
	truncateTables(t)

	priorityHigh := int64(200)
	priorityLow := int64(100)
	weightHigh := uint(100)
	weightLow := uint(1)
	highUA := "other-client*"
	lowUA := "my-client*"

	high := &Channel{
		Status:    common.ChannelStatusEnabled,
		Name:      "high-priority-non-matching",
		Key:       "high-key",
		Models:    "gpt-4o",
		Group:     "default",
		Priority:  &priorityHigh,
		Weight:    &weightHigh,
		UserAgent: &highUA,
	}
	require.NoError(t, high.Insert())

	low := &Channel{
		Status:    common.ChannelStatusEnabled,
		Name:      "low-priority-matching",
		Key:       "low-key",
		Models:    "gpt-4o",
		Group:     "default",
		Priority:  &priorityLow,
		Weight:    &weightLow,
		UserAgent: &lowUA,
	}
	require.NoError(t, low.Insert())

	channel, err := GetChannel("default", "gpt-4o", 0, "my-client/1.0")
	require.NoError(t, err)
	require.NotNil(t, channel)
	if channel.Id != low.Id {
		t.Fatalf("GetChannel returned channel %d, want matching lower-priority channel %d", channel.Id, low.Id)
	}
}
