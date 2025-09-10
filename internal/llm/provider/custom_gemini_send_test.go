package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomGeminiClient_send(t *testing.T) {
	tests := []struct {
		name           string
		messages       []message.Message
		tools          []tools.BaseTool
		serverResponse string
		serverStatus   int
		expectedError  string
		expectedResult *ProviderResponse
	}{
		{
			name: "successful send request",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Hello, how are you?"},
					},
				},
			},
			tools: []tools.BaseTool{},
			serverResponse: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"text": "I'm doing well, thank you for asking!"
								}
							]
						},
						"finishReason": "STOP"
					}
				],
				"usageMetadata": {
					"promptTokenCount": 5,
					"candidatesTokenCount": 8
				}
			}`,
			serverStatus: http.StatusOK,
			expectedResult: &ProviderResponse{
				Content:      "I'm doing well, thank you for asking!",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{InputTokens: 5, OutputTokens: 8},
				FinishReason: message.FinishReasonEndTurn,
			},
		},
		{
			name: "server error response",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Hello"},
					},
				},
			},
			tools: []tools.BaseTool{},
			serverResponse: `{
				"error": {
					"code": 400,
					"message": "Invalid request",
					"status": "INVALID_ARGUMENT"
				}
			}`,
			serverStatus:  http.StatusBadRequest,
			expectedError: "Gemini API error (HTTP 400): INVALID_ARGUMENT - Invalid request",
		},
		{
			name: "network timeout simulation",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Hello"},
					},
				},
			},
			tools:         []tools.BaseTool{},
			serverStatus:  http.StatusInternalServerError,
			expectedError: "HTTP 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request method and headers
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "Crush/1.0", r.Header.Get("User-Agent"))
				
				// Verify API key is in URL query parameter
				assert.Equal(t, "test-api-key", r.URL.Query().Get("key"))

				w.WriteHeader(tt.serverStatus)
				if tt.serverResponse != "" {
					w.Write([]byte(tt.serverResponse))
				}
			}))
			defer server.Close()

			// Create client with test server URL
			client := &testCustomGeminiClient{
				customGeminiClient: customGeminiClient{
					providerOptions: providerClientOptions{
						baseURL:   server.URL,
						apiKey:    "test-api-key",
						modelType: config.SelectedModelType("gemini-pro"),
						maxTokens: 1000,
						model: func(modelType config.SelectedModelType) catwalk.Model {
							return catwalk.Model{
								ID:               string(modelType),
								Name:             string(modelType),
								ContextWindow:    32768,
								DefaultMaxTokens: 8192,
							}
						},
					},
					httpClient: server.Client(),
					baseURL:    server.URL,
				},
			}

			// Test the send method
			ctx := context.Background()
			result, err := client.send(ctx, tt.messages, tt.tools)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)

				assert.Equal(t, tt.expectedResult.Content, result.Content)
				assert.Equal(t, tt.expectedResult.Usage, result.Usage)
				assert.Equal(t, tt.expectedResult.FinishReason, result.FinishReason)
				assert.Len(t, result.ToolCalls, len(tt.expectedResult.ToolCalls))
			}
		})
	}
}

func TestCustomGeminiClient_send_URLBuilding(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		expectedURL string
	}{
		{
			name:        "standard mode URL",
			baseURL:     "https://generativelanguage.googleapis.com/v1beta",
			expectedURL: "/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:        "complete URL mode",
			baseURL:     "https://custom.api.com/gemini/chat#",
			expectedURL: "/gemini/chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that captures the request URL
			var capturedURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedURL = r.URL.Path
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"candidates": [
						{
							"content": {
								"role": "model",
								"parts": [{"text": "test response"}]
							},
							"finishReason": "STOP"
						}
					]
				}`))
			}))
			defer server.Close()

			// Adjust baseURL to use test server
			testBaseURL := tt.baseURL
			if tt.name == "standard mode URL" {
				testBaseURL = server.URL
			} else {
				testBaseURL = server.URL + "/gemini/chat#"
			}

			client := &testCustomGeminiClient{
				customGeminiClient: customGeminiClient{
					providerOptions: providerClientOptions{
						baseURL:   testBaseURL,
						apiKey:    "test-key",
						modelType: config.SelectedModelType("gemini-pro"),
						model: func(modelType config.SelectedModelType) catwalk.Model {
							return catwalk.Model{
								ID:               string(modelType),
								Name:             string(modelType),
								ContextWindow:    32768,
								DefaultMaxTokens: 8192,
							}
						},
					},
					httpClient: server.Client(),
					baseURL:    testBaseURL,
				},
			}

			messages := []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "test message"},
					},
				},
			}

			ctx := context.Background()
			_, err := client.send(ctx, messages, []tools.BaseTool{})

			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, capturedURL)
		})
	}
}

// testCustomGeminiClient wraps customGeminiClient for testing
type testCustomGeminiClient struct {
	customGeminiClient
}