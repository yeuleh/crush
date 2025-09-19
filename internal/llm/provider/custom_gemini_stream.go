package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// streamStandard implements Server-Sent Events streaming for standard URL mode
func (c *customGeminiClient) streamStandard(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)
		
		attempts := 0
		for {
			attempts++

			// Build request URL for streaming endpoint (rebuild in case of retries)
			methodPath := c.buildGeminiMethodPath(c.getModelName(), "streamGenerateContent")
			requestURL, err := c.buildRequestURL(methodPath)
			if err != nil {
				slog.Error("Failed to build streaming request URL", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build request URL: %w", err)}
				return
			}

			// Convert messages to Gemini format
			request, err := c.convertMessages(messages, tools)
			if err != nil {
				slog.Error("Failed to convert messages for streaming", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to convert messages: %w", err)}
				return
			}

			// Build HTTP request
			httpReq, bodySize, err := c.buildHTTPRequestWithSize(ctx, "POST", requestURL, request)
			if err != nil {
				slog.Error("Failed to build HTTP request for streaming", 
					"error", err,
					"url", requestURL,
					"attempt", attempts)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build HTTP request: %w", err)}
				return
			}

			// Set Accept header for SSE
			httpReq.Header.Set("Accept", "text/event-stream")
			
			// Log detailed request information in debug mode
			c.logRequestDetails(httpReq, bodySize)

			c.logOperationContext("sse_streaming_start", 
				"url", requestURL,
				"messages_count", len(messages),
				"tools_count", len(tools),
				"attempt", attempts,
				"request_body_size", bodySize)

			// Execute HTTP request
			streamStart := time.Now()
			resp, err := c.httpClient.Do(httpReq)
			streamConnectDuration := time.Since(streamStart)
			if err != nil {
				slog.Error("SSE stream connection failed", 
					"error", err,
					"attempt", attempts,
					"connect_duration_ms", streamConnectDuration.Milliseconds())
				
				// Check if this is a retryable error
				retry, after, retryErr := c.shouldRetry(attempts, err)
				if retryErr != nil {
					slog.Error("Max retries exceeded for SSE stream", 
						"error", retryErr,
						"attempts", attempts,
						"max_retries", maxRetries)
					eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("HTTP request failed: %w", retryErr)}
					return
				}
				if retry {
					slog.Warn("Retrying streaming request due to error",
						"attempt", attempts,
						"max_retries", maxRetries,
						"error", err.Error(),
						"retry_after_ms", after.Milliseconds())
					select {
					case <-ctx.Done():
						eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
						return
					case <-time.After(after):
						continue
					}
				}
				slog.Error("HTTP streaming request failed", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("HTTP request failed: %w", err)}
				return
			}
			
			slog.Debug("SSE stream connected successfully", 
				"status_code", resp.StatusCode,
				"content_type", resp.Header.Get("Content-Type"),
				"connect_duration_ms", streamConnectDuration.Milliseconds(),
				"attempt", attempts)
			
			defer resp.Body.Close()

			// Check HTTP status code
			if resp.StatusCode != http.StatusOK {
				httpErr := c.handleHTTPError(resp)
				
				// Check if this is a retryable HTTP error
				retry, after, retryErr := c.shouldRetry(attempts, httpErr)
				if retryErr != nil {
					slog.Error("HTTP streaming request returned error status", "status", resp.StatusCode, "error", retryErr)
					eventChan <- ProviderEvent{Type: EventError, Error: retryErr}
					return
				}
				if retry {
					slog.Warn("Retrying streaming request due to HTTP error",
						"attempt", attempts,
						"max_retries", maxRetries,
						"status_code", resp.StatusCode,
						"retry_after_ms", after.Milliseconds())
					select {
					case <-ctx.Done():
						eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
						return
					case <-time.After(after):
						continue
					}
				}
				slog.Error("HTTP streaming request returned error status", "status", resp.StatusCode)
				eventChan <- ProviderEvent{Type: EventError, Error: httpErr}
				return
			}

			// Process SSE stream
			processStart := time.Now()
			if err := c.processSSEStreamWithLogging(ctx, resp.Body, eventChan); err != nil {
				processDuration := time.Since(processStart)
				slog.Error("Failed to process SSE stream", 
					"error", err,
					"process_duration_ms", processDuration.Milliseconds(),
					"attempt", attempts)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to process SSE stream: %w", err)}
				return
			}

			processDuration := time.Since(processStart)
			totalDuration := time.Since(streamStart)
			slog.Info("SSE streaming completed successfully", 
				"attempts", attempts,
				"connect_duration_ms", streamConnectDuration.Milliseconds(),
				"process_duration_ms", processDuration.Milliseconds(),
				"total_duration_ms", totalDuration.Milliseconds())
			return
		}
	}()

	return eventChan
}

// processSSEStreamWithLogging wraps processSSEStream with additional logging
func (c *customGeminiClient) processSSEStreamWithLogging(ctx context.Context, body io.Reader, eventChan chan<- ProviderEvent) error {
	if !slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		// If debug logging is not enabled, use the standard method
		return c.processSSEStream(ctx, body, eventChan)
	}
	
	var chunksReceived int
	var bytesRead int64
	var contentDeltas int
	var toolCallEvents int
	var parseErrors int
	
	// Create a counting reader to track bytes read
	countingReader := &countingReader{reader: body, count: &bytesRead}
	
	defer func() {
		slog.Debug("SSE stream processing completed",
			"chunks_received", chunksReceived,
			"bytes_read", bytesRead,
			"content_deltas", contentDeltas,
			"tool_call_events", toolCallEvents,
			"parse_errors", parseErrors)
	}()
	
	scanner := bufio.NewScanner(countingReader)

	// Send content start event
	eventChan <- ProviderEvent{Type: EventContentStart}

	var accumulatedContent strings.Builder
	var finalUsage TokenUsage
	var finalFinishReason message.FinishReason = message.FinishReasonEndTurn
	var toolCalls []message.ToolCall

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			slog.Debug("SSE stream cancelled by context", 
				"chunks_processed", chunksReceived,
				"bytes_read", bytesRead)
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		// Skip empty lines and non-data lines
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		chunksReceived++
		
		// Extract data content
		data := strings.TrimPrefix(line, "data: ")

		// Check for stream termination
		if data == "[DONE]" {
			slog.Debug("Received stream termination marker", 
				"chunks_processed", chunksReceived)
			break
		}

		// Parse stream chunk
		event, err := c.parseStreamChunk(data)
		if err != nil {
			parseErrors++
			slog.Warn("Failed to parse stream chunk", 
				"error", err, 
				"chunk_number", chunksReceived,
				"data_length", len(data))
			continue // Skip invalid chunks instead of failing the entire stream
		}

		if event != nil {
			// Track event types for debugging
			switch event.Type {
			case EventContentDelta:
				contentDeltas++
				accumulatedContent.WriteString(event.Content)
				if contentDeltas%10 == 0 {
					slog.Debug("SSE content delta progress",
						"content_deltas", contentDeltas,
						"current_content_length", accumulatedContent.Len(),
						"delta_length", len(event.Content))
				}
				
			case EventToolUseStart:
				toolCallEvents++
				if event.ToolCall != nil {
					toolCalls = append(toolCalls, *event.ToolCall)
					slog.Debug("SSE tool call received",
						"tool_name", event.ToolCall.Name,
						"tool_calls_count", len(toolCalls))
				}
				
			case EventComplete:
				if event.Response != nil {
					finalUsage = event.Response.Usage
					finalFinishReason = event.Response.FinishReason
					slog.Debug("SSE complete event received",
						"finish_reason", finalFinishReason,
						"prompt_tokens", finalUsage.InputTokens,
						"completion_tokens", finalUsage.OutputTokens)
					// Skip sending intermediate complete events
					continue
				}
			}

			// Send the event (except complete events which we handle at the end)
			if event.Type != EventComplete {
				eventChan <- *event
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading SSE stream: %w", err)
	}

	// Send content stop event
	eventChan <- ProviderEvent{Type: EventContentStop}

	// Send final complete event with accumulated data
	eventChan <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			Content:      accumulatedContent.String(),
			ToolCalls:    toolCalls,
			Usage:        finalUsage,
			FinishReason: finalFinishReason,
		},
	}

	return nil
}

// countingReader wraps an io.Reader to count bytes read
type countingReader struct {
	reader io.Reader
	count  *int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)
	*cr.count += int64(n)
	return n, err
}

// processSSEStream processes Server-Sent Events stream from Gemini API
func (c *customGeminiClient) processSSEStream(ctx context.Context, body io.Reader, eventChan chan<- ProviderEvent) error {
	scanner := bufio.NewScanner(body)

	// Send content start event
	eventChan <- ProviderEvent{Type: EventContentStart}

	var accumulatedContent strings.Builder
	var finalUsage TokenUsage
	var finalFinishReason message.FinishReason = message.FinishReasonEndTurn
	var toolCalls []message.ToolCall

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			slog.Info("SSE stream cancelled by context")
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		// Skip empty lines and non-data lines
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Extract data content
		data := strings.TrimPrefix(line, "data: ")

		// Check for stream termination
		if data == "[DONE]" {
			slog.Debug("Received stream termination marker")
			break
		}

		// Parse stream chunk
		event, err := c.parseStreamChunk(data)
		if err != nil {
			slog.Warn("Failed to parse stream chunk", "error", err, "data", data)
			continue // Skip invalid chunks instead of failing the entire stream
		}

		if event != nil {
			// Accumulate content for final response
			if event.Type == EventContentDelta {
				accumulatedContent.WriteString(event.Content)
			}

			// Collect tool calls
			if event.Type == EventToolUseStart && event.ToolCall != nil {
				toolCalls = append(toolCalls, *event.ToolCall)
			}

			// Update usage and finish reason from complete events, but don't send them yet
			if event.Type == EventComplete && event.Response != nil {
				finalUsage = event.Response.Usage
				finalFinishReason = event.Response.FinishReason
				// Skip sending intermediate complete events
				continue
			}

			// Send the event (except complete events which we handle at the end)
			eventChan <- *event
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading SSE stream: %w", err)
	}

	// Send content stop event
	eventChan <- ProviderEvent{Type: EventContentStop}

	// Send final complete event with accumulated data
	eventChan <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			Content:      accumulatedContent.String(),
			ToolCalls:    toolCalls,
			Usage:        finalUsage,
			FinishReason: finalFinishReason,
		},
	}

	return nil
}

// parseStreamChunk parses a single SSE data chunk from Gemini API
func (c *customGeminiClient) parseStreamChunk(data string) (*ProviderEvent, error) {
	var chunk geminiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stream chunk: %w", err)
	}

	// Validate chunk structure
	if err := chunk.Validate(); err != nil {
		return nil, fmt.Errorf("invalid stream chunk structure: %w", err)
	}

	// Handle empty chunk (heartbeat)
	if len(chunk.Candidates) == 0 && chunk.UsageMetadata == nil {
		return nil, nil // Skip heartbeat chunks
	}

	// Process candidates
	if len(chunk.Candidates) > 0 {
		candidate := chunk.Candidates[0] // Use first candidate

		// Extract content and tool calls
		content, toolCalls := c.extractContentAndToolCalls(candidate.Content)

		// Handle content delta
		if content != "" {
			return &ProviderEvent{
				Type:    EventContentDelta,
				Content: content,
			}, nil
		}

		// Handle tool calls
		if len(toolCalls) > 0 {
			// For streaming, we typically get one tool call at a time
			return &ProviderEvent{
				Type:     EventToolUseStart,
				ToolCall: &toolCalls[0],
			}, nil
		}

		// Handle finish reason if present
		if candidate.FinishReason != "" {
			finishReason := c.convertFinishReason(candidate.FinishReason)

			// Create a complete event with finish reason
			return &ProviderEvent{
				Type: EventComplete,
				Response: &ProviderResponse{
					Content:      "",
					ToolCalls:    []message.ToolCall{},
					Usage:        c.convertUsage(chunk.UsageMetadata),
					FinishReason: finishReason,
				},
			}, nil
		}
	}

	// Handle usage metadata only chunks (typically at the end)
	if chunk.UsageMetadata != nil {
		return &ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content:      "",
				ToolCalls:    []message.ToolCall{},
				Usage:        c.convertUsage(chunk.UsageMetadata),
				FinishReason: message.FinishReasonEndTurn,
			},
		}, nil
	}

	// Unknown chunk type, skip
	return nil, nil
}

// streamSimulated implements streaming simulation for complete URL mode
// It first calls the complete URL to get the full response, then simulates streaming
func (c *customGeminiClient) streamSimulated(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)
		
		attempts := 0
		for {
			attempts++

			// Build request URL for complete URL mode (non-streaming) (rebuild in case of retries)
			methodPath := c.buildGeminiMethodPath(c.getModelName(), "generateContent")
			requestURL, err := c.buildRequestURL(methodPath)
			if err != nil {
				slog.Error("Failed to build complete URL request URL", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build request URL: %w", err)}
				return
			}

			// Convert messages to Gemini format
			request, err := c.convertMessages(messages, tools)
			if err != nil {
				slog.Error("Failed to convert messages for streaming simulation", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to convert messages: %w", err)}
				return
			}

			// Build HTTP request
			httpReq, err := c.buildHTTPRequest(ctx, "POST", requestURL, request)
			if err != nil {
				slog.Error("Failed to build HTTP request for streaming simulation", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build HTTP request: %w", err)}
				return
			}

			slog.Info("Starting streaming simulation with complete URL",
				"url", requestURL,
				"model", c.getModelName(),
				"messages_count", len(messages),
				"tools_count", len(tools),
				"attempt", attempts)

			// Execute HTTP request to get complete response
			resp, err := c.httpClient.Do(httpReq)
			if err != nil {
				// Check if this is a retryable error
				retry, after, retryErr := c.shouldRetry(attempts, err)
				if retryErr != nil {
					slog.Error("HTTP request failed for streaming simulation", "error", retryErr)
					eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("HTTP request failed: %w", retryErr)}
					return
				}
				if retry {
					slog.Warn("Retrying simulated streaming request due to error",
						"attempt", attempts,
						"max_retries", maxRetries,
						"error", err.Error(),
						"retry_after_ms", after.Milliseconds())
					select {
					case <-ctx.Done():
						eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
						return
					case <-time.After(after):
						continue
					}
				}
				slog.Error("HTTP request failed for streaming simulation", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("HTTP request failed: %w", err)}
				return
			}
			defer resp.Body.Close()

			// Check HTTP status code
			if resp.StatusCode != http.StatusOK {
				httpErr := c.handleHTTPError(resp)
				
				// Check if this is a retryable HTTP error
				retry, after, retryErr := c.shouldRetry(attempts, httpErr)
				if retryErr != nil {
					slog.Error("HTTP request returned error status for streaming simulation", "status", resp.StatusCode, "error", retryErr)
					eventChan <- ProviderEvent{Type: EventError, Error: retryErr}
					return
				}
				if retry {
					slog.Warn("Retrying simulated streaming request due to HTTP error",
						"attempt", attempts,
						"max_retries", maxRetries,
						"status_code", resp.StatusCode,
						"retry_after_ms", after.Milliseconds())
					select {
					case <-ctx.Done():
						eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
						return
					case <-time.After(after):
						continue
					}
				}
				slog.Error("HTTP request returned error status for streaming simulation", "status", resp.StatusCode)
				eventChan <- ProviderEvent{Type: EventError, Error: httpErr}
				return
			}

			// Read complete response body
			var responseBody bytes.Buffer
			if _, err := responseBody.ReadFrom(resp.Body); err != nil {
				slog.Error("Failed to read response body for streaming simulation", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to read response body: %w", err)}
				return
			}

			// Parse complete response
			response, err := c.parseResponse(responseBody.Bytes())
			if err != nil {
				slog.Error("Failed to parse response for streaming simulation", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to parse response: %w", err)}
				return
			}

			slog.Info("Got complete response, starting streaming simulation",
				"content_length", len(response.Content),
				"tool_calls_count", len(response.ToolCalls),
				"finish_reason", response.FinishReason,
				"attempts", attempts)

			// Simulate streaming from the complete response
			if err := c.simulateStream(ctx, response, eventChan); err != nil {
				slog.Error("Failed to simulate stream", "error", err)
				eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to simulate stream: %w", err)}
				return
			}

			slog.Info("Streaming simulation completed successfully", "attempts", attempts)
			return
		}
	}()

	return eventChan
}

// simulateStream simulates streaming events from a complete response
// It chunks the content and sends it with realistic delays to mimic actual streaming
func (c *customGeminiClient) simulateStream(ctx context.Context, response *ProviderResponse, eventChan chan<- ProviderEvent) error {
	start := time.Now()
	slog.Debug("Starting streaming simulation",
		"content_length", len(response.Content),
		"tool_calls_count", len(response.ToolCalls))

	// Send content start event
	eventChan <- ProviderEvent{Type: EventContentStart}

	// Handle tool calls first if they exist
	for i, toolCall := range response.ToolCalls {
		select {
		case <-ctx.Done():
			slog.Debug("Streaming simulation cancelled during tool calls", "tool_call_index", i)
			return ctx.Err()
		default:
		}

		slog.Debug("Sending tool call event", "tool_name", toolCall.Name, "index", i)
		eventChan <- ProviderEvent{
			Type:     EventToolUseStart,
			ToolCall: &toolCall,
		}

		// Small delay between tool calls
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Stream content if it exists
	if response.Content != "" {
		if err := c.streamContentChunks(ctx, response.Content, eventChan); err != nil {
			slog.Error("Failed to stream content chunks", "error", err)
			return fmt.Errorf("failed to stream content chunks: %w", err)
		}
	} else {
		slog.Debug("No content to stream, skipping content chunks")
	}

	// Send content stop event
	eventChan <- ProviderEvent{Type: EventContentStop}

	// Send final complete event
	eventChan <- ProviderEvent{
		Type:     EventComplete,
		Response: response,
	}

	duration := time.Since(start)
	slog.Info("Streaming simulation completed",
		"duration_ms", duration.Milliseconds(),
		"content_length", len(response.Content),
		"tool_calls_count", len(response.ToolCalls))

	return nil
}

// streamContentChunks streams content in chunks with realistic delays
func (c *customGeminiClient) streamContentChunks(ctx context.Context, content string, eventChan chan<- ProviderEvent) error {
	if content == "" {
		return nil
	}

	// Strategy: Split by words and send in chunks of 1-4 words
	words := strings.Fields(content)
	if len(words) == 0 {
		// Content might be non-word characters, send as single chunk
		slog.Debug("Content has no words, sending as single chunk", "content_length", len(content))
		eventChan <- ProviderEvent{
			Type:    EventContentDelta,
			Content: content,
		}
		return nil
	}

	slog.Debug("Starting content chunking", "total_words", len(words))

	currentPos := 0
	chunkCount := 0
	for currentPos < len(words) {
		select {
		case <-ctx.Done():
			slog.Debug("Content streaming cancelled", "chunks_sent", chunkCount, "progress_percent", float64(currentPos)/float64(len(words))*100)
			return ctx.Err()
		default:
		}

		// Determine chunk size (1-4 words, with bias towards 1-2 words)
		chunkSize := c.calculateChunkSize(len(words), currentPos)
		if currentPos+chunkSize > len(words) {
			chunkSize = len(words) - currentPos
		}

		// Build chunk content efficiently using a strings.Builder for larger chunks
		var chunk string
		if chunkSize == 1 {
			// Optimize for single word (most common case)
			chunk = words[currentPos]
		} else {
			// Use strings.Join for multi-word chunks
			chunk = strings.Join(words[currentPos:currentPos+chunkSize], " ")
		}

		// Add space after chunk unless it's the last one
		if currentPos+chunkSize < len(words) {
			chunk += " "
		}

		// Send chunk
		eventChan <- ProviderEvent{
			Type:    EventContentDelta,
			Content: chunk,
		}

		chunkCount++
		currentPos += chunkSize

		// Log progress for very long content
		if len(words) > 100 && chunkCount%20 == 0 {
			slog.Debug("Content streaming progress",
				"chunks_sent", chunkCount,
				"words_processed", currentPos,
				"total_words", len(words),
				"progress_percent", float64(currentPos)/float64(len(words))*100)
		}

		// Simulate realistic delay between chunks
		delay := c.calculateDelay(currentPos, len(words))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	slog.Debug("Content chunking completed", "total_chunks", chunkCount, "total_words", len(words))
	return nil
}

// calculateChunkSize determines the size of the next content chunk
// Uses weighted randomness to bias towards smaller chunks for realistic streaming feel
func (c *customGeminiClient) calculateChunkSize(totalWords, currentPos int) int {
	// For very short responses, prefer single words
	if totalWords <= 10 {
		return 1
	}

	// Bias towards smaller chunks (1-2 words) for more realistic streaming
	random := rand.Float32()

	// 60% chance of 1 word, 25% chance of 2 words, 10% chance of 3 words, 5% chance of 4 words
	switch {
	case random < 0.6:
		return 1
	case random < 0.85:
		return 2
	case random < 0.95:
		return 3
	default:
		return 4
	}
}

// calculateDelay calculates the delay between chunks to simulate realistic streaming
// Mimics actual LLM response patterns with variable delays based on position and content
func (c *customGeminiClient) calculateDelay(currentPos, totalWords int) time.Duration {
	const (
		minDelay = 10  // Minimum delay in milliseconds
		maxDelay = 180 // Maximum delay in milliseconds
	)

	// Base delay varies between 20-120ms for realistic feel
	baseDelay := 20 + rand.Intn(100) // 20-120ms

	// Calculate position-based adjustments
	progress := float64(currentPos) / float64(totalWords)

	// Slightly longer delays at the beginning (model "thinking")
	if currentPos < 3 {
		baseDelay += rand.Intn(60) // Extra 0-60ms for initial processing
	} else if currentPos < 8 {
		baseDelay += rand.Intn(30) // Extra 0-30ms for early words
	}

	// Slightly shorter delays towards the end (model "finishing up")
	if progress > 0.8 {
		baseDelay = int(float64(baseDelay) * 0.7) // Reduce by 30%
	} else if progress > 0.6 {
		baseDelay = int(float64(baseDelay) * 0.85) // Reduce by 15%
	}

	// Ensure delay is within bounds
	if baseDelay < minDelay {
		baseDelay = minDelay
	} else if baseDelay > maxDelay {
		baseDelay = maxDelay
	}

	// Add small random jitter for naturalness
	jitter := rand.Intn(20) - 10 // ±10ms jitter
	baseDelay += jitter

	if baseDelay < minDelay {
		baseDelay = minDelay
	}

	return time.Duration(baseDelay) * time.Millisecond
}
