package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiRequestSerialization(t *testing.T) {
	tests := []struct {
		name     string
		request  geminiRequest
		expected string
	}{
		{
			name: "basic request with text content",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role: "user",
						Parts: []geminiPart{
							{Text: "Hello, world!"},
						},
					},
				},
			},
			expected: `{"contents":[{"role":"user","parts":[{"text":"Hello, world!"}]}]}`,
		},
		{
			name: "request with system instruction",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role: "user",
						Parts: []geminiPart{
							{Text: "What is Go?"},
						},
					},
				},
				SystemInstruction: &geminiContent{
					Role: "user",
					Parts: []geminiPart{
						{Text: "You are a helpful programming assistant"},
					},
				},
			},
			expected: `{"contents":[{"role":"user","parts":[{"text":"What is Go?"}]}],"systemInstruction":{"role":"user","parts":[{"text":"You are a helpful programming assistant"}]}}`,
		},
		{
			name: "request with function call",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role: "model",
						Parts: []geminiPart{
							{
								FunctionCall: &geminiFunctionCall{
									Name: "get_weather",
									Args: map[string]interface{}{
										"location": "San Francisco",
									},
								},
							},
						},
					},
				},
			},
			expected: `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"location":"San Francisco"}}}]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.request)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))
		})
	}
}

func TestGeminiResponseDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected geminiResponse
	}{
		{
			name: "basic response",
			json: `{
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
					"candidatesTokenCount": 8
				}
			}`,
			expected: geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{
								{Text: "Hello! How can I help you today?"},
							},
						},
						FinishReason: "STOP",
					},
				},
				UsageMetadata: &geminiUsage{
					PromptTokenCount:     10,
					CandidatesTokenCount: 8,
				},
			},
		},
		{
			name: "response with function call",
			json: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{
									"functionCall": {
										"name": "get_weather",
										"args": {"location": "New York"}
									}
								}
							]
						},
						"finishReason": "FUNCTION_CALL"
					}
				]
			}`,
			expected: geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{
								{
									FunctionCall: &geminiFunctionCall{
										Name: "get_weather",
										Args: map[string]interface{}{
											"location": "New York",
										},
									},
								},
							},
						},
						FinishReason: "FUNCTION_CALL",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response geminiResponse
			err := json.Unmarshal([]byte(tt.json), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, response)
		})
	}
}

func TestConvertMessages(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			systemMessage: "You are a helpful assistant",
		},
	}

	tests := []struct {
		name     string
		messages []message.Message
		tools    []tools.BaseTool
		expected *geminiRequest
	}{
		{
			name: "simple user message",
			messages: []message.Message{
				{
					Role: message.User,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Hello"},
					},
				},
			},
			expected: &geminiRequest{
				Contents: []geminiContent{
					{
						Role: "user",
						Parts: []geminiPart{
							{Text: "Hello"},
						},
					},
				},
				SystemInstruction: &geminiContent{
					Role: "user",
					Parts: []geminiPart{
						{Text: "You are a helpful assistant"},
					},
				},
			},
		},
		{
			name: "assistant message with text",
			messages: []message.Message{
				{
					Role: message.Assistant,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Hi there!"},
					},
				},
			},
			expected: &geminiRequest{
				Contents: []geminiContent{
					{
						Role: "model",
						Parts: []geminiPart{
							{Text: "Hi there!"},
						},
					},
				},
				SystemInstruction: &geminiContent{
					Role: "user",
					Parts: []geminiPart{
						{Text: "You are a helpful assistant"},
					},
				},
			},
		},
		{
			name: "user message with binary content",
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
			expected: &geminiRequest{
				Contents: []geminiContent{
					{
						Role: "user",
						Parts: []geminiPart{
							{Text: "What's in this image?"},
							{
								InlineData: &geminiInlineData{
									MimeType: "image/jpeg",
									Data:     "ZmFrZS1pbWFnZS1kYXRh", // base64 of "fake-image-data"
								},
							},
						},
					},
				},
				SystemInstruction: &geminiContent{
					Role: "user",
					Parts: []geminiPart{
						{Text: "You are a helpful assistant"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.convertMessages(tt.messages, tt.tools)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUserMessage(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		message  message.Message
		expected *geminiContent
	}{
		{
			name: "text only message",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello world"},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Hello world"},
				},
			},
		},
		{
			name: "message with binary content",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.BinaryContent{
						MIMEType: "image/png",
						Data:     []byte("test-image-data"),
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{
						InlineData: &geminiInlineData{
							MimeType: "image/png",
							Data:     "dGVzdC1pbWFnZS1kYXRh", // base64 of "test-image-data"
						},
					},
				},
			},
		},
		{
			name: "message with tool result",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.ToolResult{
						ToolCallID: "call_123",
						Name:       "get_weather",
						Content:    "Sunny, 75°F",
						IsError:    false,
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{
						FunctionResponse: &geminiFunctionResponse{
							Name: "get_weather",
							Response: map[string]interface{}{
								"content":  "Sunny, 75°F",
								"metadata": "",
								"is_error": false,
							},
						},
					},
				},
			},
		},
		{
			name: "message with tool result error",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.ToolResult{
						ToolCallID: "call_456",
						Name:       "get_weather",
						Content:    "API error: invalid location",
						Metadata:   "error_code: 404",
						IsError:    true,
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{
						FunctionResponse: &geminiFunctionResponse{
							Name: "get_weather",
							Response: map[string]interface{}{
								"content":  "API error: invalid location",
								"metadata": "error_code: 404",
								"is_error": true,
							},
						},
					},
				},
			},
		},
		{
			name: "message with text and binary content",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "What's in this image?"},
					message.BinaryContent{
						MIMEType: "image/jpeg",
						Data:     []byte("jpeg-data"),
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "What's in this image?"},
					{
						InlineData: &geminiInlineData{
							MimeType: "image/jpeg",
							Data:     "anBlZy1kYXRh", // base64 of "jpeg-data"
						},
					},
				},
			},
		},
		{
			name: "message with multiple binary contents",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Compare these images:"},
					message.BinaryContent{
						MIMEType: "image/png",
						Data:     []byte("image1"),
					},
					message.BinaryContent{
						MIMEType: "image/jpeg",
						Data:     []byte("image2"),
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Compare these images:"},
					{
						InlineData: &geminiInlineData{
							MimeType: "image/png",
							Data:     "aW1hZ2Ux", // base64 of "image1"
						},
					},
					{
						InlineData: &geminiInlineData{
							MimeType: "image/jpeg",
							Data:     "aW1hZ2Uy", // base64 of "image2"
						},
					},
				},
			},
		},
		{
			name: "message with multiple tool results",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.ToolResult{
						ToolCallID: "call_1",
						Name:       "get_weather",
						Content:    "Sunny, 75°F",
						IsError:    false,
					},
					message.ToolResult{
						ToolCallID: "call_2",
						Name:       "get_time",
						Content:    "2:30 PM EST",
						IsError:    false,
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{
						FunctionResponse: &geminiFunctionResponse{
							Name: "get_weather",
							Response: map[string]interface{}{
								"content":  "Sunny, 75°F",
								"metadata": "",
								"is_error": false,
							},
						},
					},
					{
						FunctionResponse: &geminiFunctionResponse{
							Name: "get_time",
							Response: map[string]interface{}{
								"content":  "2:30 PM EST",
								"metadata": "",
								"is_error": false,
							},
						},
					},
				},
			},
		},
		{
			name: "empty message",
			message: message.Message{
				Role:  message.User,
				Parts: []message.ContentPart{},
			},
			expected: &geminiContent{
				Role:  "user",
				Parts: []geminiPart{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertUserMessage(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertAssistantMessage(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		message  message.Message
		expected *geminiContent
	}{
		{
			name: "text only message",
			message: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.TextContent{Text: "I can help you with that!"},
				},
			},
			expected: &geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{Text: "I can help you with that!"},
				},
			},
		},
		{
			name: "message with tool call",
			message: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{
						ID:    "call_123",
						Name:  "get_weather",
						Input: `{"location": "San Francisco"}`,
					},
				},
			},
			expected: &geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{
						FunctionCall: &geminiFunctionCall{
							Name: "get_weather",
							Args: map[string]interface{}{
								"location": "San Francisco",
							},
						},
					},
				},
			},
		},
		{
			name: "message with invalid JSON tool call",
			message: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{
						ID:    "call_123",
						Name:  "simple_tool",
						Input: "plain text input",
					},
				},
			},
			expected: &geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{
						FunctionCall: &geminiFunctionCall{
							Name: "simple_tool",
							Args: map[string]interface{}{
								"input": "plain text input",
							},
						},
					},
				},
			},
		},
		{
			name: "message with text and tool call",
			message: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Let me check the weather for you."},
					message.ToolCall{
						ID:    "call_456",
						Name:  "get_weather",
						Input: `{"location": "New York"}`,
					},
				},
			},
			expected: &geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{Text: "Let me check the weather for you."},
					{
						FunctionCall: &geminiFunctionCall{
							Name: "get_weather",
							Args: map[string]interface{}{
								"location": "New York",
							},
						},
					},
				},
			},
		},
		{
			name: "message with multiple tool calls",
			message: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{
						ID:    "call_1",
						Name:  "get_weather",
						Input: `{"location": "Boston"}`,
					},
					message.ToolCall{
						ID:    "call_2",
						Name:  "get_time",
						Input: `{"timezone": "EST"}`,
					},
				},
			},
			expected: &geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{
						FunctionCall: &geminiFunctionCall{
							Name: "get_weather",
							Args: map[string]interface{}{
								"location": "Boston",
							},
						},
					},
					{
						FunctionCall: &geminiFunctionCall{
							Name: "get_time",
							Args: map[string]interface{}{
								"timezone": "EST",
							},
						},
					},
				},
			},
		},
		{
			name: "empty message",
			message: message.Message{
				Role:  message.Assistant,
				Parts: []message.ContentPart{},
			},
			expected: &geminiContent{
				Role:  "model",
				Parts: []geminiPart{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertAssistantMessage(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Mock tool for testing
type mockTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (m *mockTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        m.name,
		Description: m.description,
		Parameters:  m.parameters,
	}
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Run(ctx context.Context, params tools.ToolCall) (tools.ToolResponse, error) {
	return tools.NewTextResponse("mock response"), nil
}

func TestConvertTools(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		tools    []tools.BaseTool
		expected []geminiTool
	}{
		{
			name:     "no tools",
			tools:    []tools.BaseTool{},
			expected: nil,
		},
		{
			name: "single tool",
			tools: []tools.BaseTool{
				&mockTool{
					name:        "get_weather",
					description: "Get current weather for a location",
					parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{
								"type":        "string",
								"description": "The city and state",
							},
						},
						"required": []string{"location"},
					},
				},
			},
			expected: []geminiTool{
				{
					FunctionDeclarations: []geminiFunctionDeclaration{
						{
							Name:        "get_weather",
							Description: "Get current weather for a location",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{
										"type":        "string",
										"description": "The city and state",
									},
								},
								"required": []string{"location"},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple tools",
			tools: []tools.BaseTool{
				&mockTool{
					name:        "get_weather",
					description: "Get weather",
					parameters:  map[string]any{"type": "object"},
				},
				&mockTool{
					name:        "send_email",
					description: "Send an email",
					parameters:  map[string]any{"type": "object"},
				},
			},
			expected: []geminiTool{
				{
					FunctionDeclarations: []geminiFunctionDeclaration{
						{
							Name:        "get_weather",
							Description: "Get weather",
							Parameters:  map[string]interface{}{"type": "object"},
						},
						{
							Name:        "send_email",
							Description: "Send an email",
							Parameters:  map[string]interface{}{"type": "object"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertTools(tt.tools)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildHTTPRequest(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey: "test-api-key",
		},
	}

	request := &geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Hello"},
				},
			},
		},
	}

	ctx := context.Background()
	req, err := client.buildHTTPRequest(ctx, "POST", "https://api.example.com/test", request)
	require.NoError(t, err)

	// Check method and URL
	assert.Equal(t, "POST", req.Method)
	assert.Equal(t, "https://api.example.com/test", req.URL.String())

	// Check headers
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, "Bearer test-api-key", req.Header.Get("Authorization"))
	assert.Equal(t, "Crush/1.0", req.Header.Get("User-Agent"))

	// Check body
	var body geminiRequest
	err = json.NewDecoder(req.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, request, &body)
}

func TestBuildHTTPRequestWithoutAPIKey(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey: "", // No API key
		},
	}

	request := &geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Hello"},
				},
			},
		},
	}

	ctx := context.Background()
	req, err := client.buildHTTPRequest(ctx, "POST", "https://api.example.com/test", request)
	require.NoError(t, err)

	// Should not have Authorization header when no API key is provided
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestConvertSystemMessage(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		message  message.Message
		expected *geminiContent
	}{
		{
			name: "system message converted as user",
			message: message.Message{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: "You are a helpful assistant"},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "You are a helpful assistant"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertMessage(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToolMessage(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		message  message.Message
		expected *geminiContent
	}{
		{
			name: "tool message with text content",
			message: message.Message{
				Role: message.Tool,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Tool execution result"},
				},
			},
			expected: &geminiContent{
				Role: "user", // Tool messages are converted to user messages
				Parts: []geminiPart{
					{Text: "Tool execution result"},
				},
			},
		},
		{
			name: "tool message with tool result",
			message: message.Message{
				Role: message.Tool,
				Parts: []message.ContentPart{
					message.ToolResult{
						ToolCallID: "call_123",
						Name:       "calculator",
						Content:    "42",
						IsError:    false,
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{
						FunctionResponse: &geminiFunctionResponse{
							Name: "calculator",
							Response: map[string]interface{}{
								"content":  "42",
								"metadata": "",
								"is_error": false,
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.convertToolMessage(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertMessageWithImageURL(t *testing.T) {
	client := &customGeminiClient{}

	message := message.Message{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Describe this image:"},
			message.ImageURLContent{
				URL:    "https://example.com/image.jpg",
				Detail: "high",
			},
		},
	}

	result := client.convertUserMessage(message)

	expected := &geminiContent{
		Role: "user",
		Parts: []geminiPart{
			{Text: "Describe this image:"},
			{Text: "[Image URL: https://example.com/image.jpg]"},
		},
	}

	assert.Equal(t, expected, result)
}

func TestConvertMessageWithReasoningContent(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name     string
		message  message.Message
		expected *geminiContent
	}{
		{
			name: "user message with reasoning content",
			message: message.Message{
				Role: message.User,
				Parts: []message.ContentPart{
					message.ReasoningContent{
						Thinking: "Let me think about this step by step...",
					},
				},
			},
			expected: &geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "[Thinking: Let me think about this step by step...]"},
				},
			},
		},
		{
			name: "assistant message with reasoning content",
			message: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ReasoningContent{
						Thinking: "I need to analyze this problem...",
					},
					message.TextContent{Text: "Based on my analysis, the answer is 42."},
				},
			},
			expected: &geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{Text: "[Thinking: I need to analyze this problem...]"},
					{Text: "Based on my analysis, the answer is 42."},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result *geminiContent
			switch tt.message.Role {
			case message.User:
				result = client.convertUserMessage(tt.message)
			case message.Assistant:
				result = client.convertAssistantMessage(tt.message)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertMessageWithFinishPart(t *testing.T) {
	client := &customGeminiClient{}

	message := message.Message{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Here's the answer."},
			message.Finish{
				Reason: message.FinishReasonEndTurn,
				Time:   1234567890,
			},
		},
	}

	result := client.convertAssistantMessage(message)

	// Finish parts should be ignored in the conversion
	expected := &geminiContent{
		Role: "model",
		Parts: []geminiPart{
			{Text: "Here's the answer."},
		},
	}

	assert.Equal(t, expected, result)
}

func TestConvertComplexMessage(t *testing.T) {
	client := &customGeminiClient{}

	// Test a complex message with multiple content types
	message := message.Message{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Please analyze this data:"},
			message.BinaryContent{
				MIMEType: "application/json",
				Data:     []byte(`{"key": "value"}`),
			},
			message.ImageURLContent{
				URL: "https://example.com/chart.png",
			},
			message.ToolResult{
				ToolCallID: "call_123",
				Name:       "data_processor",
				Content:    "Processing complete",
				IsError:    false,
			},
		},
	}

	result := client.convertUserMessage(message)

	expected := &geminiContent{
		Role: "user",
		Parts: []geminiPart{
			{Text: "Please analyze this data:"},
			{
				InlineData: &geminiInlineData{
					MimeType: "application/json",
					Data:     "eyJrZXkiOiAidmFsdWUifQ==", // base64 of `{"key": "value"}`
				},
			},
			{Text: "[Image URL: https://example.com/chart.png]"},
			{
				FunctionResponse: &geminiFunctionResponse{
					Name: "data_processor",
					Response: map[string]interface{}{
						"content":  "Processing complete",
						"metadata": "",
						"is_error": false,
					},
				},
			},
		},
	}

	assert.Equal(t, expected, result)
}

func TestConvertMessagesComprehensive(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			systemMessage:      "You are a helpful AI assistant",
			systemPromptPrefix: "IMPORTANT:",
			maxTokens:          1000,
		},
	}

	// Create a comprehensive conversation with all message types and content types
	messages := []message.Message{
		{
			Role: message.System,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Additional system context"},
			},
		},
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Hello, can you help me analyze this image?"},
				message.BinaryContent{
					MIMEType: "image/jpeg",
					Data:     []byte("fake-image-data"),
				},
				message.ImageURLContent{
					URL:    "https://example.com/image.jpg",
					Detail: "high",
				},
			},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{
					Thinking: "I need to analyze this image carefully...",
				},
				message.TextContent{Text: "I'll analyze the image for you. Let me use a tool to get more details."},
				message.ToolCall{
					ID:    "call_123",
					Name:  "image_analyzer",
					Input: `{"image_url": "https://example.com/image.jpg", "detail": "high"}`,
				},
			},
		},
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "call_123",
					Name:       "image_analyzer",
					Content:    "The image shows a beautiful sunset over mountains",
					Metadata:   "confidence: 0.95",
					IsError:    false,
				},
			},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Based on the analysis, the image shows a beautiful sunset over mountains."},
				message.Finish{
					Reason: message.FinishReasonEndTurn,
					Time:   1234567890,
				},
			},
		},
	}

	tools := []tools.BaseTool{
		&mockTool{
			name:        "image_analyzer",
			description: "Analyze images and provide descriptions",
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"image_url": map[string]any{
						"type":        "string",
						"description": "URL of the image to analyze",
					},
				},
			},
		},
	}

	result, err := client.convertMessages(messages, tools)
	require.NoError(t, err)

	// Verify system instruction
	assert.NotNil(t, result.SystemInstruction)
	assert.Equal(t, "user", result.SystemInstruction.Role)
	assert.Equal(t, "IMPORTANT:\nYou are a helpful AI assistant", result.SystemInstruction.Parts[0].Text)

	// Verify generation config
	assert.NotNil(t, result.GenerationConfig)
	assert.Equal(t, 1000, result.GenerationConfig.MaxOutputTokens)

	// Verify tools
	assert.Len(t, result.Tools, 1)
	assert.Len(t, result.Tools[0].FunctionDeclarations, 1)
	assert.Equal(t, "image_analyzer", result.Tools[0].FunctionDeclarations[0].Name)

	// Verify contents (should have 5 messages: system, user, assistant, tool, assistant)
	assert.Len(t, result.Contents, 5)

	// Verify system message (converted to user)
	assert.Equal(t, "user", result.Contents[0].Role)
	assert.Equal(t, "Additional system context", result.Contents[0].Parts[0].Text)

	// Verify user message with multiple content types
	assert.Equal(t, "user", result.Contents[1].Role)
	assert.Len(t, result.Contents[1].Parts, 3) // text + binary + image URL
	assert.Equal(t, "Hello, can you help me analyze this image?", result.Contents[1].Parts[0].Text)
	assert.NotNil(t, result.Contents[1].Parts[1].InlineData)
	assert.Equal(t, "image/jpeg", result.Contents[1].Parts[1].InlineData.MimeType)
	assert.Equal(t, "[Image URL: https://example.com/image.jpg]", result.Contents[1].Parts[2].Text)

	// Verify assistant message with reasoning and tool call
	assert.Equal(t, "model", result.Contents[2].Role)
	assert.Len(t, result.Contents[2].Parts, 3) // reasoning + text + tool call
	assert.Equal(t, "[Thinking: I need to analyze this image carefully...]", result.Contents[2].Parts[0].Text)
	assert.Equal(t, "I'll analyze the image for you. Let me use a tool to get more details.", result.Contents[2].Parts[1].Text)
	assert.NotNil(t, result.Contents[2].Parts[2].FunctionCall)
	assert.Equal(t, "image_analyzer", result.Contents[2].Parts[2].FunctionCall.Name)

	// Verify tool message (converted to user with function response)
	assert.Equal(t, "user", result.Contents[3].Role)
	assert.Len(t, result.Contents[3].Parts, 1)
	assert.NotNil(t, result.Contents[3].Parts[0].FunctionResponse)
	assert.Equal(t, "image_analyzer", result.Contents[3].Parts[0].FunctionResponse.Name)

	// Verify final assistant message (finish part should be ignored)
	assert.Equal(t, "model", result.Contents[4].Role)
	assert.Len(t, result.Contents[4].Parts, 1) // Only text, finish part ignored
	assert.Equal(t, "Based on the analysis, the image shows a beautiful sunset over mountains.", result.Contents[4].Parts[0].Text)

	// Verify the request is valid
	err = result.Validate()
	assert.NoError(t, err)
}

func TestGeminiStreamChunkDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected geminiStreamChunk
	}{
		{
			name: "stream chunk with content",
			json: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{"text": "Hello"}
							]
						},
						"finishReason": "STOP"
					}
				]
			}`,
			expected: geminiStreamChunk{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{
								{Text: "Hello"},
							},
						},
						FinishReason: "STOP",
					},
				},
			},
		},
		{
			name: "stream chunk with usage metadata only",
			json: `{
				"usageMetadata": {
					"promptTokenCount": 5,
					"candidatesTokenCount": 3
				}
			}`,
			expected: geminiStreamChunk{
				UsageMetadata: &geminiUsage{
					PromptTokenCount:     5,
					CandidatesTokenCount: 3,
				},
			},
		},
		{
			name:     "empty stream chunk",
			json:     `{}`,
			expected: geminiStreamChunk{},
		},
		{
			name: "stream chunk with partial content",
			json: `{
				"candidates": [
					{
						"content": {
							"role": "model",
							"parts": [
								{"text": " world!"}
							]
						}
					}
				]
			}`,
			expected: geminiStreamChunk{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{
								{Text: " world!"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk geminiStreamChunk
			err := json.Unmarshal([]byte(tt.json), &chunk)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, chunk)
		})
	}
}
