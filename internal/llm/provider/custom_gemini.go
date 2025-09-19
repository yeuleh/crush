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
	start := time.Now()
	attempts := 0

	c.logOperationContext("send_starting", 
		"messages_count", len(messages),
		"tools_count", len(tools))

	for {
		attempts++

		// Build request URL using URL resolver (rebuild in case of retries with updated config)
		methodPath := c.buildGeminiMethodPath(c.getModelName(), "generateContent")
		requestURL, err := c.buildRequestURL(methodPath)
		if err != nil {
			slog.Error("Failed to build request URL", 
				"error", err,
				"method_path", methodPath,
				"base_url", c.baseURL,
				"model", c.getModelName())
			return nil, fmt.Errorf("failed to build request URL: %w", err)
		}

		// Convert messages to Gemini format
		request, err := c.convertMessages(messages, tools)
		if err != nil {
			slog.Error("Failed to convert messages to Gemini format", 
				"error", err,
				"messages_count", len(messages),
				"tools_count", len(tools))
			return nil, fmt.Errorf("failed to convert messages: %w", err)
		}

		slog.Debug("Messages converted to Gemini format", 
			"contents_count", len(request.Contents),
			"has_system_instruction", request.SystemInstruction != nil,
			"tools_count", len(request.Tools))

		// Build HTTP request
		httpReq, bodySize, err := c.buildHTTPRequestWithSize(ctx, "POST", requestURL, request)
		if err != nil {
			slog.Error("Failed to build HTTP request", 
				"error", err,
				"method", "POST",
				"url", requestURL)
			return nil, fmt.Errorf("failed to build HTTP request: %w", err)
		}
		
		// Log detailed request information in debug mode
		c.logRequestDetails(httpReq, bodySize)

		// Execute HTTP request
		requestStart := time.Now()
		slog.Debug("Executing HTTP request", 
			"url", requestURL,
			"attempt", attempts)
		
		resp, err := c.httpClient.Do(httpReq)
		requestDuration := time.Since(requestStart)
		
		if err != nil {
			slog.Error("HTTP request execution failed", 
				"error", err,
				"attempt", attempts,
				"duration_ms", requestDuration.Milliseconds(),
				"url", requestURL)
			
			// Check if this is a retryable error
			retry, after, retryErr := c.shouldRetry(attempts, err)
			if retryErr != nil {
				slog.Error("Max retries exceeded for HTTP request", 
					"error", retryErr,
					"attempts", attempts,
					"max_retries", maxRetries)
				return nil, fmt.Errorf("HTTP request failed: %w", retryErr)
			}
			if retry {
				slog.Warn("Retrying send request due to error", 
					"attempt", attempts, 
					"max_retries", maxRetries, 
					"error", err.Error(),
					"retry_after_ms", after.Milliseconds(),
					"total_duration_ms", time.Since(start).Milliseconds())
				select {
				case <-ctx.Done():
					slog.Warn("Request cancelled during retry wait", "context_error", ctx.Err())
					return nil, ctx.Err()
				case <-time.After(after):
					continue
				}
			}
			return nil, fmt.Errorf("HTTP request failed: %w", err)
		}
		
		slog.Debug("HTTP request completed", 
			"status_code", resp.StatusCode,
			"status", resp.Status,
			"content_length", resp.ContentLength,
			"duration_ms", requestDuration.Milliseconds(),
			"attempt", attempts)
		
		// Log detailed response information in debug mode
		c.logResponseDetails(resp, resp.ContentLength)
		defer resp.Body.Close()

		// Check HTTP status code
		if resp.StatusCode != http.StatusOK {
			slog.Warn("HTTP request returned non-OK status", 
				"status_code", resp.StatusCode,
				"status", resp.Status,
				"url", requestURL,
				"attempt", attempts)
			
			httpErr := c.handleHTTPError(resp)
			
			// Check if this is a retryable HTTP error
			retry, after, retryErr := c.shouldRetry(attempts, httpErr)
			if retryErr != nil {
				slog.Error("Max retries exceeded for HTTP error", 
					"error", retryErr,
					"attempts", attempts,
					"max_retries", maxRetries,
					"status_code", resp.StatusCode)
				return nil, retryErr
			}
			if retry {
				slog.Warn("Retrying send request due to HTTP error",
					"attempt", attempts,
					"max_retries", maxRetries,
					"status_code", resp.StatusCode,
					"retry_after_ms", after.Milliseconds(),
					"total_duration_ms", time.Since(start).Milliseconds())
				select {
				case <-ctx.Done():
					slog.Warn("Request cancelled during retry wait", "context_error", ctx.Err())
					return nil, ctx.Err()
				case <-time.After(after):
					continue
				}
			}
			return nil, httpErr
		}

		// Read response body
		var responseBody bytes.Buffer
		readStart := time.Now()
		respBodySize, err := responseBody.ReadFrom(resp.Body)
		if err != nil {
			slog.Error("Failed to read response body", 
				"error", err,
				"attempt", attempts)
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		
		slog.Debug("Response body read", 
			"body_size_bytes", respBodySize,
			"read_duration_ms", time.Since(readStart).Milliseconds())

		// Parse response
		parseStart := time.Now()
		response, err := c.parseResponse(responseBody.Bytes())
		if err != nil {
			slog.Error("Failed to parse response", 
				"error", err,
				"body_size_bytes", respBodySize,
				"attempt", attempts)
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		
		slog.Debug("Response parsed successfully", 
			"parse_duration_ms", time.Since(parseStart).Milliseconds(),
			"content_length", len(response.Content),
			"tool_calls_count", len(response.ToolCalls),
			"prompt_tokens", response.Usage.InputTokens,
			"completion_tokens", response.Usage.OutputTokens)

		totalDuration := time.Since(start)
		
		slog.Info("Custom Gemini provider send completed",
			"messages_count", len(messages),
			"tools_count", len(tools),
			"model", c.getModelName(),
			"response_length", len(response.Content),
			"tool_calls_count", len(response.ToolCalls),
			"attempts", attempts,
			"total_duration_ms", totalDuration.Milliseconds(),
			"prompt_tokens", response.Usage.InputTokens,
			"completion_tokens", response.Usage.OutputTokens,
			"total_tokens", response.Usage.InputTokens + response.Usage.OutputTokens,
			"success", true)

		return response, nil
	}
}

// stream implements the ProviderClient interface for streaming requests
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	start := time.Now()
	
	// Detect URL mode to choose streaming strategy
	mode, _, err := DetectURLMode(c.baseURL)
	if err != nil {
		eventChan := make(chan ProviderEvent)
		go func() {
			defer close(eventChan)
			slog.Error("Failed to detect URL mode for streaming", 
				"error", err,
				"base_url", c.baseURL,
				"model", c.getModelName())
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to detect URL mode: %w", err)}
		}()
	return eventChan
	}

	slog.Info("Custom Gemini provider stream starting",
		"messages_count", len(messages),
		"tools_count", len(tools),
		"model", c.getModelName(),
		"url_mode", mode,
		"base_url", c.baseURL)

	switch mode {
	case ModeStandard:
		// Use Server-Sent Events streaming for standard mode
		slog.Debug("Using standard SSE streaming mode", 
			"model", c.getModelName(),
			"mode", mode)
		return c.streamStandardWithLogging(ctx, messages, tools, start)
	case ModeFull:
		// Use streaming simulation for complete URL mode
		slog.Debug("Using streaming simulation mode", 
			"model", c.getModelName(),
			"mode", mode)
		return c.streamSimulatedWithLogging(ctx, messages, tools, start)
	default:
		eventChan := make(chan ProviderEvent)
		go func() {
			defer close(eventChan)
			slog.Error("Unknown URL mode for streaming", 
				"mode", mode,
				"base_url", c.baseURL,
				"model", c.getModelName())
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("unknown URL mode: %d", mode)}
		}()
	return eventChan
	}
}

// Model implements the ProviderClient interface
func (c *customGeminiClient) Model() catwalk.Model {
	return c.providerOptions.model(c.providerOptions.modelType)
}

// streamStandardWithLogging wraps streamStandard with comprehensive logging
func (c *customGeminiClient) streamStandardWithLogging(ctx context.Context, messages []message.Message, tools []tools.BaseTool, startTime time.Time) <-chan ProviderEvent {
	eventChan := c.streamStandard(ctx, messages, tools)
	loggedEventChan := make(chan ProviderEvent)

	go func() {
		defer close(loggedEventChan)

		var eventCount int
		var deltaCount int
		var lastEventType EventType
		var contentLength int
		var toolCallsCount int

		for event := range eventChan {
			eventCount++
			lastEventType = event.Type

			// Log event details in debug mode
			switch event.Type {
			case EventContentStart:
				slog.Debug("SSE stream content started",
					"model", c.getModelName(),
					"event_count", eventCount)
				
			case EventContentDelta:
				deltaCount++
				contentLength += len(event.Content)
				if deltaCount%10 == 0 { // Log every 10th delta to avoid spam
					slog.Debug("SSE stream delta progress",
						"delta_count", deltaCount,
						"content_length", contentLength,
						"current_delta_length", len(event.Content))
				}
				
			case EventContentStop:
				slog.Debug("SSE stream content stopped",
					"total_deltas", deltaCount,
					"final_content_length", contentLength)
				
			case EventToolUseStart:
				toolCallsCount++
				slog.Debug("SSE stream tool call started",
					"tool_calls_count", toolCallsCount,
					"tool_name", func() string {
						if event.ToolCall != nil {
							return event.ToolCall.Name
						}
						return "unknown"
					}())
				
			case EventComplete:
				response := event.Response
				totalDuration := time.Since(startTime)
				slog.Info("SSE stream completed",
					"model", c.getModelName(),
					"total_events", eventCount,
					"delta_events", deltaCount,
					"content_length", len(response.Content),
					"tool_calls_count", len(response.ToolCalls),
					"total_duration_ms", totalDuration.Milliseconds(),
					"prompt_tokens", response.Usage.InputTokens,
					"completion_tokens", response.Usage.OutputTokens,
					"finish_reason", response.FinishReason,
					"success", true)
				
			case EventError:
				totalDuration := time.Since(startTime)
				slog.Error("SSE stream failed",
					"model", c.getModelName(),
					"total_events", eventCount,
					"last_event_type", lastEventType,
					"error", event.Error,
					"total_duration_ms", totalDuration.Milliseconds())
			}
			
			// Forward the event
			loggedEventChan <- event
		}

		// Log if stream ended unexpectedly
		if lastEventType != EventComplete && lastEventType != EventError {
			slog.Warn("SSE stream ended unexpectedly",
				"model", c.getModelName(),
				"last_event_type", lastEventType,
				"total_events", eventCount,
				"total_duration_ms", time.Since(startTime).Milliseconds())
		}
	}()

	return loggedEventChan
}

// streamSimulatedWithLogging wraps streamSimulated with comprehensive logging
func (c *customGeminiClient) streamSimulatedWithLogging(ctx context.Context, messages []message.Message, tools []tools.BaseTool, startTime time.Time) <-chan ProviderEvent {
	eventChan := c.streamSimulated(ctx, messages, tools)
	loggedEventChan := make(chan ProviderEvent)

	go func() {
		defer close(loggedEventChan)

		var eventCount int
		var deltaCount int
		var lastEventType EventType
		var simulationStart time.Time
		var simulationEnd time.Time
		var contentLength int
		var toolCallsCount int
		var chunkCount int

		for event := range eventChan {
			eventCount++
			lastEventType = event.Type

			// Log event details in debug mode
			switch event.Type {
			case EventContentStart:
				simulationStart = time.Now()
				slog.Debug("Stream simulation content started",
					"model", c.getModelName(),
					"event_count", eventCount,
					"preparation_duration_ms", simulationStart.Sub(startTime).Milliseconds())
				
			case EventContentDelta:
				deltaCount++
				chunkCount++
				contentLength += len(event.Content)
				if chunkCount%5 == 0 { // Log every 5th chunk for simulation
					slog.Debug("Stream simulation progress",
						"chunk_count", chunkCount,
						"content_length", contentLength,
						"current_chunk_length", len(event.Content),
						"simulation_duration_ms", func() int64 {
							if !simulationStart.IsZero() {
								return time.Since(simulationStart).Milliseconds()
							}
							return 0
						}())
				}
				
			case EventContentStop:
				simulationEnd = time.Now()
				slog.Debug("Stream simulation content stopped",
					"total_chunks", chunkCount,
					"final_content_length", contentLength,
					"simulation_duration_ms", func() int64 {
						if !simulationStart.IsZero() && !simulationEnd.IsZero() {
							return simulationEnd.Sub(simulationStart).Milliseconds()
						}
						return 0
					}())
				
			case EventToolUseStart:
				toolCallsCount++
				slog.Debug("Stream simulation tool call started",
					"tool_calls_count", toolCallsCount,
					"tool_name", func() string {
						if event.ToolCall != nil {
							return event.ToolCall.Name
						}
						return "unknown"
					}())
				
			case EventComplete:
				response := event.Response
				totalDuration := time.Since(startTime)
				simulationDuration := func() int64 {
					if !simulationStart.IsZero() && !simulationEnd.IsZero() {
						return simulationEnd.Sub(simulationStart).Milliseconds()
					}
					return 0
				}()
				slog.Info("Stream simulation completed",
					"model", c.getModelName(),
					"total_events", eventCount,
					"chunk_events", chunkCount,
					"content_length", len(response.Content),
					"tool_calls_count", len(response.ToolCalls),
					"total_duration_ms", totalDuration.Milliseconds(),
					"simulation_duration_ms", simulationDuration,
					"preparation_duration_ms", func() int64 {
						if !simulationStart.IsZero() {
							return simulationStart.Sub(startTime).Milliseconds()
						}
						return 0
					}(),
					"prompt_tokens", response.Usage.InputTokens,
					"completion_tokens", response.Usage.OutputTokens,
					"finish_reason", response.FinishReason,
					"success", true)
				
			case EventError:
				totalDuration := time.Since(startTime)
				slog.Error("Stream simulation failed",
					"model", c.getModelName(),
					"total_events", eventCount,
					"last_event_type", lastEventType,
					"error", event.Error,
					"total_duration_ms", totalDuration.Milliseconds())
			}
			
			// Forward the event
			loggedEventChan <- event
		}

		// Log if stream ended unexpectedly
		if lastEventType != EventComplete && lastEventType != EventError {
			slog.Warn("Stream simulation ended unexpectedly",
				"model", c.getModelName(),
				"last_event_type", lastEventType,
				"total_events", eventCount,
				"total_duration_ms", time.Since(startTime).Milliseconds())
		}
	}()

	return loggedEventChan
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

// getModelName extracts the model name from provider options
func (c *customGeminiClient) getModelName() string {
	return string(c.providerOptions.modelType)
}

// logOperationContext logs common operation context for debugging
func (c *customGeminiClient) logOperationContext(operation string, extra ...interface{}) {
	if !slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		return
	}
	
	args := []interface{}{
		"operation", operation,
		"provider", "custom-gemini",
		"model", c.getModelName(),
		"base_url", c.baseURL,
		"http_client_timeout", c.httpClient.Timeout,
	}
	
	// Append any extra key-value pairs
	args = append(args, extra...)
	
	slog.Debug("Operation context", args...)
}

// logRequestDetails logs detailed HTTP request information
func (c *customGeminiClient) logRequestDetails(req *http.Request, bodySize int) {
	if !slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		return
	}
	
	slog.Debug("HTTP request details",
		"method", req.Method,
		"url", req.URL.String(),
		"content_type", req.Header.Get("Content-Type"),
		"user_agent", req.Header.Get("User-Agent"),
		"accept", req.Header.Get("Accept"),
		"body_size_bytes", bodySize,
		"has_api_key", strings.Contains(req.URL.RawQuery, "key="),
		"headers_count", len(req.Header))
}

// logResponseDetails logs detailed HTTP response information
func (c *customGeminiClient) logResponseDetails(resp *http.Response, bodySize int64) {
	if !slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		return
	}
	
	slog.Debug("HTTP response details",
		"status_code", resp.StatusCode,
		"status", resp.Status,
		"content_type", resp.Header.Get("Content-Type"),
		"content_length", resp.ContentLength,
		"body_size_bytes", bodySize,
		"headers_count", len(resp.Header),
		"has_cache_control", resp.Header.Get("Cache-Control") != "",
		"transfer_encoding", resp.Header.Get("Transfer-Encoding"))
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
	req, _, err := c.buildHTTPRequestWithSize(ctx, method, url, request)
	return req, err
}

// buildHTTPRequestWithSize creates an HTTP request for the Gemini API and returns the request body size
func (c *customGeminiClient) buildHTTPRequestWithSize(ctx context.Context, method, url string, request *geminiRequest) (*http.Request, int, error) {
	// Serialize request body
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	bodySize := len(requestBody)
	
	// Log JSON request body in debug mode
	if slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		slog.Debug("Gemini request body",
			"body_size_bytes", bodySize,
			"contents_count", len(request.Contents),
			"has_system_instruction", request.SystemInstruction != nil,
			"tools_count", len(request.Tools),
			"has_generation_config", request.GenerationConfig != nil)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set required headers
	req.Header.Set("Content-Type", "application/json")

	// Set user agent
	req.Header.Set("User-Agent", "Crush/1.0")

	// Note: API key is added as query parameter in buildRequestURL, not as Authorization header

	return req, bodySize, nil
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

	// Validate response structure with lenient rules for empty parts
	if err := c.validateResponseLenient(&response); err != nil {
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

// validateResponseLenient validates a response with lenient rules for empty parts
func (c *customGeminiClient) validateResponseLenient(response *geminiResponse) error {
	if len(response.Candidates) == 0 {
		return fmt.Errorf("candidates cannot be empty")
	}

	for i, candidate := range response.Candidates {
		if err := candidate.ValidateStreaming(); err != nil {
			return fmt.Errorf("candidates[%d]: %w", i, err)
		}
	}

	if response.UsageMetadata != nil {
		if err := response.UsageMetadata.Validate(); err != nil {
			return fmt.Errorf("usageMetadata: %w", err)
		}
	}

	return nil
}

// generateToolCallID generates a unique ID for tool calls
func generateToolCallID() string {
	// Simple implementation using timestamp and random suffix
	// In a production system, you might want to use UUID
	return fmt.Sprintf("call_%d", time.Now().UnixNano())
}

// handleHTTPError handles HTTP error responses from the Gemini API
func (c *customGeminiClient) handleHTTPError(resp *http.Response) error {
	// Read response body
	var responseBody bytes.Buffer
	if _, err := responseBody.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("HTTP %d: failed to read error response body: %w", resp.StatusCode, err)
	}

	// Create structured HTTP error
	httpErr := createHTTPError(resp, responseBody.Bytes())

	// Log error details for debugging
	slog.Error("HTTP error response",
		"status_code", resp.StatusCode,
		"status", resp.Status,
		"message", httpErr.Message,
		"content_type", resp.Header.Get("Content-Type"))

	return httpErr
}
