package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// streamStandard implements Server-Sent Events streaming for standard URL mode
func (c *customGeminiClient) streamStandard(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)

		// Build request URL for streaming endpoint
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
		httpReq, err := c.buildHTTPRequest(ctx, "POST", requestURL, request)
		if err != nil {
			slog.Error("Failed to build HTTP request for streaming", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build HTTP request: %w", err)}
			return
		}

		// Set Accept header for SSE
		httpReq.Header.Set("Accept", "text/event-stream")

		slog.Info("Starting SSE streaming request",
			"url", requestURL,
			"model", c.getModelName(),
			"messages_count", len(messages),
			"tools_count", len(tools))

		// Execute HTTP request
		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			slog.Error("HTTP streaming request failed", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("HTTP request failed: %w", err)}
			return
		}
		defer resp.Body.Close()

		// Check HTTP status code
		if resp.StatusCode != http.StatusOK {
			slog.Error("HTTP streaming request returned error status", "status", resp.StatusCode)
			eventChan <- ProviderEvent{Type: EventError, Error: c.handleHTTPError(resp)}
			return
		}

		// Process SSE stream
		if err := c.processSSEStream(ctx, resp.Body, eventChan); err != nil {
			slog.Error("Failed to process SSE stream", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to process SSE stream: %w", err)}
			return
		}

		slog.Info("SSE streaming completed successfully")
	}()

	return eventChan
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
// This will be implemented in Task 2.2
func (c *customGeminiClient) streamSimulated(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)

		// TODO: Implement in Task 2.2
		slog.Info("Streaming simulation not yet implemented (Task 2.2)")

		eventChan <- ProviderEvent{Type: EventContentStart}
		eventChan <- ProviderEvent{
			Type:    EventContentDelta,
			Content: "Streaming simulation placeholder (Task 2.2)",
		}
		eventChan <- ProviderEvent{Type: EventContentStop}
		eventChan <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content:      "Streaming simulation placeholder (Task 2.2)",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{},
				FinishReason: message.FinishReasonEndTurn,
			},
		}
	}()

	return eventChan
}
