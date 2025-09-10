package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/message"
)

type customGeminiClient struct {
	providerOptions providerClientOptions
	httpClient      *http.Client
	baseURL         string
}

type CustomGeminiClient ProviderClient

func newCustomGeminiClient(opts providerClientOptions) CustomGeminiClient {
	httpClient := createCustomGeminiHTTPClient(opts)

	return &customGeminiClient{
		providerOptions: opts,
		httpClient:      httpClient,
		baseURL:         opts.baseURL,
	}
}

func createCustomGeminiHTTPClient(opts providerClientOptions) *http.Client {
	// Use debug-aware HTTP client if debug mode is enabled
	if config.Get().Options.Debug {
		return log.NewHTTPClient()
	}

	// Standard HTTP client configuration optimized for Gemini API
	return &http.Client{
		Timeout: 120 * time.Second, // Longer timeout for streaming responses
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// send implements the ProviderClient interface for non-streaming requests
func (c *customGeminiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	// Build request URL using URL resolver
	methodPath := c.buildGeminiMethodPath(c.getModelName(), "generateContent")
	requestURL, err := c.buildRequestURL(methodPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build request URL: %w", err)
	}

	// TODO: Implement actual Gemini API call in later tasks
	slog.Info("Custom Gemini provider send called",
		"messages_count", len(messages),
		"tools_count", len(tools),
		"request_url", requestURL,
		"model", c.getModelName())

	// Return a minimal mock response for now
	return &ProviderResponse{
		Content:      "Custom Gemini provider response (stub)",
		ToolCalls:    []message.ToolCall{},
		Usage:        TokenUsage{},
		FinishReason: message.FinishReasonEndTurn,
	}, nil
}

// stream implements the ProviderClient interface for streaming requests
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)

		// Build request URL using URL resolver
		methodPath := c.buildGeminiMethodPath(c.getModelName(), "streamGenerateContent")
		requestURL, err := c.buildRequestURL(methodPath)
		if err != nil {
			slog.Error("Failed to build streaming request URL", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build request URL: %w", err)}
			return
		}

		// TODO: Implement actual streaming in later tasks
		slog.Info("Custom Gemini provider stream called",
			"messages_count", len(messages),
			"tools_count", len(tools),
			"request_url", requestURL,
			"model", c.getModelName())

		// Send minimal mock events
		eventChan <- ProviderEvent{Type: EventContentStart}
		eventChan <- ProviderEvent{
			Type:    EventContentDelta,
			Content: "Custom Gemini streaming response (stub)",
		}
		eventChan <- ProviderEvent{Type: EventContentStop}
		eventChan <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content:      "Custom Gemini streaming response (stub)",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{},
				FinishReason: message.FinishReasonEndTurn,
			},
		}
	}()

	return eventChan
}

// Model implements the ProviderClient interface
func (c *customGeminiClient) Model() catwalk.Model {
	return c.providerOptions.model(c.providerOptions.modelType)
}

// buildRequestURL constructs the full request URL for a given Gemini API method
func (c *customGeminiClient) buildRequestURL(methodPath string) (string, error) {
	return ResolveGeminiURL(c.baseURL, methodPath)
}

// buildGeminiMethodPath constructs the method path for Gemini API endpoints
func (c *customGeminiClient) buildGeminiMethodPath(model, operation string) string {
	return fmt.Sprintf("v1beta/models/%s:%s", model, operation)
}

// getModelName extracts the model name from the provider options
func (c *customGeminiClient) getModelName() string {
	return c.Model().ID
}

// convertMessages converts Crush messages to Gemini API format
func (c *customGeminiClient) convertMessages(messages []message.Message, tools []tools.BaseTool) (*geminiRequest, error) {
	request := &geminiRequest{
		Contents: make([]geminiContent, 0),
	}

	// Handle system message
	systemMessage := c.providerOptions.systemMessage
	if c.providerOptions.systemPromptPrefix != "" {
		systemMessage = c.providerOptions.systemPromptPrefix + "\n" + systemMessage
	}

	if systemMessage != "" {
		request.SystemInstruction = &geminiContent{
			Role:  "user", // Gemini uses "user" role for system instructions
			Parts: []geminiPart{{Text: systemMessage}},
		}
	}

	// Convert conversation messages
	for _, msg := range messages {
		content := c.convertMessage(msg)
		if content != nil {
			request.Contents = append(request.Contents, *content)
		}
	}

	// Convert tools if provided
	if len(tools) > 0 {
		geminiTools := c.convertTools(tools)
		request.Tools = geminiTools
	}

	// Set generation config if needed
	if c.providerOptions.maxTokens > 0 {
		request.GenerationConfig = &generationConfig{
			MaxOutputTokens: int(c.providerOptions.maxTokens),
		}
	}

	return request, nil
}

// convertMessage converts a single Crush message to Gemini content format
func (c *customGeminiClient) convertMessage(msg message.Message) *geminiContent {
	switch msg.Role {
	case message.User:
		return c.convertUserMessage(msg)
	case message.Assistant:
		return c.convertAssistantMessage(msg)
	case message.System:
		return c.convertSystemMessage(msg)
	case message.Tool:
		return c.convertToolMessage(msg)
	default:
		return nil
	}
}

// convertSystemMessage converts a system message to Gemini format
// In Gemini, system messages are typically handled as user messages in the conversation
func (c *customGeminiClient) convertSystemMessage(msg message.Message) *geminiContent {
	// Convert system message as user message since Gemini doesn't have a separate system role in conversation
	content := c.convertUserMessage(msg)
	if content != nil {
		content.Role = "user"
	}
	return content
}

// convertUserMessage converts a user message to Gemini format
func (c *customGeminiClient) convertUserMessage(msg message.Message) *geminiContent {
	content := &geminiContent{
		Role:  "user",
		Parts: make([]geminiPart, 0),
	}

	// Process all parts in order to maintain the original structure
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case message.TextContent:
			if p.Text != "" {
				content.Parts = append(content.Parts, geminiPart{
					Text: p.Text,
				})
			}
		case message.BinaryContent:
			content.Parts = append(content.Parts, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: p.MIMEType,
					Data:     p.String(catwalk.InferenceProviderGemini),
				},
			})
		case message.ImageURLContent:
			// For Gemini, we need to convert image URLs to inline data
			// For now, we'll add as text content with a note
			// In a full implementation, we'd fetch and convert the image
			content.Parts = append(content.Parts, geminiPart{
				Text: fmt.Sprintf("[Image URL: %s]", p.URL),
			})
		case message.ReasoningContent:
			// Add reasoning content as text
			if p.Thinking != "" {
				content.Parts = append(content.Parts, geminiPart{
					Text: fmt.Sprintf("[Thinking: %s]", p.Thinking),
				})
			}
		case message.ToolResult:
			content.Parts = append(content.Parts, geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name: p.Name,
					Response: map[string]interface{}{
						"content":  p.Content,
						"metadata": p.Metadata,
						"is_error": p.IsError,
					},
				},
			})
		case message.Finish:
			// Finish parts are typically not included in the request content
			// They're used for response processing
			continue
		}
	}

	return content
}

// convertAssistantMessage converts an assistant message to Gemini format
func (c *customGeminiClient) convertAssistantMessage(msg message.Message) *geminiContent {
	content := &geminiContent{
		Role:  "model", // Gemini uses "model" for assistant messages
		Parts: make([]geminiPart, 0),
	}

	// Process all parts in order to maintain the original structure
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case message.TextContent:
			if p.Text != "" {
				content.Parts = append(content.Parts, geminiPart{
					Text: p.Text,
				})
			}
		case message.ReasoningContent:
			// Add reasoning content as text
			if p.Thinking != "" {
				content.Parts = append(content.Parts, geminiPart{
					Text: fmt.Sprintf("[Thinking: %s]", p.Thinking),
				})
			}
		case message.ToolCall:
			// Parse tool call input as JSON for args
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(p.Input), &args); err != nil {
				// If parsing fails, use the input as a single string argument
				args = map[string]interface{}{
					"input": p.Input,
				}
			}

			content.Parts = append(content.Parts, geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: p.Name,
					Args: args,
				},
			})
		case message.Finish:
			// Finish parts are typically not included in the request content
			// They're used for response processing
			continue
		}
	}

	return content
}

// convertToolMessage converts a tool message to Gemini format
func (c *customGeminiClient) convertToolMessage(msg message.Message) *geminiContent {
	// Tool messages are typically handled as part of user messages in Gemini
	// This is a fallback that treats tool messages as user messages
	content := c.convertUserMessage(msg)
	if content != nil {
		content.Role = "user"
	}
	return content
}

// convertTools converts Crush tools to Gemini tool format
func (c *customGeminiClient) convertTools(tools []tools.BaseTool) []geminiTool {
	if len(tools) == 0 {
		return nil
	}

	functionDeclarations := make([]geminiFunctionDeclaration, 0, len(tools))

	for _, tool := range tools {
		info := tool.Info()
		functionDeclarations = append(functionDeclarations, geminiFunctionDeclaration{
			Name:        info.Name,
			Description: info.Description,
			Parameters:  info.Parameters,
		})
	}

	return []geminiTool{
		{
			FunctionDeclarations: functionDeclarations,
		},
	}
}

// buildHTTPRequest creates an HTTP request for the Gemini API
func (c *customGeminiClient) buildHTTPRequest(ctx context.Context, method, url string, request *geminiRequest) (*http.Request, error) {
	// Serialize request body
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set required headers
	req.Header.Set("Content-Type", "application/json")

	// Set authorization header
	if c.providerOptions.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.providerOptions.apiKey)
	}

	// Set user agent
	req.Header.Set("User-Agent", "Crush/1.0")

	return req, nil
}
