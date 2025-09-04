package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomGeminiTokenUsageIntegration(t *testing.T) {
	t.Run("Non-streaming request with token usage", func(t *testing.T) {
		// Create a mock server that returns a response with token usage
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify this is a non-streaming request
			assert.Equal(t, "POST", r.Method)
			assert.Contains(t, r.URL.Path, ":generateContent")
			assert.NotContains(t, r.URL.Path, "stream")

			// Return a mock response with token usage
			response := GeminiResponse{
				Candidates: []GeminiCandidate{
					{
						Content: &GeminiContent{
							Parts: []GeminiPart{
								{Text: "Hello! I can help you with that."},
							},
							Role: GeminiRoleModel,
						},
						FinishReason: GeminiFinishReasonStop,
						Index:        0,
					},
				},
				UsageMetadata: &GeminiUsageMetadata{
					PromptTokenCount:        25,
					CandidatesTokenCount:    8,
					TotalTokenCount:         33,
					CachedContentTokenCount: 5,
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		// Create client with mock server
		opts := providerClientOptions{
			baseURL: server.URL,
			apiKey:  "test-api-key",
			modelType: config.SelectedModelTypeLarge,
			model: func(config.SelectedModelType) catwalk.Model {
				return catwalk.Model{
					ID:                "gemini-1.5-pro",
					Name:              "Gemini 1.5 Pro",
					ContextWindow:     2097152,
					DefaultMaxTokens:  8192,
				}
			},
		}

		client := newCustomGeminiClient(opts)

		// Test messages
		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello, how are you?"},
				},
			},
		}

		// Send request using the ProviderClient interface
		ctx := context.Background()
		response, err := client.send(ctx, messages, nil)
		require.NoError(t, err)
		require.NotNil(t, response)

		// Verify response content
		assert.Equal(t, "Hello! I can help you with that.", response.Content)

		// Verify token usage (Requirements 6.1, 6.2, 6.3)
		assert.Equal(t, int64(25), response.Usage.InputTokens)     // Requirement 6.1: Input tokens extracted
		assert.Equal(t, int64(8), response.Usage.OutputTokens)     // Requirement 6.2: Output tokens extracted
		assert.Equal(t, int64(5), response.Usage.CacheReadTokens)  // Requirement 6.3: Cache read tokens extracted
		assert.Equal(t, int64(0), response.Usage.CacheCreationTokens) // Gemini doesn't provide cache creation tokens
	})

	t.Run("Streaming request with token usage", func(t *testing.T) {
		// Create a mock server that returns streaming response with token usage
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify this is a streaming request
			assert.Equal(t, "POST", r.Method)
			assert.Contains(t, r.URL.Path, ":streamGenerateContent")
			assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))

			// Return a mock streaming response
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Write streaming chunks
			chunks := []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}]}`,
				`data: {"candidates":[{"content":{"parts":[{"text":" there!"}],"role":"model"}}]}`,
				`data: {"candidates":[{"content":{"parts":[{"text":" How can I help?"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":12,"totalTokenCount":27,"cachedContentTokenCount":3}}`,
			}

			for _, chunk := range chunks {
				w.Write([]byte(chunk + "\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}))
		defer server.Close()

		// Create client with mock server
		opts := providerClientOptions{
			baseURL: server.URL,
			apiKey:  "test-api-key",
			modelType: config.SelectedModelTypeLarge,
			model: func(config.SelectedModelType) catwalk.Model {
				return catwalk.Model{
					ID:                "gemini-1.5-pro",
					Name:              "Gemini 1.5 Pro",
					ContextWindow:     2097152,
					DefaultMaxTokens:  8192,
				}
			},
		}

		client := newCustomGeminiClient(opts)

		// Test messages
		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello!"},
				},
			},
		}

		// Stream request using the ProviderClient interface
		ctx := context.Background()
		eventChan := client.stream(ctx, messages, nil)

		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Verify we got the expected events
		require.Greater(t, len(events), 0)

		// Find the final complete event
		var finalEvent *ProviderEvent
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Type == EventComplete {
				finalEvent = &events[i]
				break
			}
		}

		require.NotNil(t, finalEvent, "Should have received EventComplete")
		require.NotNil(t, finalEvent.Response, "Complete event should have response")

		// Verify response content
		assert.Equal(t, "Hello there! How can I help?", finalEvent.Response.Content)

		// Verify token usage in streaming response
		assert.Equal(t, int64(15), finalEvent.Response.Usage.InputTokens)     // Requirement 6.1
		assert.Equal(t, int64(12), finalEvent.Response.Usage.OutputTokens)    // Requirement 6.2
		assert.Equal(t, int64(3), finalEvent.Response.Usage.CacheReadTokens)  // Requirement 6.3
		assert.Equal(t, int64(0), finalEvent.Response.Usage.CacheCreationTokens) // Gemini doesn't provide this
	})

	t.Run("Request without usage metadata", func(t *testing.T) {
		// Create a mock server that returns response without usage metadata
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return a mock response without usage metadata
			response := GeminiResponse{
				Candidates: []GeminiCandidate{
					{
						Content: &GeminiContent{
							Parts: []GeminiPart{
								{Text: "Response without metadata"},
							},
							Role: GeminiRoleModel,
						},
						FinishReason: GeminiFinishReasonStop,
					},
				},
				// No UsageMetadata
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		// Create client with mock server
		opts := providerClientOptions{
			baseURL: server.URL,
			apiKey:  "test-api-key",
			modelType: config.SelectedModelTypeLarge,
			model: func(config.SelectedModelType) catwalk.Model {
				return catwalk.Model{ID: "gemini-1.5-pro"}
			},
		}

		client := newCustomGeminiClient(opts)

		// Test messages
		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Test message"},
				},
			},
		}

		// Send request using the ProviderClient interface
		ctx := context.Background()
		response, err := client.send(ctx, messages, nil)
		require.NoError(t, err)
		require.NotNil(t, response)

		// Verify response content
		assert.Equal(t, "Response without metadata", response.Content)

		// Verify all token counts are zero (Requirement 6.4)
		assert.Equal(t, int64(0), response.Usage.InputTokens)
		assert.Equal(t, int64(0), response.Usage.OutputTokens)
		assert.Equal(t, int64(0), response.Usage.CacheReadTokens)
		assert.Equal(t, int64(0), response.Usage.CacheCreationTokens)
	})

	t.Run("Simulated streaming with token usage", func(t *testing.T) {
		// Create a mock server that doesn't support streaming (returns 404 for streaming endpoint)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "streamGenerateContent") {
				// Return 404 for streaming endpoint to trigger fallback
				w.WriteHeader(http.StatusNotFound)
				return
			}

			// Return normal response for non-streaming endpoint
			response := GeminiResponse{
				Candidates: []GeminiCandidate{
					{
						Content: &GeminiContent{
							Parts: []GeminiPart{
								{Text: "Simulated streaming response"},
							},
							Role: GeminiRoleModel,
						},
						FinishReason: GeminiFinishReasonStop,
					},
				},
				UsageMetadata: &GeminiUsageMetadata{
					PromptTokenCount:        20,
					CandidatesTokenCount:    6,
					TotalTokenCount:         26,
					CachedContentTokenCount: 2,
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		// Create client with mock server
		opts := providerClientOptions{
			baseURL: server.URL,
			apiKey:  "test-api-key",
			modelType: config.SelectedModelTypeLarge,
			model: func(config.SelectedModelType) catwalk.Model {
				return catwalk.Model{ID: "gemini-1.5-pro"}
			},
			extraBody: map[string]interface{}{
				"chunk_size":        5, // Small chunks for testing
				"simulation_delay":  1, // Fast simulation
			},
		}

		client := newCustomGeminiClient(opts)

		// Test messages
		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Test streaming"},
				},
			},
		}

		// Stream request (should fallback to simulation) using the ProviderClient interface
		ctx := context.Background()
		eventChan := client.stream(ctx, messages, nil)

		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Verify we got streaming events
		require.Greater(t, len(events), 0)

		// Find the final complete event
		var finalEvent *ProviderEvent
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Type == EventComplete {
				finalEvent = &events[i]
				break
			}
		}

		require.NotNil(t, finalEvent, "Should have received EventComplete")
		require.NotNil(t, finalEvent.Response, "Complete event should have response")

		// Verify response content
		assert.Equal(t, "Simulated streaming response", finalEvent.Response.Content)

		// Verify token usage is preserved in simulated streaming
		assert.Equal(t, int64(20), finalEvent.Response.Usage.InputTokens)
		assert.Equal(t, int64(6), finalEvent.Response.Usage.OutputTokens)
		assert.Equal(t, int64(2), finalEvent.Response.Usage.CacheReadTokens)
		assert.Equal(t, int64(0), finalEvent.Response.Usage.CacheCreationTokens)
	})
}