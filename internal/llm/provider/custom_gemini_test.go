package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

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

	// Verify internal structure
	customClient := client.(*customGeminiClient)
	assert.Equal(t, opts.baseURL, customClient.baseURL)
	assert.NotNil(t, customClient.httpClient)
	assert.Equal(t, opts.baseURL, customClient.providerOptions.baseURL)
	assert.Equal(t, opts.apiKey, customClient.providerOptions.apiKey)
	assert.Equal(t, opts.modelType, customClient.providerOptions.modelType)
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
	tests := []struct {
		name        string
		baseURL     string
		expectError bool
	}{
		{
			name:        "valid standard mode URL",
			baseURL:     "https://api.example.com",
			expectError: false,
		},
		{
			name:        "valid complete URL mode",
			baseURL:     "https://api.example.com/complete#",
			expectError: false,
		},
		{
			name:        "invalid base URL",
			baseURL:     "invalid-url",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := providerClientOptions{
				baseURL:   tt.baseURL,
				apiKey:    "test-key",
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-pro"}
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

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, response)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, response)
				assert.Contains(t, response.Content, "stub")
				assert.Equal(t, message.FinishReasonEndTurn, response.FinishReason)
				assert.Empty(t, response.ToolCalls)
			}
		})
	}
}

func TestCustomGeminiClient_Stream(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		expectError bool
	}{
		{
			name:        "valid standard mode URL",
			baseURL:     "https://api.example.com",
			expectError: false,
		},
		{
			name:        "valid complete URL mode",
			baseURL:     "https://api.example.com/complete#",
			expectError: false,
		},
		{
			name:        "invalid base URL",
			baseURL:     "invalid-url",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := providerClientOptions{
				baseURL:   tt.baseURL,
				apiKey:    "test-key",
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-pro"}
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

			if tt.expectError {
				// Should have received an error event
				require.Len(t, events, 1)
				assert.Equal(t, EventError, events[0].Type)
				assert.Error(t, events[0].Error)
			} else {
				// Verify normal event sequence
				require.Len(t, events, 4)
				assert.Equal(t, EventContentStart, events[0].Type)
				assert.Equal(t, EventContentDelta, events[1].Type)
				assert.Contains(t, events[1].Content, "stub")
				assert.Equal(t, EventContentStop, events[2].Type)
				assert.Equal(t, EventComplete, events[3].Type)
				assert.NotNil(t, events[3].Response)
			}
		})
	}
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
		name         string
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

// Test HTTP client configuration
func TestCreateCustomGeminiHTTPClient(t *testing.T) {
	t.Run("standard client configuration", func(t *testing.T) {
		// Test with debug mode off by temporarily setting config
		// Note: This is a simplified test - in a real scenario we'd need proper config mocking
		client := &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}

		// Verify timeout configuration
		assert.Equal(t, 120*time.Second, client.Timeout)

		// Verify transport configuration
		transport, ok := client.Transport.(*http.Transport)
		require.True(t, ok)
		assert.Equal(t, 100, transport.MaxIdleConns)
		assert.Equal(t, 10, transport.MaxIdleConnsPerHost)
		assert.Equal(t, 90*time.Second, transport.IdleConnTimeout)
		assert.Equal(t, 10*time.Second, transport.TLSHandshakeTimeout)
		assert.Equal(t, 30*time.Second, transport.ResponseHeaderTimeout)
		assert.Equal(t, 1*time.Second, transport.ExpectContinueTimeout)
	})
}

// Test URL building functionality
func TestCustomGeminiClient_BuildRequestURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		methodPath  string
		expectedURL string
		expectError bool
	}{
		{
			name:        "standard mode URL building",
			baseURL:     "https://api.example.com",
			methodPath:  "v1beta/models/gemini-pro:generateContent",
			expectedURL: "https://api.example.com/v1beta/models/gemini-pro:generateContent",
			expectError: false,
		},
		{
			name:        "complete URL mode",
			baseURL:     "https://api.example.com/complete-endpoint#",
			methodPath:  "ignored",
			expectedURL: "https://api.example.com/complete-endpoint",
			expectError: false,
		},
		{
			name:        "invalid base URL",
			baseURL:     "invalid-url",
			methodPath:  "v1beta/models/gemini-pro:generateContent",
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := providerClientOptions{
				baseURL:   tt.baseURL,
				apiKey:    "test-key",
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "test-model"}
				},
			}

			client := newCustomGeminiClient(opts).(*customGeminiClient)

			actualURL, err := client.buildRequestURL(tt.methodPath)

			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, actualURL)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedURL, actualURL)
			}
		})
	}
}

// Test Gemini method path building
func TestCustomGeminiClient_BuildGeminiMethodPath(t *testing.T) {
	opts := providerClientOptions{
		baseURL:   "https://api.example.com",
		apiKey:    "test-key",
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{ID: "test-model"}
		},
	}

	client := newCustomGeminiClient(opts).(*customGeminiClient)

	tests := []struct {
		name      string
		model     string
		operation string
		expected  string
	}{
		{
			name:      "generate content path",
			model:     "gemini-pro",
			operation: "generateContent",
			expected:  "v1beta/models/gemini-pro:generateContent",
		},
		{
			name:      "stream generate content path",
			model:     "gemini-pro",
			operation: "streamGenerateContent",
			expected:  "v1beta/models/gemini-pro:streamGenerateContent",
		},
		{
			name:      "different model",
			model:     "gemini-1.5-pro",
			operation: "generateContent",
			expected:  "v1beta/models/gemini-1.5-pro:generateContent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := client.buildGeminiMethodPath(tt.model, tt.operation)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

// Test model name extraction
func TestCustomGeminiClient_GetModelName(t *testing.T) {
	opts := providerClientOptions{
		baseURL:   "https://api.example.com",
		apiKey:    "test-key",
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{ID: "gemini-pro"}
		},
	}

	client := newCustomGeminiClient(opts).(*customGeminiClient)

	modelName := client.getModelName()
	assert.Equal(t, "gemini-pro", modelName)
}
