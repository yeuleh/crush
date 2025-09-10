package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeminiAPIRequestFormatCompliance verifies that the HTTP request format
// matches the official Gemini API specification
func TestGeminiAPIRequestFormatCompliance(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:        "test-api-key",
			systemMessage: "You are a helpful assistant",
		},
	}

	tests := []struct {
		name                string
		messages            []message.Message
		tools               []tools.BaseTool
		expectedURL         string
		expectedMethod      string
		expectedHeaders     map[string]string
		validateRequestBody func(t *testing.T, body []byte)
	}{
		{
			name: "basic text message request format",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Hello, how are you?"},
					},
				},
			},
			expectedURL:    "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
			expectedMethod: "POST",
			expectedHeaders: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer test-api-key",
				"User-Agent":    "Crush/1.0",
			},
			validateRequestBody: func(t *testing.T, body []byte) {
				var request geminiRequest
				err := json.Unmarshal(body, &request)
				require.NoError(t, err)

				// Validate structure matches Gemini API spec
				assert.NotEmpty(t, request.Contents)
				assert.Equal(t, 1, len(request.Contents))
				assert.Equal(t, "user", request.Contents[0].Role)
				assert.Equal(t, 1, len(request.Contents[0].Parts))
				assert.Equal(t, "Hello, how are you?", request.Contents[0].Parts[0].Text)

				// Validate system instruction format
				assert.NotNil(t, request.SystemInstruction)
				assert.Equal(t, "user", request.SystemInstruction.Role)
				assert.Equal(t, "You are a helpful assistant", request.SystemInstruction.Parts[0].Text)

				// Validate JSON structure matches expected Gemini API format
				expectedJSON := `{
					"contents": [
						{
							"role": "user",
							"parts": [
								{"text": "Hello, how are you?"}
							]
						}
					],
					"systemInstruction": {
						"role": "user",
						"parts": [
							{"text": "You are a helpful assistant"}
						]
					}
				}`
				
				// Re-marshal to compare JSON structure
				actualJSON, err := json.Marshal(request)
				require.NoError(t, err)
				assert.JSONEq(t, expectedJSON, string(actualJSON))
			},
		},
		{
			name: "multimodal message with image request format",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "What's in this image?"},
						message.BinaryContent{
							MIMEType: "image/jpeg",
							Data:     []byte("fake-image-data"),
						},
					},
				},
			},
			expectedURL:    "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
			expectedMethod: "POST",
			expectedHeaders: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer test-api-key",
				"User-Agent":    "Crush/1.0",
			},
			validateRequestBody: func(t *testing.T, body []byte) {
				var request geminiRequest
				err := json.Unmarshal(body, &request)
				require.NoError(t, err)

				// Validate multimodal structure
				assert.Equal(t, 1, len(request.Contents))
				assert.Equal(t, "user", request.Contents[0].Role)
				assert.Equal(t, 2, len(request.Contents[0].Parts))

				// Text part
				assert.Equal(t, "What's in this image?", request.Contents[0].Parts[0].Text)

				// Image part
				assert.NotNil(t, request.Contents[0].Parts[1].InlineData)
				assert.Equal(t, "image/jpeg", request.Contents[0].Parts[1].InlineData.MimeType)
				assert.Equal(t, "ZmFrZS1pbWFnZS1kYXRh", request.Contents[0].Parts[1].InlineData.Data) // base64 of "fake-image-data"

				// Validate JSON structure matches Gemini API multimodal format
				expectedJSON := `{
					"contents": [
						{
							"role": "user",
							"parts": [
								{"text": "What's in this image?"},
								{
									"inlineData": {
										"mimeType": "image/jpeg",
										"data": "ZmFrZS1pbWFnZS1kYXRh"
									}
								}
							]
						}
					],
					"systemInstruction": {
						"role": "user",
						"parts": [
							{"text": "You are a helpful assistant"}
						]
					}
				}`

				actualJSON, err := json.Marshal(request)
				require.NoError(t, err)
				assert.JSONEq(t, expectedJSON, string(actualJSON))
			},
		},
		{
			name: "conversation with tool calls request format",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "What's the weather like?"},
					},
				},
				{
					Role: message.Assistant,
					Parts: []message.ContentPart{
						message.TextContent{Text: "I'll check the weather for you."},
						message.ToolCall{
							ID:    "call_123",
							Name:  "get_weather",
							Input: `{"location": "San Francisco"}`,
						},
					},
				},
				{
					Role: message.Tool,
					Parts: []message.ContentPart{
						message.ToolResult{
							ToolCallID: "call_123",
							Name:       "get_weather",
							Content:    "Sunny, 75°F",
							IsError:    false,
						},
					},
				},
			},
			tools: []tools.BaseTool{
				&mockTool{
					name:        "get_weather",
					description: "Get current weather",
					parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{
								"type":        "string",
								"description": "Location to get weather for",
							},
						},
						"required": []string{"location"},
					},
				},
			},
			expectedURL:    "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
			expectedMethod: "POST",
			expectedHeaders: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer test-api-key",
				"User-Agent":    "Crush/1.0",
			},
			validateRequestBody: func(t *testing.T, body []byte) {
				var request geminiRequest
				err := json.Unmarshal(body, &request)
				require.NoError(t, err)

				// Validate conversation structure
				assert.Equal(t, 3, len(request.Contents))

				// User message
				assert.Equal(t, "user", request.Contents[0].Role)
				assert.Equal(t, "What's the weather like?", request.Contents[0].Parts[0].Text)

				// Assistant message with tool call
				assert.Equal(t, "model", request.Contents[1].Role)
				assert.Equal(t, 2, len(request.Contents[1].Parts))
				assert.Equal(t, "I'll check the weather for you.", request.Contents[1].Parts[0].Text)
				assert.NotNil(t, request.Contents[1].Parts[1].FunctionCall)
				assert.Equal(t, "get_weather", request.Contents[1].Parts[1].FunctionCall.Name)

				// Tool result (converted to user message with function response)
				assert.Equal(t, "user", request.Contents[2].Role)
				assert.NotNil(t, request.Contents[2].Parts[0].FunctionResponse)
				assert.Equal(t, "get_weather", request.Contents[2].Parts[0].FunctionResponse.Name)

				// Validate tools structure
				assert.NotNil(t, request.Tools)
				assert.Equal(t, 1, len(request.Tools))
				assert.Equal(t, 1, len(request.Tools[0].FunctionDeclarations))
				assert.Equal(t, "get_weather", request.Tools[0].FunctionDeclarations[0].Name)
				assert.Equal(t, "Get current weather", request.Tools[0].FunctionDeclarations[0].Description)

				// Validate the request is valid according to our validation rules
				err = request.Validate()
				assert.NoError(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert messages to Gemini request format
			request, err := client.convertMessages(tt.messages, tt.tools)
			require.NoError(t, err)

			// Build HTTP request
			ctx := context.Background()
			httpReq, err := client.buildHTTPRequest(ctx, tt.expectedMethod, tt.expectedURL, request)
			require.NoError(t, err)

			// Validate HTTP method and URL
			assert.Equal(t, tt.expectedMethod, httpReq.Method)
			assert.Equal(t, tt.expectedURL, httpReq.URL.String())

			// Validate headers
			for key, expectedValue := range tt.expectedHeaders {
				assert.Equal(t, expectedValue, httpReq.Header.Get(key), "Header %s mismatch", key)
			}

			// Read and validate request body
			body, err := io.ReadAll(httpReq.Body)
			require.NoError(t, err)

			// Reset body for potential re-reading
			httpReq.Body = io.NopCloser(bytes.NewReader(body))

			// Validate request body format
			tt.validateRequestBody(t, body)

			// Ensure the body is valid JSON
			var jsonCheck interface{}
			err = json.Unmarshal(body, &jsonCheck)
			assert.NoError(t, err, "Request body should be valid JSON")
		})
	}
}

// TestGeminiAPIStreamingRequestFormat verifies streaming request format compliance
func TestGeminiAPIStreamingRequestFormat(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:        "test-api-key",
			systemMessage: "You are a helpful assistant",
		},
		baseURL: "https://generativelanguage.googleapis.com",
	}

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Tell me a story"},
			},
		},
	}

	// Test streaming URL construction
	methodPath := client.buildGeminiMethodPath("gemini-pro", "streamGenerateContent")
	expectedStreamingURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:streamGenerateContent"
	
	actualURL, err := client.buildRequestURL(methodPath)
	require.NoError(t, err)
	assert.Equal(t, expectedStreamingURL, actualURL)

	// Test that streaming request uses the same format as non-streaming
	request, err := client.convertMessages(messages, nil)
	require.NoError(t, err)

	ctx := context.Background()
	httpReq, err := client.buildHTTPRequest(ctx, "POST", actualURL, request)
	require.NoError(t, err)

	// Validate streaming request has same structure as non-streaming
	assert.Equal(t, "POST", httpReq.Method)
	assert.Equal(t, "application/json", httpReq.Header.Get("Content-Type"))
	assert.Equal(t, "Bearer test-api-key", httpReq.Header.Get("Authorization"))

	// Validate request body is identical to non-streaming format
	body, err := io.ReadAll(httpReq.Body)
	require.NoError(t, err)

	var streamRequest geminiRequest
	err = json.Unmarshal(body, &streamRequest)
	require.NoError(t, err)

	// Should have same structure as non-streaming request
	assert.NotEmpty(t, streamRequest.Contents)
	assert.NotNil(t, streamRequest.SystemInstruction)
	
	err = streamRequest.Validate()
	assert.NoError(t, err)
}

// TestGeminiAPIErrorResponseFormat verifies error response handling compliance
func TestGeminiAPIErrorResponseFormat(t *testing.T) {
	// Test that we can parse standard Gemini API error responses
	errorResponseJSON := `{
		"error": {
			"code": 400,
			"message": "Invalid request format",
			"status": "INVALID_ARGUMENT"
		}
	}`

	var errorResp geminiErrorResponse
	err := json.Unmarshal([]byte(errorResponseJSON), &errorResp)
	require.NoError(t, err)

	assert.Equal(t, 400, errorResp.Error.Code)
	assert.Equal(t, "Invalid request format", errorResp.Error.Message)
	assert.Equal(t, "INVALID_ARGUMENT", errorResp.Error.Status)

	// Validate error response structure
	err = errorResp.Validate()
	assert.NoError(t, err)
}

// TestGeminiAPIResponseFormatCompliance verifies response parsing compliance
func TestGeminiAPIResponseFormatCompliance(t *testing.T) {
	// Test parsing of standard Gemini API response format
	responseJSON := `{
		"candidates": [
			{
				"content": {
					"role": "model",
					"parts": [
						{"text": "Hello! How can I help you today?"}
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
	}`

	var response geminiResponse
	err := json.Unmarshal([]byte(responseJSON), &response)
	require.NoError(t, err)

	// Validate response structure matches Gemini API spec
	assert.Equal(t, 1, len(response.Candidates))
	assert.Equal(t, "model", response.Candidates[0].Content.Role)
	assert.Equal(t, 1, len(response.Candidates[0].Content.Parts))
	assert.Equal(t, "Hello! How can I help you today?", response.Candidates[0].Content.Parts[0].Text)
	assert.Equal(t, "STOP", response.Candidates[0].FinishReason)

	// Validate usage metadata
	assert.NotNil(t, response.UsageMetadata)
	assert.Equal(t, 10, response.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 8, response.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 0, response.UsageMetadata.CachedContentTokenCount)

	// Validate response structure
	err = response.Validate()
	assert.NoError(t, err)
}

// TestGeminiAPIStreamChunkFormatCompliance verifies streaming chunk format compliance
func TestGeminiAPIStreamChunkFormatCompliance(t *testing.T) {
	// Test parsing of Gemini API streaming chunk format
	chunkJSON := `{
		"candidates": [
			{
				"content": {
					"role": "model",
					"parts": [
						{"text": "Hello"}
					]
				}
			}
		]
	}`

	var chunk geminiStreamChunk
	err := json.Unmarshal([]byte(chunkJSON), &chunk)
	require.NoError(t, err)

	// Validate chunk structure
	assert.Equal(t, 1, len(chunk.Candidates))
	assert.Equal(t, "model", chunk.Candidates[0].Content.Role)
	assert.Equal(t, "Hello", chunk.Candidates[0].Content.Parts[0].Text)

	// Validate chunk structure
	err = chunk.Validate()
	assert.NoError(t, err)
}

// TestGeminiAPICompleteURLModeFormat verifies complete URL mode still uses correct request format
func TestGeminiAPICompleteURLModeFormat(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:        "test-api-key",
			systemMessage: "You are a helpful assistant",
		},
		baseURL: "https://custom-proxy.com/gemini/chat#", // Complete URL mode
	}

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Hello"},
			},
		},
	}

	// Convert messages - should use same format regardless of URL mode
	request, err := client.convertMessages(messages, nil)
	require.NoError(t, err)

	// Build HTTP request for complete URL mode
	completeURL := "https://custom-proxy.com/gemini/chat" // Without the # marker
	ctx := context.Background()
	httpReq, err := client.buildHTTPRequest(ctx, "POST", completeURL, request)
	require.NoError(t, err)

	// Validate that complete URL mode uses identical request format
	assert.Equal(t, "POST", httpReq.Method)
	assert.Equal(t, completeURL, httpReq.URL.String())
	assert.Equal(t, "application/json", httpReq.Header.Get("Content-Type"))
	assert.Equal(t, "Bearer test-api-key", httpReq.Header.Get("Authorization"))

	// Validate request body format is identical to standard mode
	body, err := io.ReadAll(httpReq.Body)
	require.NoError(t, err)

	var completeURLRequest geminiRequest
	err = json.Unmarshal(body, &completeURLRequest)
	require.NoError(t, err)

	// Should have identical structure to standard mode
	assert.NotEmpty(t, completeURLRequest.Contents)
	assert.Equal(t, "user", completeURLRequest.Contents[0].Role)
	assert.Equal(t, "Hello", completeURLRequest.Contents[0].Parts[0].Text)
	assert.NotNil(t, completeURLRequest.SystemInstruction)

	err = completeURLRequest.Validate()
	assert.NoError(t, err)
}