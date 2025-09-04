package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTokenUsage(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Extract complete token usage", func(t *testing.T) {
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    50,
			TotalTokenCount:         150,
			CachedContentTokenCount: 25,
		}

		usage := client.extractTokenUsage(metadata)

		assert.Equal(t, int64(100), usage.InputTokens)
		assert.Equal(t, int64(50), usage.OutputTokens)
		assert.Equal(t, int64(25), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.CacheCreationTokens) // Gemini doesn't provide this
	})

	t.Run("Extract token usage with zero values", func(t *testing.T) {
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        0,
			CandidatesTokenCount:    0,
			TotalTokenCount:         0,
			CachedContentTokenCount: 0,
		}

		usage := client.extractTokenUsage(metadata)

		assert.Equal(t, int64(0), usage.InputTokens)
		assert.Equal(t, int64(0), usage.OutputTokens)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.CacheCreationTokens)
	})

	t.Run("Extract token usage with partial data", func(t *testing.T) {
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        75,
			CandidatesTokenCount:    0, // No output tokens
			TotalTokenCount:         75,
			CachedContentTokenCount: 0, // No cached content
		}

		usage := client.extractTokenUsage(metadata)

		assert.Equal(t, int64(75), usage.InputTokens)
		assert.Equal(t, int64(0), usage.OutputTokens)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.CacheCreationTokens)
	})

	t.Run("Extract token usage with nil metadata", func(t *testing.T) {
		usage := client.extractTokenUsage(nil)

		// Requirement 6.4: When usage metadata is not available, all token counts should be zero
		assert.Equal(t, int64(0), usage.InputTokens)
		assert.Equal(t, int64(0), usage.OutputTokens)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.CacheCreationTokens)
	})

	t.Run("Extract token usage with cache read tokens only", func(t *testing.T) {
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        0,
			CandidatesTokenCount:    30,
			TotalTokenCount:         30,
			CachedContentTokenCount: 15, // Some cached content was used
		}

		usage := client.extractTokenUsage(metadata)

		assert.Equal(t, int64(0), usage.InputTokens)
		assert.Equal(t, int64(30), usage.OutputTokens)
		assert.Equal(t, int64(15), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.CacheCreationTokens)
	})
}

func TestUpdateTokenUsage(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Update token usage from empty state", func(t *testing.T) {
		finalUsage := &TokenUsage{}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        50,
			CandidatesTokenCount:    25,
			TotalTokenCount:         75,
			CachedContentTokenCount: 10,
		}

		client.updateTokenUsage(finalUsage, metadata)

		assert.Equal(t, int64(50), finalUsage.InputTokens)
		assert.Equal(t, int64(25), finalUsage.OutputTokens)
		assert.Equal(t, int64(10), finalUsage.CacheReadTokens)
		assert.Equal(t, int64(0), finalUsage.CacheCreationTokens)
	})

	t.Run("Update token usage with accumulating output tokens", func(t *testing.T) {
		finalUsage := &TokenUsage{
			InputTokens:     50,
			OutputTokens:    10,
			CacheReadTokens: 5,
		}

		// Simulate streaming chunk with more output tokens
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        50, // Should remain the same
			CandidatesTokenCount:    20, // Increased output tokens
			TotalTokenCount:         70,
			CachedContentTokenCount: 5, // Should remain the same
		}

		client.updateTokenUsage(finalUsage, metadata)

		assert.Equal(t, int64(50), finalUsage.InputTokens)
		assert.Equal(t, int64(20), finalUsage.OutputTokens) // Updated to higher value
		assert.Equal(t, int64(5), finalUsage.CacheReadTokens)
		assert.Equal(t, int64(0), finalUsage.CacheCreationTokens)
	})

	t.Run("Update token usage with nil metadata", func(t *testing.T) {
		originalUsage := TokenUsage{
			InputTokens:     100,
			OutputTokens:    50,
			CacheReadTokens: 25,
		}
		finalUsage := originalUsage

		client.updateTokenUsage(&finalUsage, nil)

		// Should remain unchanged when metadata is nil
		assert.Equal(t, originalUsage.InputTokens, finalUsage.InputTokens)
		assert.Equal(t, originalUsage.OutputTokens, finalUsage.OutputTokens)
		assert.Equal(t, originalUsage.CacheReadTokens, finalUsage.CacheReadTokens)
		assert.Equal(t, int64(0), finalUsage.CacheCreationTokens)
	})

	t.Run("Update token usage with inconsistent input tokens", func(t *testing.T) {
		finalUsage := &TokenUsage{
			InputTokens:     50,
			OutputTokens:    10,
			CacheReadTokens: 5,
		}

		// Metadata with different input token count (should log warning but update)
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        60, // Different from existing
			CandidatesTokenCount:    15,
			TotalTokenCount:         75,
			CachedContentTokenCount: 5,
		}

		client.updateTokenUsage(finalUsage, metadata)

		assert.Equal(t, int64(60), finalUsage.InputTokens) // Updated to new value
		assert.Equal(t, int64(15), finalUsage.OutputTokens)
		assert.Equal(t, int64(5), finalUsage.CacheReadTokens)
	})

	t.Run("Update token usage with lower output tokens", func(t *testing.T) {
		finalUsage := &TokenUsage{
			InputTokens:     50,
			OutputTokens:    20,
			CacheReadTokens: 5,
		}

		// Metadata with lower output token count (should not decrease)
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        50,
			CandidatesTokenCount:    15, // Lower than existing
			TotalTokenCount:         65,
			CachedContentTokenCount: 5,
		}

		client.updateTokenUsage(finalUsage, metadata)

		assert.Equal(t, int64(50), finalUsage.InputTokens)
		assert.Equal(t, int64(20), finalUsage.OutputTokens) // Should not decrease
		assert.Equal(t, int64(5), finalUsage.CacheReadTokens)
	})
}

func TestValidateTokenUsage(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Validate correct token usage", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         100,
			OutputTokens:        50,
			CacheReadTokens:     25,
			CacheCreationTokens: 0,
		}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    50,
			TotalTokenCount:         150,
			CachedContentTokenCount: 25,
		}

		err := client.validateTokenUsage(usage, metadata)
		assert.NoError(t, err)
	})

	t.Run("Validate token usage with nil metadata", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         0,
			OutputTokens:        0,
			CacheReadTokens:     0,
			CacheCreationTokens: 0,
		}

		err := client.validateTokenUsage(usage, nil)
		assert.NoError(t, err)
	})

	t.Run("Validate token usage with nil metadata but non-zero tokens", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         100, // Should be zero when metadata is nil
			OutputTokens:        0,
			CacheReadTokens:     0,
			CacheCreationTokens: 0,
		}

		err := client.validateTokenUsage(usage, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token usage should be zero when metadata is unavailable")
	})

	t.Run("Validate token usage with input token mismatch", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         100,
			OutputTokens:        50,
			CacheReadTokens:     25,
			CacheCreationTokens: 0,
		}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        90, // Mismatch
			CandidatesTokenCount:    50,
			TotalTokenCount:         140,
			CachedContentTokenCount: 25,
		}

		err := client.validateTokenUsage(usage, metadata)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "input token mismatch")
	})

	t.Run("Validate token usage with output token mismatch", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         100,
			OutputTokens:        60, // Mismatch
			CacheReadTokens:     25,
			CacheCreationTokens: 0,
		}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    50,
			TotalTokenCount:         150,
			CachedContentTokenCount: 25,
		}

		err := client.validateTokenUsage(usage, metadata)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "output token mismatch")
	})

	t.Run("Validate token usage with cache read token mismatch", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         100,
			OutputTokens:        50,
			CacheReadTokens:     30, // Mismatch
			CacheCreationTokens: 0,
		}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    50,
			TotalTokenCount:         150,
			CachedContentTokenCount: 25,
		}

		err := client.validateTokenUsage(usage, metadata)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cache read token mismatch")
	})

	t.Run("Validate token usage with negative tokens", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         -10, // Negative
			OutputTokens:        50,
			CacheReadTokens:     25,
			CacheCreationTokens: 0,
		}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    50,
			TotalTokenCount:         150,
			CachedContentTokenCount: 25,
		}

		err := client.validateTokenUsage(usage, metadata)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token counts must be non-negative")
	})

	t.Run("Validate token usage with zero metadata values", func(t *testing.T) {
		usage := TokenUsage{
			InputTokens:         0,
			OutputTokens:        0,
			CacheReadTokens:     0,
			CacheCreationTokens: 0,
		}
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        0,
			CandidatesTokenCount:    0,
			TotalTokenCount:         0,
			CachedContentTokenCount: 0,
		}

		err := client.validateTokenUsage(usage, metadata)
		assert.NoError(t, err)
	})
}

func TestTokenUsageIntegration(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Complete token usage workflow", func(t *testing.T) {
		// Test the complete workflow: extract -> validate
		metadata := &GeminiUsageMetadata{
			PromptTokenCount:        150,
			CandidatesTokenCount:    75,
			TotalTokenCount:         225,
			CachedContentTokenCount: 30,
		}

		// Extract token usage
		usage := client.extractTokenUsage(metadata)

		// Validate the extracted usage
		err := client.validateTokenUsage(usage, metadata)
		require.NoError(t, err)

		// Verify all requirements are met
		assert.Equal(t, int64(150), usage.InputTokens)     // Requirement 6.1
		assert.Equal(t, int64(75), usage.OutputTokens)     // Requirement 6.2
		assert.Equal(t, int64(30), usage.CacheReadTokens)  // Requirement 6.3
		assert.Equal(t, int64(0), usage.CacheCreationTokens) // Gemini doesn't provide this
	})

	t.Run("Streaming token usage workflow", func(t *testing.T) {
		// Test streaming workflow: initialize -> update -> validate
		finalUsage := &TokenUsage{}

		// First chunk
		chunk1 := &GeminiUsageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    20,
			TotalTokenCount:         120,
			CachedContentTokenCount: 15,
		}
		client.updateTokenUsage(finalUsage, chunk1)

		// Second chunk with more output tokens
		chunk2 := &GeminiUsageMetadata{
			PromptTokenCount:        100, // Same
			CandidatesTokenCount:    40,  // Increased
			TotalTokenCount:         140,
			CachedContentTokenCount: 15, // Same
		}
		client.updateTokenUsage(finalUsage, chunk2)

		// Validate final usage
		err := client.validateTokenUsage(*finalUsage, chunk2)
		require.NoError(t, err)

		// Verify streaming behavior
		assert.Equal(t, int64(100), finalUsage.InputTokens)
		assert.Equal(t, int64(40), finalUsage.OutputTokens) // Should be the higher value
		assert.Equal(t, int64(15), finalUsage.CacheReadTokens)
		assert.Equal(t, int64(0), finalUsage.CacheCreationTokens)
	})

	t.Run("No metadata workflow", func(t *testing.T) {
		// Test workflow when no metadata is available
		usage := client.extractTokenUsage(nil)

		// Validate that all tokens are zero (Requirement 6.4)
		err := client.validateTokenUsage(usage, nil)
		require.NoError(t, err)

		assert.Equal(t, int64(0), usage.InputTokens)
		assert.Equal(t, int64(0), usage.OutputTokens)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.CacheCreationTokens)
	})
}

func TestTokenUsageInResponseConversion(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Convert response with complete token usage", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "Hello! How can I help you?"},
						},
					},
					FinishReason: GeminiFinishReasonStop,
				},
			},
			UsageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:        100,
				CandidatesTokenCount:    25,
				TotalTokenCount:         125,
				CachedContentTokenCount: 15,
			},
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)

		// Verify content
		assert.Equal(t, "Hello! How can I help you?", response.Content)

		// Verify token usage matches requirements
		assert.Equal(t, int64(100), response.Usage.InputTokens)     // Requirement 6.1
		assert.Equal(t, int64(25), response.Usage.OutputTokens)     // Requirement 6.2
		assert.Equal(t, int64(15), response.Usage.CacheReadTokens)  // Requirement 6.3
		assert.Equal(t, int64(0), response.Usage.CacheCreationTokens) // Gemini doesn't provide this

		// Validate the extracted usage
		err = client.validateTokenUsage(response.Usage, geminiResponse.UsageMetadata)
		assert.NoError(t, err)
	})

	t.Run("Convert empty response with token usage", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{}, // Empty candidates
			UsageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:        50,
				CandidatesTokenCount:    0, // No output
				TotalTokenCount:         50,
				CachedContentTokenCount: 10,
			},
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)

		// Verify empty content
		assert.Equal(t, "", response.Content)
		assert.Empty(t, response.ToolCalls)

		// Verify token usage is still extracted properly
		assert.Equal(t, int64(50), response.Usage.InputTokens)
		assert.Equal(t, int64(0), response.Usage.OutputTokens)
		assert.Equal(t, int64(10), response.Usage.CacheReadTokens)
		assert.Equal(t, int64(0), response.Usage.CacheCreationTokens)
	})

	t.Run("Convert response without usage metadata", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "Response without metadata"},
						},
					},
				},
			},
			// No UsageMetadata
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)

		// Verify content
		assert.Equal(t, "Response without metadata", response.Content)

		// Verify all token counts are zero (Requirement 6.4)
		assert.Equal(t, int64(0), response.Usage.InputTokens)
		assert.Equal(t, int64(0), response.Usage.OutputTokens)
		assert.Equal(t, int64(0), response.Usage.CacheReadTokens)
		assert.Equal(t, int64(0), response.Usage.CacheCreationTokens)

		// Validate the usage
		err = client.validateTokenUsage(response.Usage, nil)
		assert.NoError(t, err)
	})
}