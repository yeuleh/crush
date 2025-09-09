package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCustomGeminiClient(t *testing.T) {
	// Create mock options
	opts := providerClientOptions{
		baseURL:   "https://api.example.com",
		apiKey:    "test-key",
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{
				ID:   "test-model",
				Name: "Test Model",
			}
		},
	}

	client := newCustomGeminiClient(opts)
	require.NotNil(t, client)

	// Verify the client implements the interface
	var _ ProviderClient = client
}

func TestCustomGeminiClient_Model(t *testing.T) {
	expectedModel := catwalk.Model{
		ID:   "gemini-pro",
		Name: "Gemini Pro",
	}

	opts := providerClientOptions{
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return expectedModel
		},
	}

	client := newCustomGeminiClient(opts)
	actualModel := client.Model()
	assert.Equal(t, expectedModel, actualModel)
}

func TestCustomGeminiClient_Send(t *testing.T) {
	opts := providerClientOptions{
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{ID: "test-model"}
		},
	}

	client := newCustomGeminiClient(opts)
	ctx := context.Background()

	// Create test messages
	messages := []message.Message{
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "Hello"}},
		},
	}

	// Create empty tools slice for now
	tools := []tools.BaseTool{}

	response, err := client.send(ctx, messages, tools)
	require.NoError(t, err)
	require.NotNil(t, response)

	assert.Contains(t, response.Content, "stub")
	assert.Equal(t, message.FinishReasonEndTurn, response.FinishReason)
	assert.Empty(t, response.ToolCalls)
}

func TestCustomGeminiClient_Stream(t *testing.T) {
	opts := providerClientOptions{
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{ID: "test-model"}
		},
	}

	client := newCustomGeminiClient(opts)
	ctx := context.Background()

	// Create test messages
	messages := []message.Message{
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "Hello"}},
		},
	}

	tools := []tools.BaseTool{}

	eventChan := client.stream(ctx, messages, tools)
	require.NotNil(t, eventChan)

	// Collect events
	var events []ProviderEvent
	for event := range eventChan {
		events = append(events, event)
	}

	// Verify event sequence
	require.Len(t, events, 4)
	assert.Equal(t, EventContentStart, events[0].Type)
	assert.Equal(t, EventContentDelta, events[1].Type)
	assert.Contains(t, events[1].Content, "stub")
	assert.Equal(t, EventContentStop, events[2].Type)
	assert.Equal(t, EventComplete, events[3].Type)
	assert.NotNil(t, events[3].Response)
}

// Integration test with NewProvider function
func TestNewProvider_CustomGemini(t *testing.T) {
	cfg := config.ProviderConfig{
		ID:      "test-custom-gemini",
		Name:    "Test Custom Gemini",
		Type:    TypeCustomGemini, // Use typed constant
		BaseURL: "https://api.example.com",
		APIKey:  "test-key",
		Models: []catwalk.Model{
			{
				ID:   "gemini-pro",
				Name: "Gemini Pro",
			},
		},
	}

	// Mock the config system - simplified for skeleton
	// Note: In a real test, you'd want to properly restore config

	provider, err := NewProvider(cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify it implements the Provider interface
	var _ Provider = provider
}

// Compatibility test to ensure existing providers still work
func TestNewProvider_ExistingProviders_StillWork(t *testing.T) {
	tests := []struct {
		name string
		providerType catwalk.Type
	}{
		{"OpenAI provider", catwalk.TypeOpenAI},
		{"Anthropic provider", catwalk.TypeAnthropic},
		{"Gemini provider", catwalk.TypeGemini},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.ProviderConfig{
				ID:      "test-provider",
				Name:    "Test Provider",
				Type:    tt.providerType,
				BaseURL: "https://api.example.com",
				APIKey:  "test-key",
				Models: []catwalk.Model{
					{
						ID:   "test-model",
						Name: "Test Model",
					},
				},
			}

			provider, err := NewProvider(cfg)
			// We expect these to work (or fail in the same way as before)
			// The key is that the error should not be "provider not supported"
			if err != nil && err.Error() == fmt.Sprintf("provider not supported: %s", tt.providerType) {
				t.Errorf("Provider %s should be supported but got unsupported error", tt.providerType)
			}
			// Note: We don't require.NoError because some providers might fail
			// due to missing configuration, but they should not fail due to
			// being unsupported

			if provider != nil {
				// Verify it implements the Provider interface
				var _ Provider = provider
			}
		})
	}
}

// Test that unknown provider types still return appropriate error
func TestNewProvider_UnknownProvider_ReturnsError(t *testing.T) {
	cfg := config.ProviderConfig{
		ID:      "test-unknown",
		Name:    "Test Unknown",
		Type:    catwalk.Type("unknown-provider-type"), // Explicit type casting for unknown type
		BaseURL: "https://api.example.com",
		APIKey:  "test-key",
	}

	provider, err := NewProvider(cfg)
	require.Error(t, err)
	require.Nil(t, provider)
	require.Contains(t, err.Error(), "provider not supported")
}
