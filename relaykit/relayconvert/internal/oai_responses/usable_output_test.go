// ABOUTME: Table tests for Responses usable-output and empty-completed classification.
// ABOUTME: Locks reasoning-only completed as a soft failure and tool-call-only as success.
package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsUsableResponsesOutputAndEmptyCompleted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		resp          *dto.OpenAIResponsesResponse
		wantUsable    bool
		wantEmptyComp bool
	}{
		{
			name:          "nil",
			resp:          nil,
			wantUsable:    false,
			wantEmptyComp: false,
		},
		{
			name: "reasoning only completed",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: []dto.ResponsesOutput{
					{
						Type: responsesOutputTypeReasoning,
						Content: []dto.ResponsesOutputContent{
							{Type: "reasoning_text", Text: "thinking hard"},
						},
					},
				},
			},
			wantUsable:    false,
			wantEmptyComp: true,
		},
		{
			name: "message text completed",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: []dto.ResponsesOutput{
					{
						Type: responsesOutputTypeMessage,
						Role: "assistant",
						Content: []dto.ResponsesOutputContent{
							{Type: "output_text", Text: "hello"},
						},
					},
				},
			},
			wantUsable:    true,
			wantEmptyComp: false,
		},
		{
			name: "function call only completed",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: []dto.ResponsesOutput{
					{
						Type:   responsesOutputTypeFunctionCall,
						Name:   "lookup",
						CallId: "call_1",
					},
				},
			},
			wantUsable:    true,
			wantEmptyComp: false,
		},
		{
			name: "empty message plus reasoning completed",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: []dto.ResponsesOutput{
					{
						Type: responsesOutputTypeReasoning,
						Content: []dto.ResponsesOutputContent{
							{Type: "reasoning_text", Text: "hmm"},
						},
					},
					{
						Type: responsesOutputTypeMessage,
						Role: "assistant",
						Content: []dto.ResponsesOutputContent{
							{Type: "output_text", Text: "   "},
						},
					},
				},
			},
			wantUsable:    false,
			wantEmptyComp: true,
		},
		{
			name: "content filter incomplete",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"incomplete"`),
				IncompleteDetails: &dto.IncompleteDetails{
					Reason: responsesIncompleteReasonContentFilter,
				},
				Output: []dto.ResponsesOutput{},
			},
			wantUsable:    false,
			wantEmptyComp: false,
		},
		{
			name: "custom tool call only completed",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: []dto.ResponsesOutput{
					{
						Type: responsesOutputTypeCustomToolCall,
						Name: "custom_tool",
					},
				},
			},
			wantUsable:    true,
			wantEmptyComp: false,
		},
		{
			name: "image generation call only completed",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: []dto.ResponsesOutput{
					{
						Type: dto.ResponsesOutputTypeImageGenerationCall,
					},
				},
			},
			wantUsable:    true,
			wantEmptyComp: false,
		},
		{
			name: "failed status with no output",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"failed"`),
			},
			wantUsable:    false,
			wantEmptyComp: false,
		},
		{
			name: "completed with empty output",
			resp: &dto.OpenAIResponsesResponse{
				Status: []byte(`"completed"`),
				Output: nil,
			},
			wantUsable:    false,
			wantEmptyComp: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantUsable, IsUsableResponsesOutput(tt.resp))
			assert.Equal(t, tt.wantEmptyComp, IsEmptyCompletedResponses(tt.resp))
		})
	}
}

func TestIsEmptyCompletedResponsesRequiresCompletedStatus(t *testing.T) {
	t.Parallel()
	resp := &dto.OpenAIResponsesResponse{
		Status: []byte(`"cancelled"`),
		Output: []dto.ResponsesOutput{
			{Type: responsesOutputTypeReasoning},
		},
	}
	require.False(t, IsEmptyCompletedResponses(resp))
	require.False(t, IsUsableResponsesOutput(resp))
}
