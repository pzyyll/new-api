// ABOUTME: Unit tests for per-channel User-Agent glob matching.
// ABOUTME: Tests the MatchUserAgent method and the underlying globMatch function.
package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
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

	channel, err := GetChannel("default", "gpt-4o", nil, true, "my-client/1.0", "")
	require.NoError(t, err)
	require.NotNil(t, channel)
	if channel.Id != low.Id {
		t.Fatalf("GetChannel returned channel %d, want matching lower-priority channel %d", channel.Id, low.Id)
	}
}

func TestGetRandomSatisfiedChannelExhaustsPriorityBeforeFallback(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			originalMemoryCacheEnabled := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = memoryCacheEnabled
			t.Cleanup(func() {
				common.MemoryCacheEnabled = originalMemoryCacheEnabled
			})
			truncateTables(t)

			highPriority := int64(200)
			lowPriority := int64(100)
			weight := uint(1)
			channels := []*Channel{
				{
					Status:   common.ChannelStatusEnabled,
					Name:     "high-priority-a",
					Key:      "high-key-a",
					Models:   "gpt-4o",
					Group:    "default",
					Priority: &highPriority,
					Weight:   &weight,
				},
				{
					Status:   common.ChannelStatusEnabled,
					Name:     "high-priority-b",
					Key:      "high-key-b",
					Models:   "gpt-4o",
					Group:    "default",
					Priority: &highPriority,
					Weight:   &weight,
				},
				{
					Status:   common.ChannelStatusEnabled,
					Name:     "low-priority",
					Key:      "low-key",
					Models:   "gpt-4o",
					Group:    "default",
					Priority: &lowPriority,
					Weight:   &weight,
				},
			}
			for _, channel := range channels {
				require.NoError(t, channel.Insert())
			}
			if memoryCacheEnabled {
				InitChannelCache()
			}

			excludedChannelIds := make(map[int]struct{})
			first, err := GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, true, "", "")
			require.NoError(t, err)
			require.NotNil(t, first)
			assert.Equal(t, highPriority, first.GetPriority())
			excludedChannelIds[first.Id] = struct{}{}

			second, err := GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, true, "", "")
			require.NoError(t, err)
			require.NotNil(t, second)
			assert.Equal(t, highPriority, second.GetPriority())
			assert.NotEqual(t, first.Id, second.Id)
			excludedChannelIds[second.Id] = struct{}{}

			third, err := GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, true, "", "")
			require.NoError(t, err)
			require.NotNil(t, third)
			assert.Equal(t, lowPriority, third.GetPriority())
			excludedChannelIds[third.Id] = struct{}{}

			exhausted, err := GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, true, "", "")
			require.NoError(t, err)
			assert.Nil(t, exhausted)
		})
	}
}

func TestGetRandomSatisfiedChannelFallsBackToMultiKeyAfterUntriedChannels(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			originalMemoryCacheEnabled := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = memoryCacheEnabled
			t.Cleanup(func() {
				common.MemoryCacheEnabled = originalMemoryCacheEnabled
			})
			truncateTables(t)

			highPriority := int64(200)
			lowPriority := int64(100)
			weight := uint(1)
			multiKey := &Channel{
				Status:   common.ChannelStatusEnabled,
				Name:     "multi-key-high-priority",
				Key:      "key-a\nkey-b",
				Models:   "gpt-4o",
				Group:    "default",
				Priority: &highPriority,
				Weight:   &weight,
				ChannelInfo: ChannelInfo{
					IsMultiKey:   true,
					MultiKeySize: 2,
				},
			}
			require.NoError(t, multiKey.Insert())
			low := &Channel{
				Status:   common.ChannelStatusEnabled,
				Name:     "single-key-low-priority",
				Key:      "low-key",
				Models:   "gpt-4o",
				Group:    "default",
				Priority: &lowPriority,
				Weight:   &weight,
			}
			require.NoError(t, low.Insert())
			if memoryCacheEnabled {
				InitChannelCache()
			}

			excludedChannelIds := map[int]struct{}{multiKey.Id: {}}
			selected, err := GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, true, "", "")
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, low.Id, selected.Id)

			excludedChannelIds[low.Id] = struct{}{}
			selected, err = GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, false, "", "")
			require.NoError(t, err)
			assert.Nil(t, selected)

			selected, err = GetRandomSatisfiedChannel("default", "gpt-4o", excludedChannelIds, true, "", "")
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, multiKey.Id, selected.Id)
		})
	}
}
