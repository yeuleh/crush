package provider

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/message"
)

// CustomGeminiClient implements ProviderClient interface for direct HTTP API calls to Gemini
type customGeminiClient struct {
	providerOptions    providerClientOptions
	httpClient         *http.Client
	baseURL            string
	apiKey             string
	streamingSupported bool // cached streaming support status
	streamingChecked   bool // whether streaming support has been checked
	mu                 sync.RWMutex // protects concurrent access to streaming fields
}

// CustomGeminiClient type alias for ProviderClient interface
type CustomGeminiClient ProviderClient

// newCustomGeminiClient creates a new CustomGeminiClient instance
func newCustomGeminiClient(opts providerClientOptions) CustomGeminiClient {
	baseURL := opts.baseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}

	return &customGeminiClient{
		providerOptions:    opts,
		httpClient:         createHTTPClient(opts),
		baseURL:            baseURL,
		apiKey:             opts.apiKey,
		streamingSupported: false, // will be detected on first use
		streamingChecked:   false,
	}
}

// createHTTPClient creates an HTTP client with proxy support, debug logging, and proper timeouts
func createHTTPClient(opts providerClientOptions) *http.Client {
	// Create base transport with proxy support and optimized settings
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment, // Support system proxy
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ResponseHeaderTimeout: 30 * time.Second, // Timeout for reading response headers
		ExpectContinueTimeout: 1 * time.Second,  // Timeout for 100-continue responses
	}

	// Use debug HTTP client if debug mode is enabled
	if config.Get().Options.Debug {
		httpClient := log.NewHTTPClient()
		// Override transport to add proxy support and optimized settings
		if logger, ok := httpClient.Transport.(*log.HTTPRoundTripLogger); ok {
			logger.Transport = transport
		}
		httpClient.Timeout = 60 * time.Second
		return httpClient
	}

	// Create standard HTTP client with optimized settings
	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
}

// send implements the ProviderClient interface for non-streaming requests
func (c *customGeminiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	// Build the Gemini API request
	request, err := c.buildGeminiRequest(messages, tools)
	if err != nil {
		return nil, fmt.Errorf("failed to build Gemini request: %w", err)
	}

	// Serialize request to JSON
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build the API URL
	modelID := c.getModelID()
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.baseURL, modelID)

	// Send request with retry logic
	var response *ProviderResponse
	attempts := 0
	for {
		attempts++
		
		// Create HTTP request
		httpReq, err := c.buildRequest(ctx, "POST", url, requestBody)
		if err != nil {
			return nil, fmt.Errorf("failed to build HTTP request: %w", err)
		}

		// Send the request
		httpResp, err := c.httpClient.Do(httpReq)
		if err != nil {
			// Check if we should retry
			if shouldRetry, delay := c.shouldRetry(attempts, err, 0); shouldRetry {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
					continue
				}
			}
			return nil, fmt.Errorf("HTTP request failed: %w", err)
		}

		// Read response body
		defer httpResp.Body.Close()
		responseBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Check for HTTP errors
		if httpResp.StatusCode >= 400 {
			// Try to parse error response
			var geminiError GeminiError
			if parseErr := json.Unmarshal(responseBody, &geminiError); parseErr == nil {
				err = fmt.Errorf("Gemini API error (status %d): %s", httpResp.StatusCode, geminiError.Error.Message)
			} else {
				err = fmt.Errorf("HTTP error %d: %s", httpResp.StatusCode, string(responseBody))
			}

			// Check if we should retry
			if shouldRetry, delay := c.shouldRetry(attempts, err, httpResp.StatusCode); shouldRetry {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
					continue
				}
			}
			return nil, err
		}

		// Parse successful response
		var geminiResponse GeminiResponse
		if err := json.Unmarshal(responseBody, &geminiResponse); err != nil {
			return nil, fmt.Errorf("failed to parse Gemini response: %w", err)
		}

		// Convert to internal format
		response, err = c.convertGeminiResponseToInternal(&geminiResponse)
		if err != nil {
			return nil, fmt.Errorf("failed to convert response: %w", err)
		}

		// Set finish reason based on response
		if len(geminiResponse.Candidates) > 0 {
			response.FinishReason = c.convertFinishReason(geminiResponse.Candidates[0].FinishReason)
		}

		break // Success, exit retry loop
	}

	return response, nil
}

// stream implements the ProviderClient interface for streaming requests
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)
	
	go func() {
		defer close(eventChan)
		
		// Check if streaming is forced to be disabled
		if c.isStreamingForced() {
			slog.Debug("Streaming is disabled by configuration, using simulation")
			c.simulateStreaming(ctx, messages, tools, eventChan)
			return
		}
		
		// Check streaming support
		if c.checkStreamingSupport(ctx) {
			// Try real streaming response
			if c.tryRealStreaming(ctx, messages, tools, eventChan) {
				return // Success, completed
			}
			// If streaming failed, mark as unsupported and fallback
			c.mu.Lock()
			c.streamingSupported = false
			c.mu.Unlock()
			slog.Warn("Streaming failed, falling back to non-streaming mode")
		}
		
		// Fallback to simulated streaming response
		c.simulateStreaming(ctx, messages, tools, eventChan)
	}()
	
	return eventChan
}

// checkStreamingSupport detects if the API endpoint supports streaming
func (c *customGeminiClient) checkStreamingSupport(ctx context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.streamingChecked {
		return c.streamingSupported
	}
	
	// Try a simple streaming request to detect support
	testRequest := &GeminiRequest{
		Contents: []GeminiContent{
			{
				Parts: []GeminiPart{{Text: "test"}},
				Role:  GeminiRoleUser,
			},
		},
	}
	
	// Build the streaming URL
	modelID := c.getModelID()
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent", c.baseURL, modelID)
	
	// Create a minimal test request
	requestBody, err := json.Marshal(testRequest)
	if err != nil {
		slog.Debug("Failed to marshal test request for streaming detection", "error", err)
		c.streamingSupported = false
		c.streamingChecked = true
		return false
	}
	
	// Create HTTP request with streaming headers
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(requestBody)))
	if err != nil {
		slog.Debug("Failed to create test request for streaming detection", "error", err)
		c.streamingSupported = false
		c.streamingChecked = true
		return false
	}
	
	// Set streaming headers
	c.setStreamingHeaders(req)
	
	// Send the test request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("Streaming detection request failed", "error", err)
		c.streamingSupported = false
	} else if resp.StatusCode == 404 || resp.StatusCode == 501 || resp.StatusCode == 405 {
		// These status codes indicate streaming is not supported
		slog.Debug("Streaming not supported by API", "status_code", resp.StatusCode)
		c.streamingSupported = false
	} else if resp.StatusCode >= 400 {
		// Other client/server errors - assume streaming not supported for safety
		slog.Debug("Streaming detection failed with error", "status_code", resp.StatusCode)
		c.streamingSupported = false
	} else {
		// Success or acceptable response - streaming is supported
		slog.Debug("Streaming support detected", "status_code", resp.StatusCode)
		c.streamingSupported = true
	}
	
	if resp != nil {
		resp.Body.Close()
	}
	
	c.streamingChecked = true
	return c.streamingSupported
}

// tryRealStreaming attempts to perform a real streaming request
func (c *customGeminiClient) tryRealStreaming(ctx context.Context, messages []message.Message, tools []tools.BaseTool, eventChan chan<- ProviderEvent) bool {
	// Build the Gemini API request
	request, err := c.buildGeminiRequest(messages, tools)
	if err != nil {
		slog.Debug("Failed to build streaming request", "error", err)
		eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build streaming request: %w", err)}
		return false
	}
	
	// Build the streaming URL
	modelID := c.getModelID()
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent", c.baseURL, modelID)
	
	// Retry logic for streaming requests
	attempts := 0
	for {
		attempts++
		
		// Create the streaming request
		req, err := c.buildStreamRequest(ctx, url, request)
		if err != nil {
			slog.Debug("Failed to build stream request", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build stream request: %w", err)}
			return false
		}
		
		// Send the request
		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Check if we should retry
			if shouldRetry, delay := c.shouldRetry(attempts, err, 0); shouldRetry {
				slog.Debug("Streaming request failed, retrying", "attempt", attempts, "delay_seconds", delay.Seconds())
				select {
				case <-ctx.Done():
					eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
					return false
				case <-time.After(delay):
					continue
				}
			}
			slog.Debug("Streaming request failed", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("streaming request failed: %w", err)}
			return false
		}
		
		if resp.StatusCode >= 400 {
			// Read error response for better error handling
			defer resp.Body.Close()
			errorBody, _ := io.ReadAll(resp.Body)
			
			// Create error from response
			var responseErr error
			var geminiError GeminiError
			if parseErr := json.Unmarshal(errorBody, &geminiError); parseErr == nil {
				responseErr = fmt.Errorf("Gemini API error (status %d): %s", resp.StatusCode, geminiError.Error.Message)
			} else {
				responseErr = fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(errorBody))
			}
			
			// Check if we should retry
			if shouldRetry, delay := c.shouldRetry(attempts, responseErr, resp.StatusCode); shouldRetry {
				slog.Debug("Streaming request returned error, retrying", "status_code", resp.StatusCode, "attempt", attempts, "delay_seconds", delay.Seconds())
				select {
				case <-ctx.Done():
					eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
					return false
				case <-time.After(delay):
					continue
				}
			}
			
			slog.Debug("Streaming request returned error", "status_code", resp.StatusCode)
			eventChan <- ProviderEvent{Type: EventError, Error: responseErr}
			return false
		}
		
		defer resp.Body.Close()
		
		// Process the streaming response
		return c.processStreamResponse(resp, eventChan)
	}
}

// buildStreamRequest creates an HTTP request for streaming
func (c *customGeminiClient) buildStreamRequest(ctx context.Context, url string, request *GeminiRequest) (*http.Request, error) {
	// Serialize request to JSON
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(requestBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	
	// Set streaming headers
	c.setStreamingHeaders(req)
	
	return req, nil
}

// processStreamResponse processes a streaming HTTP response using Server-Sent Events
func (c *customGeminiClient) processStreamResponse(resp *http.Response, eventChan chan<- ProviderEvent) bool {
	defer resp.Body.Close()
	
	// Send initial content start event
	eventChan <- ProviderEvent{Type: EventContentStart}
	
	// Track accumulated response data for final event
	var accumulatedContent strings.Builder
	var accumulatedToolCalls []message.ToolCall
	var finalUsage TokenUsage
	var finishReason message.FinishReason = message.FinishReasonUnknown
	
	// Create a scanner to read the SSE stream line by line
	scanner := bufio.NewScanner(resp.Body)
	
	// Process each line of the SSE stream
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		
		// Skip empty lines and SSE comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		
		// Parse SSE data lines
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			
			// Skip SSE keep-alive messages
			if data == "" || data == "[DONE]" {
				continue
			}
			
			// Parse the JSON chunk
			var streamChunk GeminiStreamResponse
			if err := json.Unmarshal([]byte(data), &streamChunk); err != nil {
				slog.Debug("Failed to parse streaming chunk", "error", err, "data", data)
				continue
			}
			
			// Process the chunk
			if err := c.processStreamChunk(&streamChunk, eventChan, &accumulatedContent, &accumulatedToolCalls, &finalUsage, &finishReason); err != nil {
				slog.Debug("Failed to process streaming chunk", "error", err)
				continue
			}
		}
	}
	
	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		slog.Debug("Error reading streaming response", "error", err)
		eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("streaming read error: %w", err)}
		return false
	}
	
	// Send final events
	eventChan <- ProviderEvent{Type: EventContentStop}
	
	// Create final response
	finalResponse := &ProviderResponse{
		Content:      accumulatedContent.String(),
		ToolCalls:    accumulatedToolCalls,
		Usage:        finalUsage,
		FinishReason: finishReason,
	}
	
	eventChan <- ProviderEvent{
		Type:     EventComplete,
		Response: finalResponse,
	}
	
	return true
}

// processStreamChunk processes a single chunk from the streaming response
func (c *customGeminiClient) processStreamChunk(
	chunk *GeminiStreamResponse,
	eventChan chan<- ProviderEvent,
	accumulatedContent *strings.Builder,
	accumulatedToolCalls *[]message.ToolCall,
	finalUsage *TokenUsage,
	finishReason *message.FinishReason,
) error {
	// Process candidates in the chunk
	for _, candidate := range chunk.Candidates {
		if candidate.Content != nil {
			// Process each part in the candidate content
			for _, part := range candidate.Content.Parts {
				// Handle text content
				if part.Text != "" {
					// Send content delta event
					eventChan <- ProviderEvent{
						Type:    EventContentDelta,
						Content: part.Text,
					}
					
					// Accumulate content for final response
					accumulatedContent.WriteString(part.Text)
				}
				
				// Handle function calls (tool use)
				if part.FunctionCall != nil {
					// Convert to internal tool call format
					argsJSON, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						return fmt.Errorf("failed to marshal function call args: %w", err)
					}
					
					toolCall := message.ToolCall{
						ID:    generateToolCallID(),
						Name:  part.FunctionCall.Name,
						Input: string(argsJSON),
						Type:  "function",
					}
					
					// Send tool use events
					eventChan <- ProviderEvent{
						Type:     EventToolUseStart,
						ToolCall: &toolCall,
					}
					
					// For streaming, we might get the tool call in parts
					// Send the complete tool call as a delta
					eventChan <- ProviderEvent{
						Type:     EventToolUseDelta,
						ToolCall: &toolCall,
					}
					
					eventChan <- ProviderEvent{
						Type:     EventToolUseStop,
						ToolCall: &toolCall,
					}
					
					// Accumulate tool call for final response
					*accumulatedToolCalls = append(*accumulatedToolCalls, toolCall)
				}
			}
		}
		
		// Update finish reason if provided
		if candidate.FinishReason != "" {
			*finishReason = c.convertFinishReason(candidate.FinishReason)
		}
	}
	
	// Update usage metadata if provided with comprehensive handling
	if chunk.UsageMetadata != nil {
		c.updateTokenUsage(finalUsage, chunk.UsageMetadata)
	}
	
	return nil
}

// simulateStreaming simulates streaming events using non-streaming API
// This method creates a realistic streaming experience by chunking content and adding delays
func (c *customGeminiClient) simulateStreaming(ctx context.Context, messages []message.Message, tools []tools.BaseTool, eventChan chan<- ProviderEvent) {
	// Call non-streaming API
	response, err := c.send(ctx, messages, tools)
	if err != nil {
		eventChan <- ProviderEvent{Type: EventError, Error: err}
		return
	}
	
	// Send initial content start event (matches real streaming)
	eventChan <- ProviderEvent{Type: EventContentStart}
	
	// Get simulation configuration
	config := c.getSimulationConfig()
	
	// Simulate content streaming with configurable chunking strategy
	if response.Content != "" {
		if err := c.simulateContentStreaming(ctx, response.Content, config, eventChan); err != nil {
			eventChan <- ProviderEvent{Type: EventError, Error: err}
			return
		}
	}
	
	// Simulate tool calls with realistic event sequence (matches real streaming)
	if err := c.simulateToolCallStreaming(ctx, response.ToolCalls, config, eventChan); err != nil {
		eventChan <- ProviderEvent{Type: EventError, Error: err}
		return
	}
	
	// Send final events (matches real streaming sequence)
	eventChan <- ProviderEvent{Type: EventContentStop}
	eventChan <- ProviderEvent{
		Type:     EventComplete,
		Response: response,
	}
}

// simulationConfig holds configuration for simulated streaming behavior
type simulationConfig struct {
	chunkSize        int           // Size of content chunks
	baseDelay        time.Duration // Base delay between chunks
	delayVariation   time.Duration // Random variation in delay
	chunkingStrategy string        // Strategy for chunking: "fixed", "word", "sentence", "natural"
	toolCallDelay    time.Duration // Delay for tool call events
	enableJitter     bool          // Whether to add random jitter to delays
}

// getSimulationConfig extracts simulation configuration from provider options
func (c *customGeminiClient) getSimulationConfig() simulationConfig {
	config := simulationConfig{
		chunkSize:        20,                    // Default chunk size
		baseDelay:        20 * time.Millisecond, // Default delay
		delayVariation:   5 * time.Millisecond,  // Default variation
		chunkingStrategy: "natural",             // Default strategy
		toolCallDelay:    10 * time.Millisecond, // Default tool call delay
		enableJitter:     true,                  // Default enable jitter
	}
	
	// Extract chunk size
	if chunkSize, ok := c.providerOptions.extraBody["chunk_size"]; ok {
		if size, ok := chunkSize.(int); ok && size > 0 {
			config.chunkSize = size
		} else if size, ok := chunkSize.(float64); ok && size > 0 {
			config.chunkSize = int(size)
		}
	}
	
	// Extract base delay
	if delay, ok := c.providerOptions.extraBody["simulation_delay"]; ok {
		if delayInt, ok := delay.(int); ok && delayInt > 0 {
			config.baseDelay = time.Duration(delayInt) * time.Millisecond
		} else if delayFloat, ok := delay.(float64); ok && delayFloat > 0 {
			config.baseDelay = time.Duration(delayFloat) * time.Millisecond
		}
	}
	
	// Extract delay variation
	if variation, ok := c.providerOptions.extraBody["delay_variation"]; ok {
		if variationInt, ok := variation.(int); ok && variationInt > 0 {
			config.delayVariation = time.Duration(variationInt) * time.Millisecond
		} else if variationFloat, ok := variation.(float64); ok && variationFloat > 0 {
			config.delayVariation = time.Duration(variationFloat) * time.Millisecond
		}
	}
	
	// Extract chunking strategy
	if strategy, ok := c.providerOptions.extraBody["chunking_strategy"]; ok {
		if strategyStr, ok := strategy.(string); ok {
			switch strategyStr {
			case "fixed", "word", "sentence", "natural":
				config.chunkingStrategy = strategyStr
			}
		}
	}
	
	// Extract tool call delay
	if toolDelay, ok := c.providerOptions.extraBody["tool_call_delay"]; ok {
		if delayInt, ok := toolDelay.(int); ok && delayInt > 0 {
			config.toolCallDelay = time.Duration(delayInt) * time.Millisecond
		} else if delayFloat, ok := toolDelay.(float64); ok && delayFloat > 0 {
			config.toolCallDelay = time.Duration(delayFloat) * time.Millisecond
		}
	}
	
	// Extract jitter setting
	if jitter, ok := c.providerOptions.extraBody["enable_jitter"]; ok {
		if jitterBool, ok := jitter.(bool); ok {
			config.enableJitter = jitterBool
		}
	}
	
	return config
}

// simulateContentStreaming simulates streaming of content with configurable chunking
func (c *customGeminiClient) simulateContentStreaming(ctx context.Context, content string, config simulationConfig, eventChan chan<- ProviderEvent) error {
	chunks := c.chunkContent(content, config)
	
	for i, chunk := range chunks {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		
		// Send content delta event
		eventChan <- ProviderEvent{
			Type:    EventContentDelta,
			Content: chunk,
		}
		
		// Add delay between chunks (except for the last chunk)
		if i < len(chunks)-1 {
			delay := c.calculateDelay(config)
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}
	
	return nil
}

// simulateToolCallStreaming simulates streaming of tool calls with realistic event sequence
func (c *customGeminiClient) simulateToolCallStreaming(ctx context.Context, toolCalls []message.ToolCall, config simulationConfig, eventChan chan<- ProviderEvent) error {
	for _, toolCall := range toolCalls {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		
		// Send tool use start event
		eventChan <- ProviderEvent{
			Type:     EventToolUseStart,
			ToolCall: &toolCall,
		}
		
		// Add small delay to simulate processing
		if config.toolCallDelay > 0 {
			time.Sleep(config.toolCallDelay)
		}
		
		// Send tool use delta event (matches real streaming behavior)
		eventChan <- ProviderEvent{
			Type:     EventToolUseDelta,
			ToolCall: &toolCall,
		}
		
		// Add another small delay
		if config.toolCallDelay > 0 {
			time.Sleep(config.toolCallDelay / 2)
		}
		
		// Send tool use stop event
		eventChan <- ProviderEvent{
			Type:     EventToolUseStop,
			ToolCall: &toolCall,
		}
	}
	
	return nil
}

// chunkContent splits content into chunks based on the configured strategy
func (c *customGeminiClient) chunkContent(content string, config simulationConfig) []string {
	if content == "" {
		return nil
	}
	
	switch config.chunkingStrategy {
	case "fixed":
		return c.chunkContentFixed(content, config.chunkSize)
	case "word":
		return c.chunkContentByWords(content, config.chunkSize)
	case "sentence":
		return c.chunkContentBySentences(content, config.chunkSize)
	case "natural":
		return c.chunkContentNatural(content, config.chunkSize)
	default:
		return c.chunkContentFixed(content, config.chunkSize)
	}
}

// chunkContentFixed splits content into fixed-size chunks
func (c *customGeminiClient) chunkContentFixed(content string, chunkSize int) []string {
	if chunkSize <= 0 {
		return []string{content}
	}
	
	var chunks []string
	runes := []rune(content) // Handle Unicode properly
	
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	
	return chunks
}

// chunkContentByWords splits content by words, keeping chunks around the target size
func (c *customGeminiClient) chunkContentByWords(content string, targetSize int) []string {
	if targetSize <= 0 {
		return []string{content}
	}
	
	if len(content) == 0 {
		return nil
	}
	
	// Use a simple approach: split by character position but respect word boundaries
	var chunks []string
	runes := []rune(content)
	start := 0
	
	for start < len(runes) {
		end := start + targetSize
		if end > len(runes) {
			end = len(runes)
		}
		
		// If we're not at the end and we're in the middle of a word, back up to the last space
		if end < len(runes) {
			// Look for the last space before the target position
			for end > start && runes[end] != ' ' {
				end--
			}
			// If we didn't find a space, use the original end position (break the word)
			if end == start {
				end = start + targetSize
				if end > len(runes) {
					end = len(runes)
				}
			} else {
				// Include the space in this chunk
				end++
			}
		}
		
		chunk := string(runes[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = end
	}
	
	return chunks
}

// chunkContentBySentences splits content by sentences, keeping chunks around the target size
func (c *customGeminiClient) chunkContentBySentences(content string, targetSize int) []string {
	if targetSize <= 0 {
		return []string{content}
	}
	
	if len(content) == 0 {
		return nil
	}
	
	// Find sentence boundaries while preserving the original content structure
	var chunks []string
	runes := []rune(content)
	start := 0
	
	for start < len(runes) {
		end := start + targetSize
		if end > len(runes) {
			end = len(runes)
		}
		
		// If we're not at the end, try to find a sentence boundary
		if end < len(runes) {
			// Look for sentence endings (., !, ?) before the target position
			sentenceEnd := -1
			for i := end - 1; i > start; i-- {
				if runes[i] == '.' || runes[i] == '!' || runes[i] == '?' {
					sentenceEnd = i + 1 // Include the punctuation
					break
				}
			}
			
			if sentenceEnd > start {
				end = sentenceEnd
				// Skip any trailing spaces after the sentence
				for end < len(runes) && runes[end] == ' ' {
					end++
				}
			} else {
				// No sentence boundary found, look for word boundary
				for end > start && runes[end] != ' ' {
					end--
				}
				if end == start {
					// No word boundary either, just break at target size
					end = start + targetSize
					if end > len(runes) {
						end = len(runes)
					}
				} else {
					// Include the space
					end++
				}
			}
		}
		
		chunk := string(runes[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = end
	}
	
	return chunks
}

// chunkContentNatural uses a natural chunking strategy that varies chunk sizes
func (c *customGeminiClient) chunkContentNatural(content string, baseSize int) []string {
	if baseSize <= 0 {
		return []string{content}
	}
	
	if len(content) == 0 {
		return nil
	}
	
	// Natural chunking with size variation
	var chunks []string
	runes := []rune(content)
	start := 0
	
	for start < len(runes) {
		// Vary chunk size naturally (80% to 120% of base size)
		variation := float64(baseSize) * 0.4 * (rand.Float64() - 0.5) // -20% to +20%
		targetSize := int(float64(baseSize) + variation)
		if targetSize < 1 {
			targetSize = 1
		}
		
		end := start + targetSize
		if end > len(runes) {
			end = len(runes)
		}
		
		// If we're not at the end and we're in the middle of a word, back up to the last space
		if end < len(runes) {
			// Look for the last space before the target position
			for end > start && runes[end] != ' ' {
				end--
			}
			// If we didn't find a space, use the original end position (break the word)
			if end == start {
				end = start + targetSize
				if end > len(runes) {
					end = len(runes)
				}
			} else {
				// Include the space in this chunk
				end++
			}
		}
		
		chunk := string(runes[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = end
	}
	
	return chunks
}

// calculateDelay calculates the delay between chunks with optional jitter
func (c *customGeminiClient) calculateDelay(config simulationConfig) time.Duration {
	delay := config.baseDelay
	
	if config.enableJitter && config.delayVariation > 0 {
		// Add random jitter: -variation to +variation
		jitter := time.Duration(rand.Int63n(int64(config.delayVariation*2))) - config.delayVariation
		delay += jitter
		
		// Ensure delay is not negative
		if delay < 0 {
			delay = config.baseDelay / 2
		}
	}
	
	return delay
}

// getSimulationChunkSize returns the chunk size for simulated streaming (legacy method for backward compatibility)
func (c *customGeminiClient) getSimulationChunkSize() int {
	config := c.getSimulationConfig()
	return config.chunkSize
}

// getSimulationDelay returns the delay for simulated streaming (legacy method for backward compatibility)
func (c *customGeminiClient) getSimulationDelay() time.Duration {
	config := c.getSimulationConfig()
	return config.baseDelay
}

// isStreamingForced checks if non-streaming mode is forced via configuration
func (c *customGeminiClient) isStreamingForced() bool {
	if forced, ok := c.providerOptions.extraBody["force_non_streaming"]; ok {
		if forcedBool, ok := forced.(bool); ok {
			return forcedBool
		}
	}
	return false
}

// Model returns the model configuration for this client
func (c *customGeminiClient) Model() catwalk.Model {
	return c.providerOptions.model(c.providerOptions.modelType)
}

// buildRequest creates an HTTP request with proper authentication and headers
func (c *customGeminiClient) buildRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}

	// Set request body if provided
	if body != nil {
		req.Body = http.NoBody
		if len(body) > 0 {
			req.Body = io.NopCloser(strings.NewReader(string(body)))
			req.ContentLength = int64(len(body))
		}
	}

	// Set authentication and standard headers
	c.setRequestHeaders(req)

	return req, nil
}

// setRequestHeaders sets authentication and standard headers for requests
func (c *customGeminiClient) setRequestHeaders(req *http.Request) {
	// Set API key authentication
	if c.apiKey != "" {
		// For Gemini API, we can use either query parameter or header
		// Using query parameter as it's the standard for Gemini API
		q := req.URL.Query()
		q.Set("key", c.apiKey)
		req.URL.RawQuery = q.Encode()
	}

	// Set standard headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CustomGeminiClient/1.0")

	// Add extra headers from provider configuration
	for key, value := range c.providerOptions.extraHeaders {
		req.Header.Set(key, value)
	}
}

// setStreamingHeaders sets headers specific to streaming requests
func (c *customGeminiClient) setStreamingHeaders(req *http.Request) {
	c.setRequestHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
}

// RetryConfig holds configuration for retry behavior
type RetryConfig struct {
	MaxRetries    int
	BaseDelay     time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	JitterFactor  float64
}

// ErrorType represents different categories of errors for retry logic
type ErrorType int

const (
	ErrorTypeUnknown ErrorType = iota
	ErrorTypeRateLimit
	ErrorTypeAuthentication
	ErrorTypeNetwork
	ErrorTypeServer
	ErrorTypeClient
	ErrorTypeTimeout
)

// RetryableError wraps an error with retry information
type RetryableError struct {
	Err       error
	Type      ErrorType
	Retryable bool
	Delay     time.Duration
}

func (r *RetryableError) Error() string {
	return r.Err.Error()
}

func (r *RetryableError) Unwrap() error {
	return r.Err
}

// defaultRetryConfig returns the default retry configuration
func defaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,                // Default retry attempts
		BaseDelay:     1 * time.Second,
		MaxDelay:      30 * time.Second, // Maximum delay
		BackoffFactor: 2.0,
		JitterFactor:  0.1,
	}
}

// getRetryConfig returns retry configuration with user overrides applied
func (c *customGeminiClient) getRetryConfig() RetryConfig {
	config := defaultRetryConfig()
	
	// Allow customization via extraBody configuration
	if maxRetries, ok := c.providerOptions.extraBody["max_retries"]; ok {
		if retriesInt, ok := maxRetries.(int); ok && retriesInt >= 0 {
			config.MaxRetries = retriesInt
		} else if retriesFloat, ok := maxRetries.(float64); ok && retriesFloat >= 0 {
			config.MaxRetries = int(retriesFloat)
		}
	}
	
	if baseDelay, ok := c.providerOptions.extraBody["retry_base_delay_ms"]; ok {
		if delayInt, ok := baseDelay.(int); ok && delayInt > 0 {
			config.BaseDelay = time.Duration(delayInt) * time.Millisecond
		} else if delayFloat, ok := baseDelay.(float64); ok && delayFloat > 0 {
			config.BaseDelay = time.Duration(delayFloat) * time.Millisecond
		}
	}
	
	if maxDelay, ok := c.providerOptions.extraBody["retry_max_delay_ms"]; ok {
		if delayInt, ok := maxDelay.(int); ok && delayInt > 0 {
			config.MaxDelay = time.Duration(delayInt) * time.Millisecond
		} else if delayFloat, ok := maxDelay.(float64); ok && delayFloat > 0 {
			config.MaxDelay = time.Duration(delayFloat) * time.Millisecond
		}
	}
	
	if backoffFactor, ok := c.providerOptions.extraBody["retry_backoff_factor"]; ok {
		if factorFloat, ok := backoffFactor.(float64); ok && factorFloat > 1.0 {
			config.BackoffFactor = factorFloat
		}
	}
	
	if jitterFactor, ok := c.providerOptions.extraBody["retry_jitter_factor"]; ok {
		if jitterFloat, ok := jitterFactor.(float64); ok && jitterFloat >= 0.0 && jitterFloat <= 1.0 {
			config.JitterFactor = jitterFloat
		}
	}
	
	return config
}

// shouldRetry determines if a request should be retried based on the error and attempt count
func (c *customGeminiClient) shouldRetry(attempts int, err error, statusCode int) (bool, time.Duration) {
	config := c.getRetryConfig()
	
	if attempts >= config.MaxRetries {
		slog.Debug("Maximum retry attempts reached", "attempts", attempts, "max_retries", config.MaxRetries)
		return false, 0
	}

	// Classify and analyze the error
	retryableErr := c.classifyError(err, statusCode)
	
	if !retryableErr.Retryable {
		slog.Debug("Error is not retryable", "error_type", retryableErr.Type, "error", err)
		return false, 0
	}

	var shouldRetry bool
	var delay time.Duration

	// Handle different error types with specific strategies
	switch retryableErr.Type {
	case ErrorTypeAuthentication:
		// Try to refresh API key for authentication errors
		if c.tryRefreshAPIKey() {
			slog.Info("API key refreshed, retrying immediately")
			return true, 0 // Retry immediately after key refresh
		}
		slog.Debug("API key refresh failed, not retrying")
		return false, 0
		
	case ErrorTypeRateLimit:
		// Use longer delays for rate limiting with exponential backoff
		delay = c.calculateRateLimitDelay(attempts, config)
		shouldRetry = true
		
	case ErrorTypeNetwork:
		// Handle network errors with extended delays
		shouldRetry, delay = c.handleNetworkError(err, attempts)
		
	case ErrorTypeTimeout:
		// Handle timeout errors with progressive delays
		shouldRetry, delay = c.handleTimeoutError(err, attempts)
		
	case ErrorTypeServer:
		// Standard exponential backoff for server errors
		delay = calculateBackoffDelay(attempts, config)
		shouldRetry = true
		
	default:
		// Standard exponential backoff for other retryable errors
		delay = calculateBackoffDelay(attempts, config)
		shouldRetry = true
	}

	if shouldRetry {
		c.logRetryAttempt(attempts, retryableErr.Type, err, delay)
	}

	return shouldRetry, delay
}

// classifyError analyzes an error and determines its type and retry behavior
func (c *customGeminiClient) classifyError(err error, statusCode int) *RetryableError {
	if err == nil && statusCode == 0 {
		return &RetryableError{
			Err:       fmt.Errorf("unknown error"),
			Type:      ErrorTypeUnknown,
			Retryable: false,
		}
	}

	// Analyze HTTP status codes first
	if statusCode > 0 {
		switch statusCode {
		case 401, 403:
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeAuthentication,
				Retryable: true, // Will attempt API key refresh
			}
		case 429:
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeRateLimit,
				Retryable: true,
			}
		case 400, 404, 405, 422:
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeClient,
				Retryable: false, // Client errors are not retryable
			}
		case 500, 502, 503, 504:
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeServer,
				Retryable: true,
			}
		}
	}

	// Analyze error messages for network and timeout issues
	if err != nil {
		errStr := strings.ToLower(err.Error())
		
		// Timeout errors
		if strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "deadline exceeded") ||
			strings.Contains(errStr, "context deadline exceeded") {
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeTimeout,
				Retryable: true,
			}
		}
		
		// Network connectivity errors
		if strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "connection aborted") ||
			strings.Contains(errStr, "network is unreachable") ||
			strings.Contains(errStr, "no route to host") ||
			strings.Contains(errStr, "temporary failure") ||
			strings.Contains(errStr, "dns") {
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeNetwork,
				Retryable: true,
			}
		}
		
		// Rate limiting errors in message
		if strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "quota exceeded") ||
			strings.Contains(errStr, "too many requests") {
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeRateLimit,
				Retryable: true,
			}
		}
		
		// Authentication errors in message
		if strings.Contains(errStr, "unauthorized") ||
			strings.Contains(errStr, "invalid api key") ||
			strings.Contains(errStr, "authentication failed") ||
			strings.Contains(errStr, "api key expired") ||
			strings.Contains(errStr, "invalid credentials") {
			return &RetryableError{
				Err:       err,
				Type:      ErrorTypeAuthentication,
				Retryable: true,
			}
		}
	}

	// Default to non-retryable unknown error
	return &RetryableError{
		Err:       err,
		Type:      ErrorTypeUnknown,
		Retryable: false,
	}
}

// calculateBackoffDelay calculates the delay for exponential backoff with jitter
func calculateBackoffDelay(attempt int, config RetryConfig) time.Duration {
	// Calculate exponential backoff
	delay := float64(config.BaseDelay) * math.Pow(config.BackoffFactor, float64(attempt))
	
	// Apply maximum delay limit
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	// Add jitter to avoid thundering herd
	jitter := delay * config.JitterFactor * (rand.Float64()*2 - 1) // Random between -jitter and +jitter
	delay += jitter

	// Ensure delay is not negative
	if delay < 0 {
		delay = float64(config.BaseDelay)
	}

	return time.Duration(delay)
}

// calculateRateLimitDelay calculates delay specifically for rate limit errors with longer backoff
func (c *customGeminiClient) calculateRateLimitDelay(attempt int, config RetryConfig) time.Duration {
	// Use longer base delay for rate limiting (minimum 5 seconds)
	baseDelay := config.BaseDelay
	if baseDelay < 5*time.Second {
		baseDelay = 5 * time.Second
	}
	
	// More aggressive exponential backoff for rate limits
	delay := float64(baseDelay) * math.Pow(2.5, float64(attempt))
	
	// Higher maximum delay for rate limits (up to 2 minutes)
	maxDelay := config.MaxDelay
	if maxDelay < 120*time.Second {
		maxDelay = 120 * time.Second
	}
	
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}

	// Add more jitter for rate limits to spread out requests
	jitterFactor := 0.25 // 25% jitter for rate limits
	jitter := delay * jitterFactor * (rand.Float64()*2 - 1)
	delay += jitter

	// Ensure minimum delay
	if delay < float64(baseDelay) {
		delay = float64(baseDelay)
	}

	return time.Duration(delay)
}

// tryRefreshAPIKey attempts to refresh the API key for authentication errors
func (c *customGeminiClient) tryRefreshAPIKey() bool {
	slog.Debug("Attempting to refresh API key")
	
	// Check if there's a refresh API key configured for calling a refresh endpoint
	if refreshKey := c.getRefreshAPIKey(); refreshKey != "" {
		// In a real implementation, this would call an API key refresh endpoint
		// For now, we'll just log that this would be attempted
		slog.Debug("Refresh API key available, would call refresh endpoint")
	}
	
	// Try alternative API key first
	if newKey := c.getAlternativeAPIKey(); newKey != "" {
		c.apiKey = newKey
		slog.Info("API key refreshed successfully using alternative key")
		return true
	}
	
	// Try to reload API key from environment
	if envKey := c.reloadAPIKeyFromEnvironment(); envKey != "" {
		c.apiKey = envKey
		slog.Info("API key reloaded from environment")
		return true
	}
	
	slog.Debug("API key refresh not available or failed")
	return false
}

// getRefreshAPIKey gets the refresh API key from configuration
func (c *customGeminiClient) getRefreshAPIKey() string {
	if refreshKey, ok := c.providerOptions.extraBody["refresh_api_key"]; ok {
		if keyStr, ok := refreshKey.(string); ok {
			return keyStr
		}
	}
	return ""
}

// getAlternativeAPIKey gets an alternative API key from configuration
func (c *customGeminiClient) getAlternativeAPIKey() string {
	if altKey, ok := c.providerOptions.extraBody["alternative_api_key"]; ok {
		if keyStr, ok := altKey.(string); ok {
			return keyStr
		}
	}
	return ""
}

// reloadAPIKeyFromEnvironment attempts to reload the API key from environment variables
func (c *customGeminiClient) reloadAPIKeyFromEnvironment() string {
	// Try common environment variable names for Gemini API keys
	envVars := []string{
		"GEMINI_API_KEY",
		"GOOGLE_API_KEY", 
		"GENERATIVE_AI_API_KEY",
		"GOOGLE_GENERATIVE_AI_API_KEY",
	}
	
	for _, envVar := range envVars {
		if key := os.Getenv(envVar); key != "" {
			slog.Debug("Reloaded API key from environment", "env_var", envVar)
			return key
		}
	}
	
	return ""
}

// isTemporaryError checks if an error is temporary and should be retried
func (c *customGeminiClient) isTemporaryError(err error) bool {
	if err == nil {
		return false
	}
	
	retryableErr := c.classifyError(err, 0)
	return retryableErr.Retryable
}

// handleNetworkError provides specific handling for network-related errors
func (c *customGeminiClient) handleNetworkError(err error, attempt int) (bool, time.Duration) {
	config := c.getRetryConfig()
	
	if attempt >= config.MaxRetries {
		return false, 0
	}
	
	// For network errors, use a more conservative retry approach
	baseDelay := config.BaseDelay * 2 // Double the base delay for network issues
	delay := calculateBackoffDelay(attempt, RetryConfig{
		MaxRetries:    config.MaxRetries,
		BaseDelay:     baseDelay,
		MaxDelay:      config.MaxDelay,
		BackoffFactor: config.BackoffFactor,
		JitterFactor:  config.JitterFactor * 2, // More jitter for network issues
	})
	
	slog.Info("Network error encountered, retrying with extended delay", 
		"attempt", attempt, "delay_seconds", delay.Seconds(), "error", err.Error())
	
	return true, delay
}

// handleTimeoutError provides specific handling for timeout errors
func (c *customGeminiClient) handleTimeoutError(err error, attempt int) (bool, time.Duration) {
	config := c.getRetryConfig()
	
	if attempt >= config.MaxRetries {
		return false, 0
	}
	
	// For timeout errors, use exponential backoff with longer delays
	delay := calculateBackoffDelay(attempt, RetryConfig{
		MaxRetries:    config.MaxRetries,
		BaseDelay:     config.BaseDelay * 3, // Triple the base delay for timeouts
		MaxDelay:      config.MaxDelay * 2,  // Allow longer max delay
		BackoffFactor: config.BackoffFactor,
		JitterFactor:  config.JitterFactor,
	})
	
	slog.Info("Timeout error encountered, retrying with extended delay", 
		"attempt", attempt, "delay_seconds", delay.Seconds())
	
	return true, delay
}

// logRetryAttempt logs retry attempts with appropriate detail level
func (c *customGeminiClient) logRetryAttempt(attempt int, errorType ErrorType, err error, delay time.Duration) {
	errMsg := "unknown error"
	if err != nil {
		errMsg = err.Error()
	}
	
	switch errorType {
	case ErrorTypeRateLimit:
		slog.Warn("Rate limit exceeded, backing off", 
			"attempt", attempt, "delay_seconds", delay.Seconds())
	case ErrorTypeAuthentication:
		slog.Warn("Authentication error, attempting recovery", 
			"attempt", attempt)
	case ErrorTypeNetwork:
		slog.Info("Network error, retrying", 
			"attempt", attempt, "delay_seconds", delay.Seconds(), "error", errMsg)
	case ErrorTypeTimeout:
		slog.Info("Request timeout, retrying with longer delay", 
			"attempt", attempt, "delay_seconds", delay.Seconds())
	case ErrorTypeServer:
		slog.Info("Server error, retrying", 
			"attempt", attempt, "delay_seconds", delay.Seconds(), "status", errMsg)
	default:
		slog.Debug("Retrying request", 
			"attempt", attempt, "error_type", errorType, "delay_seconds", delay.Seconds())
	}
}

// Gemini API Data Structures

// GeminiRequest represents the request payload for Gemini API
type GeminiRequest struct {
	Contents          []GeminiContent        `json:"contents"`
	Tools             []GeminiTool           `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig      `json:"toolConfig,omitempty"`
	SafetySettings    []GeminiSafetySetting  `json:"safetySettings,omitempty"`
	SystemInstruction *GeminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

// GeminiResponse represents the response from Gemini API
type GeminiResponse struct {
	Candidates    []GeminiCandidate     `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata  `json:"usageMetadata,omitempty"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback,omitempty"`
}

// GeminiContent represents content with parts and role
type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

// GeminiPart represents a part of content (text, inline data, function call, etc.)
type GeminiPart struct {
	Text             string                   `json:"text,omitempty"`
	InlineData       *GeminiInlineData        `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall      `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse  `json:"functionResponse,omitempty"`
}

// GeminiInlineData represents inline binary data (e.g., images)
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// GeminiFunctionCall represents a function call in the request/response
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// GeminiFunctionResponse represents a function response
type GeminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// GeminiCandidate represents a response candidate
type GeminiCandidate struct {
	Content       *GeminiContent         `json:"content"`
	FinishReason  string                 `json:"finishReason"`
	Index         int                    `json:"index"`
	SafetyRatings []GeminiSafetyRating   `json:"safetyRatings,omitempty"`
}

// GeminiTool represents tool definitions for function calling
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations"`
}

// GeminiFunctionDeclaration represents a function declaration for tools
type GeminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// GeminiToolConfig represents tool configuration
type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// GeminiFunctionCallingConfig represents function calling configuration
type GeminiFunctionCallingConfig struct {
	Mode                string   `json:"mode,omitempty"` // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GeminiSafetySetting represents safety settings for content filtering
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiSafetyRating represents safety rating in response
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// GeminiPromptFeedback represents feedback about the prompt
type GeminiPromptFeedback struct {
	BlockReason   string               `json:"blockReason,omitempty"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
}

// GeminiGenerationConfig represents generation configuration
type GeminiGenerationConfig struct {
	StopSequences   []string `json:"stopSequences,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
}

// GeminiUsageMetadata represents token usage statistics
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// GeminiError represents error response from Gemini API
type GeminiError struct {
	Error GeminiErrorDetails `json:"error"`
}

// GeminiErrorDetails represents detailed error information
type GeminiErrorDetails struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// GeminiStreamResponse represents a streaming response chunk
type GeminiStreamResponse struct {
	Candidates    []GeminiCandidate     `json:"candidates,omitempty"`
	UsageMetadata *GeminiUsageMetadata  `json:"usageMetadata,omitempty"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback,omitempty"`
}

// Message Format Converters

// convertMessagesToGemini converts internal messages to Gemini API format
func (c *customGeminiClient) convertMessagesToGemini(messages []message.Message) ([]GeminiContent, *GeminiContent, error) {
	var contents []GeminiContent
	var systemInstruction *GeminiContent

	for _, msg := range messages {
		switch msg.Role {
		case message.System:
			// System messages become system instruction
			if systemInstruction == nil {
				systemInstruction = &GeminiContent{}
			}
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert system message parts: %w", err)
			}
			systemInstruction.Parts = append(systemInstruction.Parts, parts...)

		case message.User:
			// User messages
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert user message parts: %w", err)
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  GeminiRoleUser,
					Parts: parts,
				})
			}

		case message.Assistant:
			// Assistant messages
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert assistant message parts: %w", err)
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  GeminiRoleModel,
					Parts: parts,
				})
			}

		case message.Tool:
			// Tool result messages - these should be combined with the previous assistant message
			// or added as a separate user message containing function responses
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert tool message parts: %w", err)
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  GeminiRoleUser,
					Parts: parts,
				})
			}
		}
	}

	return contents, systemInstruction, nil
}

// convertPartsToGemini converts internal message parts to Gemini API parts
func (c *customGeminiClient) convertPartsToGemini(parts []message.ContentPart) ([]GeminiPart, error) {
	var geminiParts []GeminiPart

	for _, part := range parts {
		switch p := part.(type) {
		case message.TextContent:
			// Text content
			if p.Text != "" {
				geminiParts = append(geminiParts, GeminiPart{
					Text: p.Text,
				})
			}

		case message.ReasoningContent:
			// Reasoning content - convert thinking to text
			if p.Thinking != "" {
				geminiParts = append(geminiParts, GeminiPart{
					Text: p.Thinking,
				})
			}

		case message.BinaryContent:
			// Binary content (images) - convert to inline data
			if len(p.Data) > 0 {
				encodedData := base64.StdEncoding.EncodeToString(p.Data)
				geminiParts = append(geminiParts, GeminiPart{
					InlineData: &GeminiInlineData{
						MimeType: p.MIMEType,
						Data:     encodedData,
					},
				})
			}

		case message.ImageURLContent:
			// Image URL content - we can't directly use URLs in Gemini API
			// This would need to be downloaded and converted to binary content
			// For now, we'll skip it or could add a text description
			// TODO: Consider downloading the image and converting to binary

		case message.ToolCall:
			// Tool calls - convert to function calls
			if p.Name != "" {
				args := make(map[string]interface{})
				if p.Input != "" {
					// Parse the input JSON into args
					if err := json.Unmarshal([]byte(p.Input), &args); err != nil {
						// If parsing fails, treat as a single string argument
						args["input"] = p.Input
					}
				}
				geminiParts = append(geminiParts, GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: p.Name,
						Args: args,
					},
				})
			}

		case message.ToolResult:
			// Tool results - convert to function responses
			if p.Name != "" {
				response := make(map[string]interface{})
				response["content"] = p.Content
				if p.Metadata != "" {
					// Try to parse metadata as JSON, otherwise include as string
					var metadata interface{}
					if err := json.Unmarshal([]byte(p.Metadata), &metadata); err == nil {
						response["metadata"] = metadata
					} else {
						response["metadata"] = p.Metadata
					}
				}
				if p.IsError {
					response["is_error"] = true
				}

				geminiParts = append(geminiParts, GeminiPart{
					FunctionResponse: &GeminiFunctionResponse{
						Name:     p.Name,
						Response: response,
					},
				})
			}

		case message.Finish:
			// Finish parts don't need to be converted to Gemini API format
			// They are internal metadata
			continue

		default:
			// Unknown part type - skip it
			continue
		}
	}

	return geminiParts, nil
}

// convertToolsToGemini converts internal tools to Gemini API tool format
func (c *customGeminiClient) convertToolsToGemini(tools []tools.BaseTool) ([]GeminiTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	var functionDeclarations []GeminiFunctionDeclaration

	for _, tool := range tools {
		info := tool.Info()
		
		// Validate tool definition completeness
		if err := c.validateToolDefinition(info); err != nil {
			return nil, fmt.Errorf("invalid tool definition for %s: %w", info.Name, err)
		}
		
		// Convert tool parameters to Gemini function declaration format
		parameters, err := c.convertParameterSchema(info.Parameters, info.Required)
		if err != nil {
			return nil, fmt.Errorf("failed to convert parameters for tool %s: %w", info.Name, err)
		}

		functionDeclarations = append(functionDeclarations, GeminiFunctionDeclaration{
			Name:        info.Name,
			Description: info.Description,
			Parameters:  parameters,
		})
	}

	return []GeminiTool{
		{
			FunctionDeclarations: functionDeclarations,
		},
	}, nil
}

// validateToolDefinition validates the completeness of a tool definition
func (c *customGeminiClient) validateToolDefinition(info tools.ToolInfo) error {
	if info.Name == "" {
		return fmt.Errorf("tool name is required")
	}
	
	if info.Description == "" {
		return fmt.Errorf("tool description is required")
	}
	
	// Parameters can be nil for tools that don't take parameters
	if info.Parameters != nil {
		// Validate that required fields exist in parameters
		if len(info.Required) > 0 {
			properties, hasProperties := info.Parameters["properties"]
			if !hasProperties {
				return fmt.Errorf("tool has required fields but no properties defined")
			}
			
			propertiesMap, ok := properties.(map[string]interface{})
			if !ok {
				return fmt.Errorf("properties must be an object")
			}
			
			for _, required := range info.Required {
				if _, exists := propertiesMap[required]; !exists {
					return fmt.Errorf("required field %s not found in properties", required)
				}
			}
		}
	}
	
	return nil
}

// convertParameterSchema recursively converts parameter schema to Gemini API format
func (c *customGeminiClient) convertParameterSchema(schema map[string]interface{}, required []string) (map[string]interface{}, error) {
	if schema == nil {
		// Default empty object schema for tools with no parameters
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}, nil
	}
	
	// Create a deep copy of the schema to avoid modifying the original
	converted := make(map[string]interface{})
	
	// Process each field in the schema
	for key, value := range schema {
		convertedValue, err := c.convertSchemaValue(value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert schema field %s: %w", key, err)
		}
		converted[key] = convertedValue
	}
	
	// Add required fields if they exist and we're dealing with an object type
	if schemaType, hasType := converted["type"]; hasType && schemaType == "object" {
		if len(required) > 0 {
			converted["required"] = required
		}
	}
	
	return converted, nil
}

// convertSchemaValue recursively converts a schema value (handles objects, arrays, and primitives)
func (c *customGeminiClient) convertSchemaValue(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case map[string]interface{}:
		// Handle nested objects
		converted := make(map[string]interface{})
		
		for key, val := range v {
			convertedVal, err := c.convertSchemaValue(val)
			if err != nil {
				return nil, fmt.Errorf("failed to convert nested field %s: %w", key, err)
			}
			converted[key] = convertedVal
		}
		
		return converted, nil
		
	case []interface{}:
		// Handle arrays
		converted := make([]interface{}, len(v))
		
		for i, item := range v {
			convertedItem, err := c.convertSchemaValue(item)
			if err != nil {
				return nil, fmt.Errorf("failed to convert array item at index %d: %w", i, err)
			}
			converted[i] = convertedItem
		}
		
		return converted, nil
		
	case []string:
		// Handle string arrays (like required fields)
		converted := make([]interface{}, len(v))
		for i, item := range v {
			converted[i] = item
		}
		return converted, nil
		
	default:
		// Handle primitive values (string, number, boolean, etc.)
		return value, nil
	}
}

// convertGeminiResponseToInternal converts Gemini API response to internal format
func (c *customGeminiClient) convertGeminiResponseToInternal(response *GeminiResponse) (*ProviderResponse, error) {
	if len(response.Candidates) == 0 {
		return &ProviderResponse{
			Content:   "",
			ToolCalls: []message.ToolCall{},
			Usage:     c.extractTokenUsage(response.UsageMetadata),
		}, nil
	}

	// Use the first candidate
	candidate := response.Candidates[0]
	
	var content strings.Builder
	var toolCalls []message.ToolCall

	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				content.WriteString(part.Text)
			}

			if part.FunctionCall != nil {
				// Convert function call to internal tool call format
				argsJSON, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal function call args: %w", err)
				}

				toolCalls = append(toolCalls, message.ToolCall{
					ID:    generateToolCallID(), // Generate a unique ID
					Name:  part.FunctionCall.Name,
					Input: string(argsJSON),
					Type:  "function", // Default type for function calls
				})
			}
		}
	}

	// Extract token usage with comprehensive error handling
	usage := c.extractTokenUsage(response.UsageMetadata)

	return &ProviderResponse{
		Content:   content.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
	}, nil
}

// generateToolCallID generates a unique ID for tool calls
func generateToolCallID() string {
	// Generate a unique ID using timestamp and random component
	// This ensures uniqueness even when called in quick succession
	return fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63())
}

// buildGeminiRequest creates a complete Gemini API request from messages and tools
func (c *customGeminiClient) buildGeminiRequest(messages []message.Message, tools []tools.BaseTool) (*GeminiRequest, error) {
	// Convert messages to Gemini format
	contents, systemInstruction, err := c.convertMessagesToGemini(messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Convert tools to Gemini format
	geminiTools, err := c.convertToolsToGemini(tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tools: %w", err)
	}

	// Build the request
	request := &GeminiRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
		Tools:             geminiTools,
	}

	// Add generation config from provider options
	if config := c.buildGenerationConfig(); config != nil {
		request.GenerationConfig = config
	}

	// Add tool config if tools are present
	if len(geminiTools) > 0 {
		request.ToolConfig = &GeminiToolConfig{
			FunctionCallingConfig: &GeminiFunctionCallingConfig{
				Mode: GeminiFunctionCallingModeAuto,
			},
		}
	}

	// Add safety settings from extra body if present
	if safetySettings := c.extractSafetySettings(); len(safetySettings) > 0 {
		request.SafetySettings = safetySettings
	}

	return request, nil
}

// buildGenerationConfig creates generation config from provider options
func (c *customGeminiClient) buildGenerationConfig() *GeminiGenerationConfig {
	config := &GeminiGenerationConfig{}
	hasConfig := false

	// Set max tokens if specified
	if c.providerOptions.maxTokens > 0 {
		maxTokens := int(c.providerOptions.maxTokens)
		config.MaxOutputTokens = &maxTokens
		hasConfig = true
	}

	// Set temperature if specified in extra body
	if temp := c.extractTemperature(); temp != nil {
		config.Temperature = temp
		hasConfig = true
	}

	// Set other parameters from extra body
	if topP := c.extractTopP(); topP != nil {
		config.TopP = topP
		hasConfig = true
	}

	if topK := c.extractTopK(); topK != nil {
		config.TopK = topK
		hasConfig = true
	}

	if !hasConfig {
		return nil
	}

	return config
}

// getModelID returns the model ID for API requests
func (c *customGeminiClient) getModelID() string {
	model := c.Model()
	if model.ID != "" {
		return model.ID
	}
	// Default fallback
	return "gemini-1.5-pro"
}

// convertFinishReason converts Gemini finish reason to internal format
func (c *customGeminiClient) convertFinishReason(reason string) message.FinishReason {
	switch reason {
	case GeminiFinishReasonStop:
		return message.FinishReasonEndTurn
	case GeminiFinishReasonMaxTokens:
		return message.FinishReasonMaxTokens
	case GeminiFinishReasonSafety:
		return message.FinishReasonPermissionDenied
	case GeminiFinishReasonRecitation:
		return message.FinishReasonPermissionDenied
	default:
		return message.FinishReasonUnknown
	}
}

// extractTemperature extracts temperature from extra body configuration
func (c *customGeminiClient) extractTemperature() *float64 {
	if temp, ok := c.providerOptions.extraBody["temperature"]; ok {
		if tempFloat, ok := temp.(float64); ok {
			return &tempFloat
		}
		if tempInt, ok := temp.(int); ok {
			tempFloat := float64(tempInt)
			return &tempFloat
		}
	}
	return nil
}

// extractTopP extracts topP from extra body configuration
func (c *customGeminiClient) extractTopP() *float64 {
	if topP, ok := c.providerOptions.extraBody["topP"]; ok {
		if topPFloat, ok := topP.(float64); ok {
			return &topPFloat
		}
		if topPInt, ok := topP.(int); ok {
			topPFloat := float64(topPInt)
			return &topPFloat
		}
	}
	return nil
}

// extractTopK extracts topK from extra body configuration
func (c *customGeminiClient) extractTopK() *int {
	if topK, ok := c.providerOptions.extraBody["topK"]; ok {
		if topKInt, ok := topK.(int); ok {
			return &topKInt
		}
		if topKFloat, ok := topK.(float64); ok {
			topKInt := int(topKFloat)
			return &topKInt
		}
	}
	return nil
}

// extractSafetySettings extracts safety settings from extra body configuration
func (c *customGeminiClient) extractSafetySettings() []GeminiSafetySetting {
	if settings, ok := c.providerOptions.extraBody["safetySettings"]; ok {
		if settingsSlice, ok := settings.([]interface{}); ok {
			var safetySettings []GeminiSafetySetting
			for _, setting := range settingsSlice {
				if settingMap, ok := setting.(map[string]interface{}); ok {
					if category, ok := settingMap["category"].(string); ok {
						if threshold, ok := settingMap["threshold"].(string); ok {
							safetySettings = append(safetySettings, GeminiSafetySetting{
								Category:  category,
								Threshold: threshold,
							})
						}
					}
				}
			}
			return safetySettings
		}
	}
	return nil
}

// Constants for Gemini API

// Gemini API roles
const (
	GeminiRoleUser  = "user"
	GeminiRoleModel = "model"
)

// Gemini API finish reasons
const (
	GeminiFinishReasonStop         = "STOP"
	GeminiFinishReasonMaxTokens    = "MAX_TOKENS"
	GeminiFinishReasonSafety       = "SAFETY"
	GeminiFinishReasonRecitation   = "RECITATION"
	GeminiFinishReasonOther        = "OTHER"
)

// Gemini API safety categories
const (
	GeminiSafetyCategoryHarassment       = "HARM_CATEGORY_HARASSMENT"
	GeminiSafetyCategoryHateSpeech       = "HARM_CATEGORY_HATE_SPEECH"
	GeminiSafetyCategorySexuallyExplicit = "HARM_CATEGORY_SEXUALLY_EXPLICIT"
	GeminiSafetyCategoryDangerousContent = "HARM_CATEGORY_DANGEROUS_CONTENT"
)

// Gemini API safety thresholds
const (
	GeminiSafetyThresholdBlockNone         = "BLOCK_NONE"
	GeminiSafetyThresholdBlockOnlyHigh     = "BLOCK_ONLY_HIGH"
	GeminiSafetyThresholdBlockMediumAndAbove = "BLOCK_MEDIUM_AND_ABOVE"
	GeminiSafetyThresholdBlockLowAndAbove  = "BLOCK_LOW_AND_ABOVE"
)

// Gemini API function calling modes
const (
	GeminiFunctionCallingModeAuto = "AUTO"
	GeminiFunctionCallingModeAny  = "ANY"
	GeminiFunctionCallingModeNone = "NONE"
)

// extractTokenUsage extracts token usage statistics from Gemini API usage metadata
// This method provides comprehensive handling of all token types and ensures consistency
func (c *customGeminiClient) extractTokenUsage(metadata *GeminiUsageMetadata) TokenUsage {
	usage := TokenUsage{
		InputTokens:         0,
		OutputTokens:        0,
		CacheCreationTokens: 0, // Gemini API doesn't provide cache creation tokens directly
		CacheReadTokens:     0,
	}

	// Handle case where metadata is not available
	if metadata == nil {
		slog.Debug("No usage metadata available in response")
		return usage
	}

	// Extract input tokens (prompt tokens)
	if metadata.PromptTokenCount > 0 {
		usage.InputTokens = int64(metadata.PromptTokenCount)
	}

	// Extract output tokens (candidates tokens)
	if metadata.CandidatesTokenCount > 0 {
		usage.OutputTokens = int64(metadata.CandidatesTokenCount)
	}

	// Extract cache read tokens (cached content tokens)
	if metadata.CachedContentTokenCount > 0 {
		usage.CacheReadTokens = int64(metadata.CachedContentTokenCount)
	}

	// Log token usage for debugging
	if config.Get().Options.Debug {
		slog.Debug("Extracted token usage",
			"input_tokens", usage.InputTokens,
			"output_tokens", usage.OutputTokens,
			"cache_read_tokens", usage.CacheReadTokens,
			"total_tokens", metadata.TotalTokenCount,
		)
	}

	// Validate token counts are consistent
	if metadata.TotalTokenCount > 0 {
		calculatedTotal := usage.InputTokens + usage.OutputTokens
		if calculatedTotal != int64(metadata.TotalTokenCount) {
			slog.Debug("Token count mismatch detected",
				"calculated_total", calculatedTotal,
				"reported_total", metadata.TotalTokenCount,
				"input_tokens", usage.InputTokens,
				"output_tokens", usage.OutputTokens,
			)
		}
	}

	return usage
}

// updateTokenUsage updates token usage statistics during streaming responses
// This method ensures consistent token counting across streaming chunks
func (c *customGeminiClient) updateTokenUsage(finalUsage *TokenUsage, metadata *GeminiUsageMetadata) {
	if metadata == nil {
		return
	}

	// Update input tokens (should be consistent across chunks)
	if metadata.PromptTokenCount > 0 {
		newInputTokens := int64(metadata.PromptTokenCount)
		if finalUsage.InputTokens == 0 {
			finalUsage.InputTokens = newInputTokens
		} else if finalUsage.InputTokens != newInputTokens {
			// Log inconsistency but use the latest value
			slog.Debug("Input token count changed during streaming",
				"previous", finalUsage.InputTokens,
				"new", newInputTokens,
			)
			finalUsage.InputTokens = newInputTokens
		}
	}

	// Update output tokens (accumulate across chunks)
	if metadata.CandidatesTokenCount > 0 {
		newOutputTokens := int64(metadata.CandidatesTokenCount)
		if newOutputTokens > finalUsage.OutputTokens {
			finalUsage.OutputTokens = newOutputTokens
		}
	}

	// Update cache read tokens (should be consistent across chunks)
	if metadata.CachedContentTokenCount > 0 {
		newCacheReadTokens := int64(metadata.CachedContentTokenCount)
		if finalUsage.CacheReadTokens == 0 {
			finalUsage.CacheReadTokens = newCacheReadTokens
		} else if finalUsage.CacheReadTokens != newCacheReadTokens {
			// Log inconsistency but use the latest value
			slog.Debug("Cache read token count changed during streaming",
				"previous", finalUsage.CacheReadTokens,
				"new", newCacheReadTokens,
			)
			finalUsage.CacheReadTokens = newCacheReadTokens
		}
	}

	// Cache creation tokens are not provided by Gemini API
	// Keep as 0 to maintain consistency with other providers
	finalUsage.CacheCreationTokens = 0

	// Log updated usage for debugging
	if config.Get().Options.Debug {
		slog.Debug("Updated streaming token usage",
			"input_tokens", finalUsage.InputTokens,
			"output_tokens", finalUsage.OutputTokens,
			"cache_read_tokens", finalUsage.CacheReadTokens,
			"total_reported", metadata.TotalTokenCount,
		)
	}
}

// validateTokenUsage validates token usage statistics for consistency and accuracy
// This method helps ensure token counting meets the requirements
func (c *customGeminiClient) validateTokenUsage(usage TokenUsage, metadata *GeminiUsageMetadata) error {
	// Validate that all token counts are non-negative (check first)
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheCreationTokens < 0 {
		return fmt.Errorf("token counts must be non-negative, got input=%d, output=%d, cache_read=%d, cache_creation=%d",
			usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens)
	}

	// Requirement 6.4: When usage metadata is not available, all token counts should be zero
	if metadata == nil {
		if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheReadTokens != 0 || usage.CacheCreationTokens != 0 {
			return fmt.Errorf("token usage should be zero when metadata is unavailable, got input=%d, output=%d, cache_read=%d, cache_creation=%d",
				usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens)
		}
		return nil
	}

	// Requirement 6.1: Input tokens should match prompt token count
	if metadata.PromptTokenCount > 0 && usage.InputTokens != int64(metadata.PromptTokenCount) {
		return fmt.Errorf("input token mismatch: expected %d, got %d", metadata.PromptTokenCount, usage.InputTokens)
	}

	// Requirement 6.2: Output tokens should match candidates token count
	if metadata.CandidatesTokenCount > 0 && usage.OutputTokens != int64(metadata.CandidatesTokenCount) {
		return fmt.Errorf("output token mismatch: expected %d, got %d", metadata.CandidatesTokenCount, usage.OutputTokens)
	}

	// Requirement 6.3: Cache read tokens should match cached content token count
	if metadata.CachedContentTokenCount > 0 && usage.CacheReadTokens != int64(metadata.CachedContentTokenCount) {
		return fmt.Errorf("cache read token mismatch: expected %d, got %d", metadata.CachedContentTokenCount, usage.CacheReadTokens)
	}

	return nil
}