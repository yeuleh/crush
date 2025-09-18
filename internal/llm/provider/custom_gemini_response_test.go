package provider

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomGeminiClient_parseResponse(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name           string
		responseJSON   string
		expectedError  string
		expectedResult *ProviderResponse
	}{
		{
			name: "successful text response",
			responseJSON: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"text": "Hello! How can I help you today?"
								}
							]
						},
						"finishReason": "STOP"
					}
				],
				"usageMetadata": {
					"promptTokenCount": 10,
					"candidatesTokenCount": 8,
					"cachedContentTokenCount": 0
				}
			}`,
			expectedResult: &ProviderResponse{
				Content:      "Hello! How can I help you today?",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{InputTokens: 10, OutputTokens: 8, CacheCreationTokens: 0, CacheReadTokens: 0},
				FinishReason: message.FinishReasonEndTurn,
			},
		},
		{
			name: "response with function call",
			responseJSON: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"text": "I'll help you with that calculation."
								},
								{
									"functionCall": {
										"name": "calculate",
										"args": {
											"expression": "2 + 2"
										}
									}
								}
							]
						},
						"finishReason": "STOP"
					}
				],
				"usageMetadata": {
					"promptTokenCount": 15,
					"candidatesTokenCount": 12
				}
			}`,
			expectedResult: &ProviderResponse{
				Content: "I'll help you with that calculation.",
				ToolCalls: []message.ToolCall{
					{
						Name:     "calculate",
						Input:    `{"expression":"2 + 2"}`,
						Type:     "function",
						Finished: false,
					},
				},
				Usage:        TokenUsage{InputTokens: 15, OutputTokens: 12, CacheCreationTokens: 0, CacheReadTokens: 0},
				FinishReason: message.FinishReasonEndTurn,
			},
		},
		{
			name: "response with max tokens finish reason",
			responseJSON: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"text": "This is a partial response that was cut off due to"
								}
							]
						},
						"finishReason": "MAX_TOKENS"
					}
				],
				"usageMetadata": {
					"promptTokenCount": 100,
					"candidatesTokenCount": 50
				}
			}`,
			expectedResult: &ProviderResponse{
				Content:      "This is a partial response that was cut off due to",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 0, CacheReadTokens: 0},
				FinishReason: message.FinishReasonMaxTokens,
			},
		},
		{
			name: "response with safety finish reason",
			responseJSON: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"text": "I cannot provide that information."
								}
							]
						},
						"finishReason": "SAFETY"
					}
				],
				"usageMetadata": {
					"promptTokenCount": 20,
					"candidatesTokenCount": 5
				}
			}`,
			expectedResult: &ProviderResponse{
				Content:      "I cannot provide that information.",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{InputTokens: 20, OutputTokens: 5, CacheCreationTokens: 0, CacheReadTokens: 0},
				FinishReason: message.FinishReasonPermissionDenied,
			},
		},
		{
			name: "empty response",
			responseJSON: `{
				"candidates": []
			}`,
			expectedError: "no candidates in response",
		},
		{
			name:          "invalid JSON",
			responseJSON:  `{invalid json}`,
			expectedError: "failed to parse response",
		},
		{
			name: "response without usage metadata",
			responseJSON: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"text": "Response without usage info"
								}
							]
						},
						"finishReason": "STOP"
					}
				]
			}`,
			expectedResult: &ProviderResponse{
				Content:      "Response without usage info",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{}, // Empty usage when not provided
				FinishReason: message.FinishReasonEndTurn,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.parseResponse([]byte(tt.responseJSON))

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

				// Check tool calls (excluding ID which is generated)
				assert.Len(t, result.ToolCalls, len(tt.expectedResult.ToolCalls))
				for i, expectedCall := range tt.expectedResult.ToolCalls {
					if i < len(result.ToolCalls) {
						actualCall := result.ToolCalls[i]
						assert.Equal(t, expectedCall.Name, actualCall.Name)
						assert.Equal(t, expectedCall.Input, actualCall.Input)
						assert.Equal(t, expectedCall.Type, actualCall.Type)
						assert.Equal(t, expectedCall.Finished, actualCall.Finished)
						assert.NotEmpty(t, actualCall.ID) // ID should be generated
					}
				}
			}
		})
	}
}

func TestCustomGeminiClient_extractContentAndToolCalls(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name              string
		content           geminiContent
		expectedText      string
		expectedToolCalls int
	}{
		{
			name: "text only content",
			content: geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{Text: "Hello, "},
					{Text: "world!"},
				},
			},
			expectedText:      "Hello, world!",
			expectedToolCalls: 0,
		},
		{
			name: "mixed content with function call",
			content: geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{Text: "Let me calculate that for you."},
					{
						FunctionCall: &geminiFunctionCall{
							Name: "math_calculator",
							Args: map[string]interface{}{
								"operation": "add",
								"a":         5,
								"b":         3,
							},
						},
					},
				},
			},
			expectedText:      "Let me calculate that for you.",
			expectedToolCalls: 1,
		},
		{
			name: "multiple function calls",
			content: geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{
						FunctionCall: &geminiFunctionCall{
							Name: "get_weather",
							Args: map[string]interface{}{"city": "New York"},
						},
					},
					{
						FunctionCall: &geminiFunctionCall{
							Name: "get_time",
							Args: map[string]interface{}{"timezone": "EST"},
						},
					},
				},
			},
			expectedText:      "",
			expectedToolCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, toolCalls := client.extractContentAndToolCalls(tt.content)

			assert.Equal(t, tt.expectedText, text)
			assert.Len(t, toolCalls, tt.expectedToolCalls)

			// Verify tool calls have required fields
			for _, toolCall := range toolCalls {
				assert.NotEmpty(t, toolCall.ID)
				assert.NotEmpty(t, toolCall.Name)
				assert.Equal(t, "function", toolCall.Type)
				assert.False(t, toolCall.Finished)
			}
		})
	}
}

func TestCustomGeminiClient_convertToToolCall(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name         string
		functionCall *geminiFunctionCall
		expectedName string
		expectedArgs string
	}{
		{
			name: "simple function call",
			functionCall: &geminiFunctionCall{
				Name: "test_function",
				Args: map[string]interface{}{
					"param1": "value1",
					"param2": 42,
				},
			},
			expectedName: "test_function",
			expectedArgs: `{"param1":"value1","param2":42}`,
		},
		{
			name: "function call with no args",
			functionCall: &geminiFunctionCall{
				Name: "no_args_function",
				Args: nil,
			},
			expectedName: "no_args_function",
			expectedArgs: "null",
		},
		{
			name: "function call with empty args",
			functionCall: &geminiFunctionCall{
				Name: "empty_args_function",
				Args: map[string]interface{}{},
			},
			expectedName: "empty_args_function",
			expectedArgs: "{}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertToToolCall(tt.functionCall)

			assert.Equal(t, tt.expectedName, result.Name)
			assert.Equal(t, "function", result.Type)
			assert.False(t, result.Finished)
			assert.NotEmpty(t, result.ID)

			// Parse and compare JSON to handle different ordering
			var expectedJSON, actualJSON interface{}
			err1 := json.Unmarshal([]byte(tt.expectedArgs), &expectedJSON)
			err2 := json.Unmarshal([]byte(result.Input), &actualJSON)

			require.NoError(t, err1)
			require.NoError(t, err2)
			assert.Equal(t, expectedJSON, actualJSON)
		})
	}
}

func TestCustomGeminiClient_convertUsage(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		usage    *geminiUsage
		expected TokenUsage
	}{
		{
			name: "complete usage metadata",
			usage: &geminiUsage{
				PromptTokenCount:        100,
				CandidatesTokenCount:    50,
				CachedContentTokenCount: 25,
			},
			expected: TokenUsage{
				InputTokens:         100,
				OutputTokens:        50,
				CacheCreationTokens: 25,
				CacheReadTokens:     0,
			},
		},
		{
			name: "minimal usage metadata",
			usage: &geminiUsage{
				PromptTokenCount:     10,
				CandidatesTokenCount: 5,
			},
			expected: TokenUsage{
				InputTokens:         10,
				OutputTokens:        5,
				CacheCreationTokens: 0,
				CacheReadTokens:     0,
			},
		},
		{
			name:     "nil usage metadata",
			usage:    nil,
			expected: TokenUsage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertUsage(tt.usage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCustomGeminiClient_convertFinishReason(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		geminiReason   string
		expectedReason message.FinishReason
	}{
		{"STOP", message.FinishReasonEndTurn},
		{"MAX_TOKENS", message.FinishReasonMaxTokens},
		{"SAFETY", message.FinishReasonPermissionDenied},
		{"RECITATION", message.FinishReasonPermissionDenied},
		{"OTHER", message.FinishReasonError},
		{"", message.FinishReasonEndTurn}, // Empty reason defaults to end turn
		{"UNKNOWN_REASON", message.FinishReasonUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.geminiReason, func(t *testing.T) {
			result := client.convertFinishReason(tt.geminiReason)
			assert.Equal(t, tt.expectedReason, result)
		})
	}
}

func TestCustomGeminiClient_handleHTTPError(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name         string
		statusCode   int
		responseBody string
		expectedErr  string
	}{
		{
			name:       "Gemini API error response",
			statusCode: 400,
			responseBody: `{
				"error": {
					"code": 400,
					"message": "Invalid request format",
					"status": "INVALID_ARGUMENT"
				}
			}`,
			expectedErr: "HTTP 400: Invalid request format",
		},
		{
			name:         "Generic HTTP error",
			statusCode:   500,
			responseBody: "Internal Server Error",
			expectedErr:  "HTTP 500:",
		},
		{
			name:       "Authentication error",
			statusCode: 401,
			responseBody: `{
				"error": {
					"code": 401,
					"message": "Request had invalid authentication credentials",
					"status": "UNAUTHENTICATED"
				}
			}`,
			expectedErr: "HTTP 401: Request had invalid authentication credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock HTTP response
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Status:     http.StatusText(tt.statusCode),
				Body:       &mockReadCloser{strings.NewReader(tt.responseBody)},
			}

			err := client.handleHTTPError(resp)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

// mockReadCloser implements io.ReadCloser for testing
type mockReadCloser struct {
	*strings.Reader
}

func (m *mockReadCloser) Close() error {
	return nil
}

func TestGenerateToolCallID(t *testing.T) {
	// Test that generateToolCallID produces unique IDs
	id1 := generateToolCallID()

	// Add a small delay to ensure different timestamps
	time.Sleep(1 * time.Millisecond)

	id2 := generateToolCallID()

	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "call_")
	assert.Contains(t, id2, "call_")
}
