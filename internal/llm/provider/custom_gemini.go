package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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

	// Convert messages to Gemini format
	request, err := c.convertMessages(messages, tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Build HTTP request
	httpReq, err := c.buildHTTPRequest(ctx, "POST", requestURL, request)
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP request: %w", err)
	}

	// Execute HTTP request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return nil, c.handleHTTPError(resp)
	}

	// Read response body
	var responseBody bytes.Buffer
	if _, err := responseBody.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response
	response, err := c.parseResponse(responseBody.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	slog.Info("Custom Gemini provider send completed",
		"messages_count", len(messages),
		"tools_count", len(tools),
		"request_url", requestURL,
		"model", c.getModelName(),
		"response_length", len(response.Content),
		"tool_calls_count", len(response.ToolCalls))

	return response, nil
}

// stream implements the ProviderClient interface for streaming requests
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	// Detect URL mode to choose streaming strategy
	mode, _, err := DetectURLMode(c.baseURL)
	if err != nil {
		eventChan := make(chan ProviderEvent)
		go func() {
			defer close(eventChan)
			slog.Error("Failed to detect URL mode for streaming", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to detect URL mode: %w", err)}
		}()
		return eventChan
	}

	slog.Info("Custom Gemini provider stream called",
		"messages_count", len(messages),
		"tools_count", len(tools),
		"model", c.getModelName(),
		"url_mode", mode)

	switch mode {
	case ModeStandard:
		// Use Server-Sent Events streaming for standard mode
		return c.streamStandard(ctx, messages, tools)
	case ModeFull:
		// Use streaming simulation for complete URL mode
		return c.streamSimulated(ctx, messages, tools)
	default:
		eventChan := make(chan ProviderEvent)
		go func() {
			defer close(eventChan)
			slog.Error("Unknown URL mode for streaming", "mode", mode)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("unknown URL mode: %d", mode)}
		}()
		return eventChan
	}
}

// Model implements the ProviderClient interface
func (c *customGeminiClient) Model() catwalk.Model {
	return c.providerOptions.model(c.providerOptions.modelType)
}

// buildRequestURL constructs the full request URL for a given Gemini API method
func (c *customGeminiClient) buildRequestURL(methodPath string) (string, error) {
	baseURL, err := ResolveGeminiURL(c.baseURL, methodPath)
	if err != nil {
		return "", err
	}
	
	// Add API key as query parameter for Gemini API
	if c.providerOptions.apiKey != "" {
		separator := "?"
		if strings.Contains(baseURL, "?") {
			separator = "&"
		}
		baseURL += separator + "key=" + c.providerOptions.apiKey
	}
	
	return baseURL, nil
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

	// Set user agent
	req.Header.Set("User-Agent", "Crush/1.0")
	
	// Note: API key is added as query parameter in buildRequestURL, not as Authorization header

	return req, nil
}

// parseResponse parses a Gemini API response and converts it to ProviderResponse format
func (c *customGeminiClient) parseResponse(body []byte) (*ProviderResponse, error) {
	var response geminiResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(response.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	// Validate response structure
	if err := response.Validate(); err != nil {
		return nil, fmt.Errorf("invalid response structure: %w", err)
	}

	// Use the first candidate
	candidate := response.Candidates[0]
	
	// Extract content and tool calls from the candidate
	content, toolCalls := c.extractContentAndToolCalls(candidate.Content)

	// Convert usage metadata
	usage := c.convertUsage(response.UsageMetadata)

	// Convert finish reason
	finishReason := c.convertFinishReason(candidate.FinishReason)

	return &ProviderResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		Usage:        usage,
		FinishReason: finishReason,
	}, nil
}

// extractContentAndToolCalls extracts text content and tool calls from geminiContent
func (c *customGeminiClient) extractContentAndToolCalls(content geminiContent) (string, []message.ToolCall) {
	var textContent strings.Builder
	var toolCalls []message.ToolCall

	for _, part := range content.Parts {
		if part.Text != "" {
			textContent.WriteString(part.Text)
		}
		
		if part.FunctionCall != nil {
			toolCall := c.convertToToolCall(part.FunctionCall)
			toolCalls = append(toolCalls, toolCall)
		}
	}

	return textContent.String(), toolCalls
}

// convertToToolCall converts a Gemini function call to Crush ToolCall format
func (c *customGeminiClient) convertToToolCall(functionCall *geminiFunctionCall) message.ToolCall {
	// Convert args to JSON string
	inputBytes, err := json.Marshal(functionCall.Args)
	if err != nil {
		// Fallback to empty object if marshaling fails
		inputBytes = []byte("{}")
	}

	return message.ToolCall{
		ID:       generateToolCallID(), // Generate a unique ID
		Name:     functionCall.Name,
		Input:    string(inputBytes),
		Type:     "function",
		Finished: false, // Will be set to true when the tool result is received
	}
}

// convertUsage converts Gemini usage metadata to TokenUsage format
func (c *customGeminiClient) convertUsage(usage *geminiUsage) TokenUsage {
	if usage == nil {
		return TokenUsage{}
	}

	return TokenUsage{
		InputTokens:         int64(usage.PromptTokenCount),
		OutputTokens:        int64(usage.CandidatesTokenCount),
		CacheCreationTokens: int64(usage.CachedContentTokenCount),
		CacheReadTokens:     0, // Gemini doesn't provide separate cache read tokens
	}
}

// convertFinishReason converts Gemini finish reason to Crush FinishReason
func (c *customGeminiClient) convertFinishReason(reason string) message.FinishReason {
	switch reason {
	case "STOP":
		return message.FinishReasonEndTurn
	case "MAX_TOKENS":
		return message.FinishReasonMaxTokens
	case "SAFETY":
		return message.FinishReasonPermissionDenied
	case "RECITATION":
		return message.FinishReasonPermissionDenied
	case "OTHER":
		return message.FinishReasonError
	case "":
		// Empty finish reason in streaming responses
		return message.FinishReasonEndTurn
	default:
		slog.Warn("Unknown Gemini finish reason", "reason", reason)
		return message.FinishReasonUnknown
	}
}

// generateToolCallID generates a unique ID for tool calls
func generateToolCallID() string {
	// Simple implementation using timestamp and random suffix
	// In a production system, you might want to use UUID
	return fmt.Sprintf("call_%d", time.Now().UnixNano())
}

// handleHTTPError handles HTTP error responses from the Gemini API
func (c *customGeminiClient) handleHTTPError(resp *http.Response) error {
	var responseBody bytes.Buffer
	if _, err := responseBody.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("HTTP %d: failed to read error response body: %w", resp.StatusCode, err)
	}

	// Try to parse as Gemini error response
	var errorResp geminiErrorResponse
	if err := json.Unmarshal(responseBody.Bytes(), &errorResp); err == nil {
		return fmt.Errorf("Gemini API error (HTTP %d): %s - %s", 
			resp.StatusCode, errorResp.Error.Status, errorResp.Error.Message)
	}

	// Fallback to generic HTTP error
	return fmt.Errorf("HTTP %d: %s - %s", resp.StatusCode, resp.Status, responseBody.String())
}
