package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	required    []string
}

func (m *mockTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        m.name,
		Description: m.description,
		Parameters:  m.parameters,
		Required:    m.required,
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

	t.Run("Convert tool with required fields", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "required_tool",
				description: "A tool with required fields",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message": map[string]interface{}{
							"type":        "string",
							"description": "The message to process",
						},
						"optional_param": map[string]interface{}{
							"type":        "number",
							"description": "An optional parameter",
						},
					},
				},
				required: []string{"message"},
			},
		}

		geminiTools, err := client.convertToolsToGemini(mockTools)
		require.NoError(t, err)
		assert.Len(t, geminiTools, 1)
		assert.Len(t, geminiTools[0].FunctionDeclarations, 1)
		
		funcDecl := geminiTools[0].FunctionDeclarations[0]
		assert.Equal(t, "required_tool", funcDecl.Name)
		
		// Check that required fields are properly converted
		required, ok := funcDecl.Parameters["required"].([]string)
		assert.True(t, ok)
		assert.Equal(t, []string{"message"}, required)
	})

	t.Run("Convert tool with nested objects", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "nested_tool",
				description: "A tool with nested objects",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"config": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"timeout": map[string]interface{}{
									"type":        "number",
									"description": "Timeout in seconds",
								},
								"retries": map[string]interface{}{
									"type":        "integer",
									"description": "Number of retries",
								},
							},
							"required": []string{"timeout"},
						},
					},
				},
				required: []string{"config"},
			},
		}

		geminiTools, err := client.convertToolsToGemini(mockTools)
		require.NoError(t, err)
		assert.Len(t, geminiTools, 1)
		
		funcDecl := geminiTools[0].FunctionDeclarations[0]
		properties, ok := funcDecl.Parameters["properties"].(map[string]interface{})
		assert.True(t, ok)
		
		config, ok := properties["config"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "object", config["type"])
		
		configProps, ok := config["properties"].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, configProps, "timeout")
		assert.Contains(t, configProps, "retries")
		
		// Check nested required fields are converted to []interface{}
		configRequired, ok := config["required"].([]interface{})
		assert.True(t, ok)
		assert.Equal(t, []interface{}{"timeout"}, configRequired)
	})

	t.Run("Convert tool with arrays", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "array_tool",
				description: "A tool with array parameters",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"items": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"name": map[string]interface{}{
										"type": "string",
									},
									"value": map[string]interface{}{
										"type": "string",
									},
								},
								"required": []string{"name"},
							},
						},
					},
				},
				required: []string{"items"},
			},
		}

		geminiTools, err := client.convertToolsToGemini(mockTools)
		require.NoError(t, err)
		assert.Len(t, geminiTools, 1)
		
		funcDecl := geminiTools[0].FunctionDeclarations[0]
		properties, ok := funcDecl.Parameters["properties"].(map[string]interface{})
		assert.True(t, ok)
		
		items, ok := properties["items"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "array", items["type"])
		
		itemsSchema, ok := items["items"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "object", itemsSchema["type"])
		
		// Check array item required fields are converted
		itemRequired, ok := itemsSchema["required"].([]interface{})
		assert.True(t, ok)
		assert.Equal(t, []interface{}{"name"}, itemRequired)
	})

	t.Run("Error on missing tool name", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "", // Missing name
				description: "A tool without name",
				parameters:  nil,
			},
		}

		_, err := client.convertToolsToGemini(mockTools)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tool name is required")
	})

	t.Run("Error on missing tool description", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "test_tool",
				description: "", // Missing description
				parameters:  nil,
			},
		}

		_, err := client.convertToolsToGemini(mockTools)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tool description is required")
	})

	t.Run("Error on required field not in properties", func(t *testing.T) {
		mockTools := []tools.BaseTool{
			&mockTool{
				name:        "invalid_tool",
				description: "A tool with invalid required field",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message": map[string]interface{}{
							"type": "string",
						},
					},
				},
				required: []string{"missing_field"}, // Field not in properties
			},
		}

		_, err := client.convertToolsToGemini(mockTools)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required field missing_field not found in properties")
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

func TestValidateToolDefinition(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Valid tool definition", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "valid_tool",
			Description: "A valid tool",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"param1": map[string]interface{}{
						"type": "string",
					},
				},
			},
			Required: []string{"param1"},
		}

		err := client.validateToolDefinition(toolInfo)
		assert.NoError(t, err)
	})

	t.Run("Missing name", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "",
			Description: "A tool without name",
		}

		err := client.validateToolDefinition(toolInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tool name is required")
	})

	t.Run("Missing description", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "test_tool",
			Description: "",
		}

		err := client.validateToolDefinition(toolInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tool description is required")
	})

	t.Run("Required field not in properties", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "invalid_tool",
			Description: "Invalid tool",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"param1": map[string]interface{}{
						"type": "string",
					},
				},
			},
			Required: []string{"missing_param"},
		}

		err := client.validateToolDefinition(toolInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required field missing_param not found in properties")
	})

	t.Run("Required fields but no properties", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "invalid_tool",
			Description: "Invalid tool",
			Parameters: map[string]interface{}{
				"type": "object",
			},
			Required: []string{"param1"},
		}

		err := client.validateToolDefinition(toolInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tool has required fields but no properties defined")
	})

	t.Run("Properties not an object", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "invalid_tool",
			Description: "Invalid tool",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": "not an object",
			},
			Required: []string{"param1"},
		}

		err := client.validateToolDefinition(toolInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "properties must be an object")
	})

	t.Run("Tool with nil parameters", func(t *testing.T) {
		toolInfo := tools.ToolInfo{
			Name:        "simple_tool",
			Description: "A simple tool",
			Parameters:  nil,
			Required:    nil,
		}

		err := client.validateToolDefinition(toolInfo)
		assert.NoError(t, err)
	})
}

func TestConvertParameterSchema(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("Nil schema", func(t *testing.T) {
		result, err := client.convertParameterSchema(nil, nil)
		assert.NoError(t, err)
		
		expected := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
		assert.Equal(t, expected, result)
	})

	t.Run("Simple object schema", func(t *testing.T) {
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type": "string",
				},
			},
		}
		required := []string{"name"}

		result, err := client.convertParameterSchema(schema, required)
		assert.NoError(t, err)
		
		assert.Equal(t, "object", result["type"])
		assert.Equal(t, required, result["required"])
		
		properties, ok := result["properties"].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, properties, "name")
	})

	t.Run("Nested object schema", func(t *testing.T) {
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"config": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"timeout": map[string]interface{}{
							"type": "number",
						},
					},
					"required": []string{"timeout"},
				},
			},
		}
		required := []string{"config"}

		result, err := client.convertParameterSchema(schema, required)
		assert.NoError(t, err)
		
		properties, ok := result["properties"].(map[string]interface{})
		assert.True(t, ok)
		
		config, ok := properties["config"].(map[string]interface{})
		assert.True(t, ok)
		
		// Check that nested required fields are converted to []interface{}
		configRequired, ok := config["required"].([]interface{})
		assert.True(t, ok)
		assert.Equal(t, []interface{}{"timeout"}, configRequired)
	})
}

func TestConvertSchemaValue(t *testing.T) {
	client := &customGeminiClient{}

	t.Run("String value", func(t *testing.T) {
		result, err := client.convertSchemaValue("test")
		assert.NoError(t, err)
		assert.Equal(t, "test", result)
	})

	t.Run("Number value", func(t *testing.T) {
		result, err := client.convertSchemaValue(42)
		assert.NoError(t, err)
		assert.Equal(t, 42, result)
	})

	t.Run("Boolean value", func(t *testing.T) {
		result, err := client.convertSchemaValue(true)
		assert.NoError(t, err)
		assert.Equal(t, true, result)
	})

	t.Run("Simple object", func(t *testing.T) {
		value := map[string]interface{}{
			"type":        "string",
			"description": "A string field",
		}

		result, err := client.convertSchemaValue(value)
		assert.NoError(t, err)
		
		resultMap, ok := result.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "string", resultMap["type"])
		assert.Equal(t, "A string field", resultMap["description"])
	})

	t.Run("Nested object", func(t *testing.T) {
		value := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nested": map[string]interface{}{
					"type": "string",
				},
			},
		}

		result, err := client.convertSchemaValue(value)
		assert.NoError(t, err)
		
		resultMap, ok := result.(map[string]interface{})
		assert.True(t, ok)
		
		properties, ok := resultMap["properties"].(map[string]interface{})
		assert.True(t, ok)
		
		nested, ok := properties["nested"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "string", nested["type"])
	})

	t.Run("String array", func(t *testing.T) {
		value := []string{"item1", "item2"}

		result, err := client.convertSchemaValue(value)
		assert.NoError(t, err)
		
		resultArray, ok := result.([]interface{})
		assert.True(t, ok)
		assert.Equal(t, []interface{}{"item1", "item2"}, resultArray)
	})

	t.Run("Interface array", func(t *testing.T) {
		value := []interface{}{
			"string",
			42,
			map[string]interface{}{
				"type": "object",
			},
		}

		result, err := client.convertSchemaValue(value)
		assert.NoError(t, err)
		
		resultArray, ok := result.([]interface{})
		assert.True(t, ok)
		assert.Len(t, resultArray, 3)
		assert.Equal(t, "string", resultArray[0])
		assert.Equal(t, 42, resultArray[1])
		
		objItem, ok := resultArray[2].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "object", objItem["type"])
	})
}

func TestCustomGeminiSendMethod(t *testing.T) {
	client := &customGeminiClient{
		baseURL: "https://generativelanguage.googleapis.com",
		apiKey:  "test-api-key",
		providerOptions: providerClientOptions{
			maxTokens: 1000,
			model: func(config.SelectedModelType) catwalk.Model {
				return catwalk.Model{
					ID:                "gemini-1.5-pro",
					Name:              "Gemini 1.5 Pro",
					ContextWindow:     2097152,
					DefaultMaxTokens:  8192,
				}
			},
		},
	}

	t.Run("buildGeminiRequest with simple messages", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello, how are you?"},
				},
			},
		}

		request, err := client.buildGeminiRequest(messages, nil)
		require.NoError(t, err)
		assert.NotNil(t, request)
		assert.Len(t, request.Contents, 1)
		assert.Equal(t, GeminiRoleUser, request.Contents[0].Role)
		assert.Equal(t, "Hello, how are you?", request.Contents[0].Parts[0].Text)
		assert.Nil(t, request.SystemInstruction)
		assert.Nil(t, request.Tools)
		assert.NotNil(t, request.GenerationConfig)
		assert.Equal(t, 1000, *request.GenerationConfig.MaxOutputTokens)
	})

	t.Run("buildGeminiRequest with system message and tools", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: "You are a helpful assistant."},
				},
			},
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "What's the weather like?"},
				},
			},
		}

		tools := []tools.BaseTool{
			&mockTool{
				name:        "get_weather",
				description: "Get current weather",
				parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type": "string",
						},
					},
				},
				required: []string{"location"},
			},
		}

		request, err := client.buildGeminiRequest(messages, tools)
		require.NoError(t, err)
		assert.NotNil(t, request)
		
		// Check system instruction
		assert.NotNil(t, request.SystemInstruction)
		assert.Equal(t, "You are a helpful assistant.", request.SystemInstruction.Parts[0].Text)
		
		// Check contents
		assert.Len(t, request.Contents, 1)
		assert.Equal(t, GeminiRoleUser, request.Contents[0].Role)
		assert.Equal(t, "What's the weather like?", request.Contents[0].Parts[0].Text)
		
		// Check tools
		assert.Len(t, request.Tools, 1)
		assert.Len(t, request.Tools[0].FunctionDeclarations, 1)
		assert.Equal(t, "get_weather", request.Tools[0].FunctionDeclarations[0].Name)
		
		// Check tool config
		assert.NotNil(t, request.ToolConfig)
		assert.NotNil(t, request.ToolConfig.FunctionCallingConfig)
		assert.Equal(t, GeminiFunctionCallingModeAuto, request.ToolConfig.FunctionCallingConfig.Mode)
	})

	t.Run("buildGenerationConfig with extra body parameters", func(t *testing.T) {
		clientWithExtraBody := &customGeminiClient{
			providerOptions: providerClientOptions{
				maxTokens: 500,
				extraBody: map[string]interface{}{
					"temperature": 0.7,
					"topP":        0.9,
					"topK":        40,
				},
			},
		}

		config := clientWithExtraBody.buildGenerationConfig()
		require.NotNil(t, config)
		assert.Equal(t, 500, *config.MaxOutputTokens)
		assert.Equal(t, 0.7, *config.Temperature)
		assert.Equal(t, 0.9, *config.TopP)
		assert.Equal(t, 40, *config.TopK)
	})

	t.Run("extractSafetySettings from extra body", func(t *testing.T) {
		clientWithSafety := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]interface{}{
					"safetySettings": []interface{}{
						map[string]interface{}{
							"category":  "HARM_CATEGORY_HARASSMENT",
							"threshold": "BLOCK_MEDIUM_AND_ABOVE",
						},
						map[string]interface{}{
							"category":  "HARM_CATEGORY_HATE_SPEECH",
							"threshold": "BLOCK_LOW_AND_ABOVE",
						},
					},
				},
			},
		}

		settings := clientWithSafety.extractSafetySettings()
		assert.Len(t, settings, 2)
		assert.Equal(t, "HARM_CATEGORY_HARASSMENT", settings[0].Category)
		assert.Equal(t, "BLOCK_MEDIUM_AND_ABOVE", settings[0].Threshold)
		assert.Equal(t, "HARM_CATEGORY_HATE_SPEECH", settings[1].Category)
		assert.Equal(t, "BLOCK_LOW_AND_ABOVE", settings[1].Threshold)
	})

	t.Run("convertFinishReason", func(t *testing.T) {
		assert.Equal(t, message.FinishReasonEndTurn, client.convertFinishReason(GeminiFinishReasonStop))
		assert.Equal(t, message.FinishReasonMaxTokens, client.convertFinishReason(GeminiFinishReasonMaxTokens))
		assert.Equal(t, message.FinishReasonPermissionDenied, client.convertFinishReason(GeminiFinishReasonSafety))
		assert.Equal(t, message.FinishReasonPermissionDenied, client.convertFinishReason(GeminiFinishReasonRecitation))
		assert.Equal(t, message.FinishReasonUnknown, client.convertFinishReason("UNKNOWN_REASON"))
	})

	t.Run("getModelID", func(t *testing.T) {
		modelID := client.getModelID()
		assert.Equal(t, "gemini-1.5-pro", modelID)
		
		// Test with empty model ID
		clientWithEmptyModel := &customGeminiClient{
			providerOptions: providerClientOptions{
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: ""}
				},
			},
		}
		modelID = clientWithEmptyModel.getModelID()
		assert.Equal(t, "gemini-1.5-pro", modelID) // Should use default
	})
}

func TestStreamingSupportDetection(t *testing.T) {
	t.Run("checkStreamingSupport with successful detection", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL:            "https://generativelanguage.googleapis.com",
			apiKey:             "test-key",
			streamingSupported: false,
			streamingChecked:   false,
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client that returns success for streaming detection
		client.httpClient = &http.Client{
			Transport: &mockRoundTripper{
				response: &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("")),
				},
			},
		}

		ctx := context.Background()
		supported := client.checkStreamingSupport(ctx)
		
		assert.True(t, supported)
		assert.True(t, client.streamingSupported)
		assert.True(t, client.streamingChecked)
	})

	t.Run("checkStreamingSupport with 404 not found", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL:            "https://generativelanguage.googleapis.com",
			apiKey:             "test-key",
			streamingSupported: false,
			streamingChecked:   false,
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client that returns 404 for streaming detection
		client.httpClient = &http.Client{
			Transport: &mockRoundTripper{
				response: &http.Response{
					StatusCode: 404,
					Body:       io.NopCloser(strings.NewReader("")),
				},
			},
		}

		ctx := context.Background()
		supported := client.checkStreamingSupport(ctx)
		
		assert.False(t, supported)
		assert.False(t, client.streamingSupported)
		assert.True(t, client.streamingChecked)
	})

	t.Run("checkStreamingSupport with 501 not implemented", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL:            "https://generativelanguage.googleapis.com",
			apiKey:             "test-key",
			streamingSupported: false,
			streamingChecked:   false,
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client that returns 501 for streaming detection
		client.httpClient = &http.Client{
			Transport: &mockRoundTripper{
				response: &http.Response{
					StatusCode: 501,
					Body:       io.NopCloser(strings.NewReader("")),
				},
			},
		}

		ctx := context.Background()
		supported := client.checkStreamingSupport(ctx)
		
		assert.False(t, supported)
		assert.False(t, client.streamingSupported)
		assert.True(t, client.streamingChecked)
	})

	t.Run("checkStreamingSupport with network error", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL:            "https://generativelanguage.googleapis.com",
			apiKey:             "test-key",
			streamingSupported: false,
			streamingChecked:   false,
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client that returns network error
		client.httpClient = &http.Client{
			Transport: &mockRoundTripper{
				err: fmt.Errorf("network error"),
			},
		}

		ctx := context.Background()
		supported := client.checkStreamingSupport(ctx)
		
		assert.False(t, supported)
		assert.False(t, client.streamingSupported)
		assert.True(t, client.streamingChecked)
	})

	t.Run("checkStreamingSupport caches result", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL:            "https://generativelanguage.googleapis.com",
			apiKey:             "test-key",
			streamingSupported: true,  // Already cached as supported
			streamingChecked:   true,  // Already checked
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client that would return error, but shouldn't be called
		client.httpClient = &http.Client{
			Transport: &mockRoundTripper{
				err: fmt.Errorf("should not be called"),
			},
		}

		ctx := context.Background()
		supported := client.checkStreamingSupport(ctx)
		
		// Should return cached result without making HTTP request
		assert.True(t, supported)
		assert.True(t, client.streamingSupported)
		assert.True(t, client.streamingChecked)
	})

	t.Run("isStreamingForced returns true when configured", func(t *testing.T) {
		client := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]any{
					"force_non_streaming": true,
				},
			},
		}

		assert.True(t, client.isStreamingForced())
	})

	t.Run("isStreamingForced returns false when not configured", func(t *testing.T) {
		client := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]any{},
			},
		}

		assert.False(t, client.isStreamingForced())
	})

	t.Run("getSimulationChunkSize returns configured value", func(t *testing.T) {
		client := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]any{
					"chunk_size": 50,
				},
			},
		}

		assert.Equal(t, 50, client.getSimulationChunkSize())
	})

	t.Run("getSimulationChunkSize returns default when not configured", func(t *testing.T) {
		client := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]any{},
			},
		}

		assert.Equal(t, 20, client.getSimulationChunkSize())
	})

	t.Run("getSimulationDelay returns configured value", func(t *testing.T) {
		client := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]any{
					"simulation_delay": 100,
				},
			},
		}

		assert.Equal(t, 100*time.Millisecond, client.getSimulationDelay())
	})

	t.Run("getSimulationDelay returns default when not configured", func(t *testing.T) {
		client := &customGeminiClient{
			providerOptions: providerClientOptions{
				extraBody: map[string]any{},
			},
		}

		assert.Equal(t, 20*time.Millisecond, client.getSimulationDelay())
	})
}

func TestStreamMethod(t *testing.T) {
	t.Run("stream method with forced non-streaming", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL: "https://generativelanguage.googleapis.com",
			apiKey:  "test-key",
			providerOptions: providerClientOptions{
				extraBody: map[string]any{
					"force_non_streaming": true,
				},
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client for non-streaming send method
		client.httpClient = &http.Client{
			Transport: &mockRoundTripper{
				response: &http.Response{
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`{
						"candidates": [{
							"content": {
								"parts": [{"text": "Hello world"}],
								"role": "model"
							},
							"finishReason": "STOP"
						}]
					}`)),
				},
			},
		}

		ctx := context.Background()
		messages := []message.Message{
			{
				Role:  message.User,
				Parts: []message.ContentPart{message.TextContent{Text: "Hello"}},
			},
		}

		eventChan := client.stream(ctx, messages, nil)
		
		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Should have simulated streaming events
		assert.Greater(t, len(events), 0)
		
		// Check for expected event types
		eventTypes := make(map[EventType]bool)
		for _, event := range events {
			eventTypes[event.Type] = true
		}
		
		assert.True(t, eventTypes[EventContentStart])
		assert.True(t, eventTypes[EventContentDelta])
		assert.True(t, eventTypes[EventContentStop])
		assert.True(t, eventTypes[EventComplete])
	})

	t.Run("stream method with streaming detection failure", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL:            "https://generativelanguage.googleapis.com",
			apiKey:             "test-key",
			streamingSupported: false,
			streamingChecked:   false,
			providerOptions: providerClientOptions{
				extraBody: map[string]any{},
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Mock HTTP client that returns 404 for streaming detection and success for regular send
		callCount := 0
		client.httpClient = &http.Client{
			Transport: mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				callCount++
				if strings.Contains(req.URL.Path, "streamGenerateContent") {
					// First call is streaming detection, return 404
					return &http.Response{
						StatusCode: 404,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}
				// Second call is regular send, return success
				return &http.Response{
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`{
						"candidates": [{
							"content": {
								"parts": [{"text": "Hello from simulation"}],
								"role": "model"
							},
							"finishReason": "STOP"
						}]
					}`)),
				}, nil
			}),
		}

		ctx := context.Background()
		messages := []message.Message{
			{
				Role:  message.User,
				Parts: []message.ContentPart{message.TextContent{Text: "Hello"}},
			},
		}

		eventChan := client.stream(ctx, messages, nil)
		
		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Should have made both streaming detection call and regular send call
		assert.Equal(t, 2, callCount)
		
		// Should have simulated streaming events
		assert.Greater(t, len(events), 0)
		
		// Verify streaming was marked as not supported
		assert.False(t, client.streamingSupported)
		assert.True(t, client.streamingChecked)
	})
}

// Mock HTTP transport for testing
type mockRoundTripper struct {
	response *http.Response
	err      error
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// Mock HTTP transport with function for more complex scenarios
type mockRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f mockRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCustomGeminiStreamingResponse(t *testing.T) {
	t.Run("processStreamResponse with text content", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL: "https://generativelanguage.googleapis.com",
			apiKey:  "test-key",
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Create mock SSE response
		sseData := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}]}

data: {"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"}}]}

data: {"candidates":[{"content":{"parts":[{"text":"!"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}

`

		resp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseData)),
		}

		eventChan := make(chan ProviderEvent, 10)
		go func() {
			defer close(eventChan)
			success := client.processStreamResponse(resp, eventChan)
			assert.True(t, success)
		}()

		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Verify event sequence
		require.GreaterOrEqual(t, len(events), 5) // At least: start, 3 deltas, stop, complete

		// Check event types and content
		assert.Equal(t, EventContentStart, events[0].Type)
		
		// Find content delta events
		var contentDeltas []string
		for _, event := range events {
			if event.Type == EventContentDelta {
				contentDeltas = append(contentDeltas, event.Content)
			}
		}
		
		// Should have received text chunks
		assert.Contains(t, contentDeltas, "Hello")
		assert.Contains(t, contentDeltas, " world")
		assert.Contains(t, contentDeltas, "!")

		// Check final events
		assert.Equal(t, EventContentStop, events[len(events)-2].Type)
		assert.Equal(t, EventComplete, events[len(events)-1].Type)
		
		// Verify final response
		finalEvent := events[len(events)-1]
		require.NotNil(t, finalEvent.Response)
		assert.Equal(t, "Hello world!", finalEvent.Response.Content)
		assert.Equal(t, int64(5), finalEvent.Response.Usage.InputTokens)
		assert.Equal(t, int64(3), finalEvent.Response.Usage.OutputTokens)
		assert.Equal(t, message.FinishReasonEndTurn, finalEvent.Response.FinishReason)
	})

	t.Run("processStreamResponse with function calls", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL: "https://generativelanguage.googleapis.com",
			apiKey:  "test-key",
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Create mock SSE response with function call
		sseData := `data: {"candidates":[{"content":{"parts":[{"text":"I'll help you get the weather."}],"role":"model"}}]}

data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"location":"New York"}}}],"role":"model"}}]}

data: {"candidates":[{"content":{"parts":[{"text":"The weather is sunny."}],"role":"model"}],"finishReason":"STOP"}]}

`

		resp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseData)),
		}

		eventChan := make(chan ProviderEvent, 20)
		go func() {
			defer close(eventChan)
			success := client.processStreamResponse(resp, eventChan)
			assert.True(t, success)
		}()

		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Verify we have tool use events
		var toolUseStartEvents []ProviderEvent
		var toolUseDeltaEvents []ProviderEvent
		var toolUseStopEvents []ProviderEvent
		
		for _, event := range events {
			switch event.Type {
			case EventToolUseStart:
				toolUseStartEvents = append(toolUseStartEvents, event)
			case EventToolUseDelta:
				toolUseDeltaEvents = append(toolUseDeltaEvents, event)
			case EventToolUseStop:
				toolUseStopEvents = append(toolUseStopEvents, event)
			}
		}

		// Should have tool use events
		assert.Len(t, toolUseStartEvents, 1)
		assert.Len(t, toolUseDeltaEvents, 1)
		assert.Len(t, toolUseStopEvents, 1)

		// Verify tool call details
		toolCall := toolUseStartEvents[0].ToolCall
		require.NotNil(t, toolCall)
		assert.Equal(t, "get_weather", toolCall.Name)
		assert.Contains(t, toolCall.Input, "New York")

		// Verify final response includes tool call
		finalEvent := events[len(events)-1]
		require.NotNil(t, finalEvent.Response)
		assert.Len(t, finalEvent.Response.ToolCalls, 1)
		assert.Equal(t, "get_weather", finalEvent.Response.ToolCalls[0].Name)
	})

	t.Run("processStreamResponse with malformed JSON", func(t *testing.T) {
		client := &customGeminiClient{
			baseURL: "https://generativelanguage.googleapis.com",
			apiKey:  "test-key",
			providerOptions: providerClientOptions{
				modelType: config.SelectedModelTypeLarge,
				model: func(config.SelectedModelType) catwalk.Model {
					return catwalk.Model{ID: "gemini-1.5-pro"}
				},
			},
		}

		// Create mock SSE response with malformed JSON
		sseData := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}]}

data: {invalid json}

data: {"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"},"finishReason":"STOP"}]}

`

		resp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseData)),
		}

		eventChan := make(chan ProviderEvent, 10)
		go func() {
			defer close(eventChan)
			success := client.processStreamResponse(resp, eventChan)
			assert.True(t, success) // Should still succeed, just skip malformed chunks
		}()

		// Collect events
		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
		}

		// Should still process valid chunks
		var contentDeltas []string
		for _, event := range events {
			if event.Type == EventContentDelta {
				contentDeltas = append(contentDeltas, event.Content)
			}
		}
		
		assert.Contains(t, contentDeltas, "Hello")
		assert.Contains(t, contentDeltas, " world")

		// Verify final response
		finalEvent := events[len(events)-1]
		require.NotNil(t, finalEvent.Response)
		assert.Equal(t, "Hello world", finalEvent.Response.Content)
	})
}