package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCustomGeminiClient(t *testing.T) {
	opts := providerClientOptions{
		baseURL: "https://generativelanguage.googleapis.com",
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
	require.NotNil(t, client)

	// Verify the client implements ProviderClient interface
	var _ ProviderClient = client

	// Test that the client can return model information
	model := client.Model()
	assert.Equal(t, "gemini-1.5-pro", model.ID)
	assert.Equal(t, "Gemini 1.5 Pro", model.Name)
}

func TestCustomGeminiClientWithDefaultBaseURL(t *testing.T) {
	opts := providerClientOptions{
		// No baseURL provided - should use default
		apiKey:  "test-api-key",
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{ID: "gemini-1.5-pro"}
		},
	}

	client := newCustomGeminiClient(opts).(*customGeminiClient)
	require.NotNil(t, client)

	// Should use default base URL
	assert.Equal(t, "https://generativelanguage.googleapis.com", client.baseURL)
}

func TestCustomGeminiClientHTTPClientCreation(t *testing.T) {
	opts := providerClientOptions{
		baseURL: "https://generativelanguage.googleapis.com",
		apiKey:  "test-api-key",
		modelType: config.SelectedModelTypeLarge,
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{ID: "gemini-1.5-pro"}
		},
	}

	client := newCustomGeminiClient(opts).(*customGeminiClient)
	require.NotNil(t, client)
	require.NotNil(t, client.httpClient)

	// Verify HTTP client has reasonable timeout
	assert.Greater(t, client.httpClient.Timeout.Seconds(), 0.0)
}

func TestGeminiDataStructures(t *testing.T) {
	t.Run("GeminiRequest structure", func(t *testing.T) {
		request := GeminiRequest{
			Contents: []GeminiContent{
				{
					Parts: []GeminiPart{
						{Text: "Hello, world!"},
					},
					Role: GeminiRoleUser,
				},
			},
			Tools: []GeminiTool{
				{
					FunctionDeclarations: []GeminiFunctionDeclaration{
						{
							Name:        "get_weather",
							Description: "Get current weather",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type": "string",
									},
								},
							},
						},
					},
				},
			},
			GenerationConfig: &GeminiGenerationConfig{
				Temperature:     &[]float64{0.7}[0],
				MaxOutputTokens: &[]int{1000}[0],
			},
		}

		// Verify structure can be created and accessed
		assert.Len(t, request.Contents, 1)
		assert.Equal(t, "Hello, world!", request.Contents[0].Parts[0].Text)
		assert.Equal(t, GeminiRoleUser, request.Contents[0].Role)
		assert.Len(t, request.Tools, 1)
		assert.Equal(t, "get_weather", request.Tools[0].FunctionDeclarations[0].Name)
		assert.Equal(t, 0.7, *request.GenerationConfig.Temperature)
		assert.Equal(t, 1000, *request.GenerationConfig.MaxOutputTokens)
	})

	t.Run("GeminiResponse structure", func(t *testing.T) {
		response := GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "Hello! How can I help you?"},
						},
						Role: GeminiRoleModel,
					},
					FinishReason: GeminiFinishReasonStop,
					Index:        0,
				},
			},
			UsageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:        10,
				CandidatesTokenCount:    8,
				TotalTokenCount:         18,
				CachedContentTokenCount: 5,
			},
		}

		// Verify structure can be created and accessed
		assert.Len(t, response.Candidates, 1)
		assert.Equal(t, "Hello! How can I help you?", response.Candidates[0].Content.Parts[0].Text)
		assert.Equal(t, GeminiRoleModel, response.Candidates[0].Content.Role)
		assert.Equal(t, GeminiFinishReasonStop, response.Candidates[0].FinishReason)
		assert.Equal(t, 10, response.UsageMetadata.PromptTokenCount)
		assert.Equal(t, 8, response.UsageMetadata.CandidatesTokenCount)
		assert.Equal(t, 18, response.UsageMetadata.TotalTokenCount)
		assert.Equal(t, 5, response.UsageMetadata.CachedContentTokenCount)
	})

	t.Run("GeminiFunctionCall and GeminiFunctionResponse", func(t *testing.T) {
		// Test function call structure
		functionCall := GeminiFunctionCall{
			Name: "get_weather",
			Args: map[string]interface{}{
				"location": "San Francisco",
				"units":    "celsius",
			},
		}

		assert.Equal(t, "get_weather", functionCall.Name)
		assert.Equal(t, "San Francisco", functionCall.Args["location"])
		assert.Equal(t, "celsius", functionCall.Args["units"])

		// Test function response structure
		functionResponse := GeminiFunctionResponse{
			Name: "get_weather",
			Response: map[string]interface{}{
				"temperature": 22.5,
				"condition":   "sunny",
			},
		}

		assert.Equal(t, "get_weather", functionResponse.Name)
		assert.Equal(t, 22.5, functionResponse.Response["temperature"])
		assert.Equal(t, "sunny", functionResponse.Response["condition"])
	})

	t.Run("GeminiInlineData structure", func(t *testing.T) {
		inlineData := GeminiInlineData{
			MimeType: "image/jpeg",
			Data:     "base64encodeddata==",
		}

		assert.Equal(t, "image/jpeg", inlineData.MimeType)
		assert.Equal(t, "base64encodeddata==", inlineData.Data)
	})

	t.Run("GeminiError structure", func(t *testing.T) {
		geminiError := GeminiError{
			Error: GeminiErrorDetails{
				Code:    400,
				Message: "Invalid request",
				Status:  "INVALID_ARGUMENT",
			},
		}

		assert.Equal(t, 400, geminiError.Error.Code)
		assert.Equal(t, "Invalid request", geminiError.Error.Message)
		assert.Equal(t, "INVALID_ARGUMENT", geminiError.Error.Status)
	})
}

func TestGeminiConstants(t *testing.T) {
	t.Run("Role constants", func(t *testing.T) {
		assert.Equal(t, "user", GeminiRoleUser)
		assert.Equal(t, "model", GeminiRoleModel)
	})

	t.Run("Finish reason constants", func(t *testing.T) {
		assert.Equal(t, "STOP", GeminiFinishReasonStop)
		assert.Equal(t, "MAX_TOKENS", GeminiFinishReasonMaxTokens)
		assert.Equal(t, "SAFETY", GeminiFinishReasonSafety)
		assert.Equal(t, "RECITATION", GeminiFinishReasonRecitation)
		assert.Equal(t, "OTHER", GeminiFinishReasonOther)
	})

	t.Run("Safety category constants", func(t *testing.T) {
		assert.Equal(t, "HARM_CATEGORY_HARASSMENT", GeminiSafetyCategoryHarassment)
		assert.Equal(t, "HARM_CATEGORY_HATE_SPEECH", GeminiSafetyCategoryHateSpeech)
		assert.Equal(t, "HARM_CATEGORY_SEXUALLY_EXPLICIT", GeminiSafetyCategorySexuallyExplicit)
		assert.Equal(t, "HARM_CATEGORY_DANGEROUS_CONTENT", GeminiSafetyCategoryDangerousContent)
	})

	t.Run("Function calling mode constants", func(t *testing.T) {
		assert.Equal(t, "AUTO", GeminiFunctionCallingModeAuto)
		assert.Equal(t, "ANY", GeminiFunctionCallingModeAny)
		assert.Equal(t, "NONE", GeminiFunctionCallingModeNone)
	})
}

// Mock tool for testing
type mockTool struct {
	name        string
	description string
	parameters  map[string]interface{}
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

func TestConvertMessagesToGemini(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Convert user text message", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello, how are you?"},
				},
			},
		}

		contents, systemInstruction, err := client.convertMessagesToGemini(messages)
		require.NoError(t, err)
		assert.Nil(t, systemInstruction)
		assert.Len(t, contents, 1)
		assert.Equal(t, GeminiRoleUser, contents[0].Role)
		assert.Len(t, contents[0].Parts, 1)
		assert.Equal(t, "Hello, how are you?", contents[0].Parts[0].Text)
	})

	t.Run("Convert assistant text message", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.TextContent{Text: "I'm doing well, thank you!"},
				},
			},
		}

		contents, systemInstruction, err := client.convertMessagesToGemini(messages)
		require.NoError(t, err)
		assert.Nil(t, systemInstruction)
		assert.Len(t, contents, 1)
		assert.Equal(t, GeminiRoleModel, contents[0].Role)
		assert.Len(t, contents[0].Parts, 1)
		assert.Equal(t, "I'm doing well, thank you!", contents[0].Parts[0].Text)
	})

	t.Run("Convert system message to system instruction", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: "You are a helpful assistant."},
				},
			},
		}

		contents, systemInstruction, err := client.convertMessagesToGemini(messages)
		require.NoError(t, err)
		assert.Len(t, contents, 0)
		assert.NotNil(t, systemInstruction)
		assert.Len(t, systemInstruction.Parts, 1)
		assert.Equal(t, "You are a helpful assistant.", systemInstruction.Parts[0].Text)
	})

	t.Run("Convert tool message", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.Tool,
				Parts: []message.ContentPart{
					message.ToolResult{
						ToolCallID: "call_123",
						Name:       "get_weather",
						Content:    "The weather is sunny",
						IsError:    false,
					},
				},
			},
		}

		contents, systemInstruction, err := client.convertMessagesToGemini(messages)
		require.NoError(t, err)
		assert.Nil(t, systemInstruction)
		assert.Len(t, contents, 1)
		assert.Equal(t, GeminiRoleUser, contents[0].Role)
		assert.Len(t, contents[0].Parts, 1)
		assert.NotNil(t, contents[0].Parts[0].FunctionResponse)
		assert.Equal(t, "get_weather", contents[0].Parts[0].FunctionResponse.Name)
		assert.Equal(t, "The weather is sunny", contents[0].Parts[0].FunctionResponse.Response["content"])
	})

	t.Run("Convert mixed messages", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: "You are helpful."},
				},
			},
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "What's the weather?"},
				},
			},
			{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{
						ID:    "call_123",
						Name:  "get_weather",
						Input: `{"location": "San Francisco"}`,
					},
				},
			},
		}

		contents, systemInstruction, err := client.convertMessagesToGemini(messages)
		require.NoError(t, err)
		
		// Check system instruction
		assert.NotNil(t, systemInstruction)
		assert.Equal(t, "You are helpful.", systemInstruction.Parts[0].Text)
		
		// Check contents
		assert.Len(t, contents, 2)
		
		// User message
		assert.Equal(t, GeminiRoleUser, contents[0].Role)
		assert.Equal(t, "What's the weather?", contents[0].Parts[0].Text)
		
		// Assistant message with tool call
		assert.Equal(t, GeminiRoleModel, contents[1].Role)
		assert.NotNil(t, contents[1].Parts[0].FunctionCall)
		assert.Equal(t, "get_weather", contents[1].Parts[0].FunctionCall.Name)
		assert.Equal(t, "San Francisco", contents[1].Parts[0].FunctionCall.Args["location"])
	})
}

func TestConvertPartsToGemini(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Convert text content", func(t *testing.T) {
		parts := []message.ContentPart{
			message.TextContent{Text: "Hello world"},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.Equal(t, "Hello world", geminiParts[0].Text)
	})

	t.Run("Convert reasoning content", func(t *testing.T) {
		parts := []message.ContentPart{
			message.ReasoningContent{Thinking: "Let me think about this..."},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.Equal(t, "Let me think about this...", geminiParts[0].Text)
	})

	t.Run("Convert binary content", func(t *testing.T) {
		imageData := []byte("fake image data")
		parts := []message.ContentPart{
			message.BinaryContent{
				MIMEType: "image/jpeg",
				Data:     imageData,
			},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.NotNil(t, geminiParts[0].InlineData)
		assert.Equal(t, "image/jpeg", geminiParts[0].InlineData.MimeType)
		assert.Equal(t, "ZmFrZSBpbWFnZSBkYXRh", geminiParts[0].InlineData.Data) // base64 of "fake image data"
	})

	t.Run("Convert tool call", func(t *testing.T) {
		parts := []message.ContentPart{
			message.ToolCall{
				ID:    "call_123",
				Name:  "get_weather",
				Input: `{"location": "New York", "units": "celsius"}`,
			},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.NotNil(t, geminiParts[0].FunctionCall)
		assert.Equal(t, "get_weather", geminiParts[0].FunctionCall.Name)
		assert.Equal(t, "New York", geminiParts[0].FunctionCall.Args["location"])
		assert.Equal(t, "celsius", geminiParts[0].FunctionCall.Args["units"])
	})

	t.Run("Convert tool call with invalid JSON", func(t *testing.T) {
		parts := []message.ContentPart{
			message.ToolCall{
				ID:    "call_123",
				Name:  "get_weather",
				Input: "invalid json",
			},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.NotNil(t, geminiParts[0].FunctionCall)
		assert.Equal(t, "get_weather", geminiParts[0].FunctionCall.Name)
		assert.Equal(t, "invalid json", geminiParts[0].FunctionCall.Args["input"])
	})

	t.Run("Convert tool result", func(t *testing.T) {
		parts := []message.ContentPart{
			message.ToolResult{
				ToolCallID: "call_123",
				Name:       "get_weather",
				Content:    "The weather is 22°C and sunny",
				Metadata:   `{"source": "weather_api", "timestamp": "2023-01-01T12:00:00Z"}`,
				IsError:    false,
			},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.NotNil(t, geminiParts[0].FunctionResponse)
		assert.Equal(t, "get_weather", geminiParts[0].FunctionResponse.Name)
		assert.Equal(t, "The weather is 22°C and sunny", geminiParts[0].FunctionResponse.Response["content"])
		
		// Check metadata was parsed as JSON
		metadata, ok := geminiParts[0].FunctionResponse.Response["metadata"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "weather_api", metadata["source"])
	})

	t.Run("Convert tool result with error", func(t *testing.T) {
		parts := []message.ContentPart{
			message.ToolResult{
				ToolCallID: "call_123",
				Name:       "get_weather",
				Content:    "Failed to get weather data",
				IsError:    true,
			},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.NotNil(t, geminiParts[0].FunctionResponse)
		assert.Equal(t, "get_weather", geminiParts[0].FunctionResponse.Name)
		assert.Equal(t, "Failed to get weather data", geminiParts[0].FunctionResponse.Response["content"])
		assert.Equal(t, true, geminiParts[0].FunctionResponse.Response["is_error"])
	})

	t.Run("Skip finish parts", func(t *testing.T) {
		parts := []message.ContentPart{
			message.TextContent{Text: "Hello"},
			message.Finish{Reason: message.FinishReasonEndTurn},
			message.TextContent{Text: "World"},
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 2)
		assert.Equal(t, "Hello", geminiParts[0].Text)
		assert.Equal(t, "World", geminiParts[1].Text)
	})

	t.Run("Skip empty content", func(t *testing.T) {
		parts := []message.ContentPart{
			message.TextContent{Text: ""}, // Empty text should be skipped
			message.TextContent{Text: "Hello"},
			message.BinaryContent{Data: nil}, // Empty binary should be skipped
		}

		geminiParts, err := client.convertPartsToGemini(parts)
		require.NoError(t, err)
		assert.Len(t, geminiParts, 1)
		assert.Equal(t, "Hello", geminiParts[0].Text)
	})
}

func TestConvertToolsToGemini(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Convert empty tools", func(t *testing.T) {
		tools := []tools.BaseTool{}

		geminiTools, err := client.convertToolsToGemini(tools)
		require.NoError(t, err)
		assert.Nil(t, geminiTools)
	})

	t.Run("Convert single tool", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "get_weather",
				description: "Get current weather information",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type":        "string",
							"description": "The city name",
						},
						"units": map[string]interface{}{
							"type": "string",
							"enum": []string{"celsius", "fahrenheit"},
						},
					},
					"required": []string{"location"},
				},
			},
		}

		geminiTools, err := client.convertToolsToGemini(mockTools)
		require.NoError(t, err)
		assert.Len(t, geminiTools, 1)
		assert.Len(t, geminiTools[0].FunctionDeclarations, 1)
		
		funcDecl := geminiTools[0].FunctionDeclarations[0]
		assert.Equal(t, "get_weather", funcDecl.Name)
		assert.Equal(t, "Get current weather information", funcDecl.Description)
		assert.Equal(t, "object", funcDecl.Parameters["type"])
		
		properties, ok := funcDecl.Parameters["properties"].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, properties, "location")
		assert.Contains(t, properties, "units")
	})

	t.Run("Convert multiple tools", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "get_weather",
				description: "Get weather",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{"type": "string"},
					},
				},
			},
			&mockTool{
				name:        "calculate",
				description: "Perform calculation",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"expression": map[string]interface{}{"type": "string"},
					},
				},
			},
		}

		geminiTools, err := client.convertToolsToGemini(mockTools)
		require.NoError(t, err)
		assert.Len(t, geminiTools, 1)
		assert.Len(t, geminiTools[0].FunctionDeclarations, 2)
		
		// Check both function declarations
		funcNames := []string{
			geminiTools[0].FunctionDeclarations[0].Name,
			geminiTools[0].FunctionDeclarations[1].Name,
		}
		assert.Contains(t, funcNames, "get_weather")
		assert.Contains(t, funcNames, "calculate")
	})

	t.Run("Convert tool with nil parameters", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "simple_tool",
				description: "A simple tool",
				parameters:  nil, // nil parameters
			},
		}

		geminiTools, err := client.convertToolsToGemini(mockTools)
		require.NoError(t, err)
		assert.Len(t, geminiTools, 1)
		assert.Len(t, geminiTools[0].FunctionDeclarations, 1)
		
		funcDecl := geminiTools[0].FunctionDeclarations[0]
		assert.Equal(t, "simple_tool", funcDecl.Name)
		assert.Equal(t, "A simple tool", funcDecl.Description)
		
		// Should have default empty object schema
		assert.Equal(t, "object", funcDecl.Parameters["type"])
		properties, ok := funcDecl.Parameters["properties"].(map[string]interface{})
		assert.True(t, ok)
		assert.Empty(t, properties)
	})
}

func TestConvertGeminiResponseToInternal(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Convert empty response", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{},
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)
		assert.Equal(t, "", response.Content)
		assert.Empty(t, response.ToolCalls)
		assert.Equal(t, int64(0), response.Usage.InputTokens)
		assert.Equal(t, int64(0), response.Usage.OutputTokens)
		assert.Equal(t, int64(0), response.Usage.CacheReadTokens)
	})

	t.Run("Convert text response", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "Hello! How can I help you today?"},
						},
					},
					FinishReason: GeminiFinishReasonStop,
				},
			},
			UsageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:        15,
				CandidatesTokenCount:    8,
				TotalTokenCount:         23,
				CachedContentTokenCount: 5,
			},
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)
		assert.Equal(t, "Hello! How can I help you today?", response.Content)
		assert.Empty(t, response.ToolCalls)
		assert.Equal(t, int64(15), response.Usage.InputTokens)
		assert.Equal(t, int64(8), response.Usage.OutputTokens)
		assert.Equal(t, int64(5), response.Usage.CacheReadTokens)
	})

	t.Run("Convert response with function call", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "I'll check the weather for you."},
							{
								FunctionCall: &GeminiFunctionCall{
									Name: "get_weather",
									Args: map[string]interface{}{
										"location": "San Francisco",
										"units":    "celsius",
									},
								},
							},
						},
					},
					FinishReason: GeminiFinishReasonStop,
				},
			},
			UsageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:     20,
				CandidatesTokenCount: 12,
				TotalTokenCount:      32,
			},
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)
		assert.Equal(t, "I'll check the weather for you.", response.Content)
		assert.Len(t, response.ToolCalls, 1)
		
		toolCall := response.ToolCalls[0]
		assert.Equal(t, "get_weather", toolCall.Name)
		assert.NotEmpty(t, toolCall.ID) // Should have generated an ID
		
		// Parse the input JSON to verify it contains the correct args
		var args map[string]interface{}
		err = json.Unmarshal([]byte(toolCall.Input), &args)
		require.NoError(t, err)
		assert.Equal(t, "San Francisco", args["location"])
		assert.Equal(t, "celsius", args["units"])
		
		assert.Equal(t, int64(20), response.Usage.InputTokens)
		assert.Equal(t, int64(12), response.Usage.OutputTokens)
		assert.Equal(t, int64(0), response.Usage.CacheReadTokens) // Not specified in this response
	})

	t.Run("Convert response with multiple text parts", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "First part. "},
							{Text: "Second part. "},
							{Text: "Third part."},
						},
					},
					FinishReason: GeminiFinishReasonStop,
				},
			},
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)
		assert.Equal(t, "First part. Second part. Third part.", response.Content)
		assert.Empty(t, response.ToolCalls)
	})

	t.Run("Convert response with no usage metadata", func(t *testing.T) {
		geminiResponse := &GeminiResponse{
			Candidates: []GeminiCandidate{
				{
					Content: &GeminiContent{
						Parts: []GeminiPart{
							{Text: "Response without usage metadata"},
						},
					},
				},
			},
			// No UsageMetadata
		}

		response, err := client.convertGeminiResponseToInternal(geminiResponse)
		require.NoError(t, err)
		assert.Equal(t, "Response without usage metadata", response.Content)
		assert.Equal(t, int64(0), response.Usage.InputTokens)
		assert.Equal(t, int64(0), response.Usage.OutputTokens)
		assert.Equal(t, int64(0), response.Usage.CacheReadTokens)
	})
}

func TestGenerateToolCallID(t *testing.T) {
	// Test that generateToolCallID produces unique IDs
	id1 := generateToolCallID()
	id2 := generateToolCallID()
	
	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "call_")
	assert.Contains(t, id2, "call_")
}