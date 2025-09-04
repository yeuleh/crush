package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

func TestCustomGeminiSimulateStreaming(t *testing.T) {
	tests := []struct {
		name        string
		extraBody   map[string]interface{}
		content     string
		toolCalls   []message.ToolCall
		wantEvents  []EventType
		wantChunks  int
	}{
		{
			name: "basic_content_simulation",
			extraBody: map[string]interface{}{
				"chunk_size":        10,
				"simulation_delay":  1, // 1ms for fast testing
				"chunking_strategy": "fixed",
			},
			content: "Hello, this is a test message for streaming simulation.",
			wantEvents: []EventType{
				EventContentStart,
				EventContentDelta,
				EventContentDelta,
				EventContentDelta,
				EventContentDelta,
				EventContentDelta,
				EventContentDelta,
				EventContentStop,
				EventComplete,
			},
			wantChunks: 6, // 55 characters / 10 = 6 chunks (rounded up)
		},
		{
			name: "word_based_chunking",
			extraBody: map[string]interface{}{
				"chunk_size":        20,
				"simulation_delay":  1,
				"chunking_strategy": "word",
			},
			content: "This is a test message with multiple words for chunking.",
			wantEvents: []EventType{
				EventContentStart,
				EventContentDelta,
				EventContentDelta,
				EventContentDelta,
				EventContentStop,
				EventComplete,
			},
		},
		{
			name: "tool_calls_simulation",
			extraBody: map[string]interface{}{
				"chunk_size":       10,
				"simulation_delay": 1,
				"tool_call_delay":  1,
			},
			content: "Response with tool call",
			toolCalls: []message.ToolCall{
				{
					ID:    "call_123",
					Name:  "test_function",
					Input: `{"param": "value"}`,
					Type:  "function",
				},
			},
			wantEvents: []EventType{
				EventContentStart,
				EventContentDelta,
				EventContentDelta,
				EventToolUseStart,
				EventToolUseDelta,
				EventToolUseStop,
				EventContentStop,
				EventComplete,
			},
		},
		{
			name: "natural_chunking_with_jitter",
			extraBody: map[string]interface{}{
				"chunk_size":        15,
				"simulation_delay":  1,
				"delay_variation":   1,
				"chunking_strategy": "natural",
				"enable_jitter":     true,
			},
			content: "This is a longer message that will be chunked using natural strategy with jitter.",
			wantEvents: []EventType{
				EventContentStart,
				EventContentDelta, // At least one delta
				EventContentStop,
				EventComplete,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create client with test configuration
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					extraBody: tt.extraBody,
				},
			}

			// Create a mock response
			response := &ProviderResponse{
				Content:   tt.content,
				ToolCalls: tt.toolCalls,
				Usage: TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
				},
			}

			// Create event channel
			eventChan := make(chan ProviderEvent, 100)

			// Mock the send method by directly calling simulateStreaming
			ctx := context.Background()
			go func() {
				defer close(eventChan)
				
				// Send initial event
				eventChan <- ProviderEvent{Type: EventContentStart}
				
				// Get simulation config
				config := client.getSimulationConfig()
				
				// Simulate content streaming
				if response.Content != "" {
					if err := client.simulateContentStreaming(ctx, response.Content, config, eventChan); err != nil {
						eventChan <- ProviderEvent{Type: EventError, Error: err}
						return
					}
				}
				
				// Simulate tool calls
				if err := client.simulateToolCallStreaming(ctx, response.ToolCalls, config, eventChan); err != nil {
					eventChan <- ProviderEvent{Type: EventError, Error: err}
					return
				}
				
				// Send final events
				eventChan <- ProviderEvent{Type: EventContentStop}
				eventChan <- ProviderEvent{
					Type:     EventComplete,
					Response: response,
				}
			}()

			// Collect events
			var events []ProviderEvent
			var contentDeltas []string
			
			for event := range eventChan {
				events = append(events, event)
				if event.Type == EventContentDelta {
					contentDeltas = append(contentDeltas, event.Content)
				}
			}

			// Verify event sequence
			if len(events) < len(tt.wantEvents) {
				t.Errorf("Expected at least %d events, got %d", len(tt.wantEvents), len(events))
			}

			// Check that required events are present
			eventTypes := make([]EventType, len(events))
			for i, event := range events {
				eventTypes[i] = event.Type
			}

			for _, wantEvent := range tt.wantEvents {
				found := false
				for _, gotEvent := range eventTypes {
					if gotEvent == wantEvent {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected event %v not found in %v", wantEvent, eventTypes)
				}
			}

			// Verify content reconstruction
			if tt.content != "" {
				reconstructed := strings.Join(contentDeltas, "")
				if reconstructed != tt.content {
					t.Errorf("Content reconstruction failed:\nExpected: %q\nGot: %q", tt.content, reconstructed)
				}
			}

			// Verify chunk count if specified
			if tt.wantChunks > 0 {
				deltaCount := 0
				for _, event := range events {
					if event.Type == EventContentDelta {
						deltaCount++
					}
				}
				if deltaCount != tt.wantChunks {
					t.Errorf("Expected %d content deltas, got %d", tt.wantChunks, deltaCount)
				}
			}

			// Verify tool call events if tool calls are present
			if len(tt.toolCalls) > 0 {
				toolStartCount := 0
				toolDeltaCount := 0
				toolStopCount := 0
				
				for _, event := range events {
					switch event.Type {
					case EventToolUseStart:
						toolStartCount++
					case EventToolUseDelta:
						toolDeltaCount++
					case EventToolUseStop:
						toolStopCount++
					}
				}
				
				expectedToolEvents := len(tt.toolCalls)
				if toolStartCount != expectedToolEvents {
					t.Errorf("Expected %d tool start events, got %d", expectedToolEvents, toolStartCount)
				}
				if toolDeltaCount != expectedToolEvents {
					t.Errorf("Expected %d tool delta events, got %d", expectedToolEvents, toolDeltaCount)
				}
				if toolStopCount != expectedToolEvents {
					t.Errorf("Expected %d tool stop events, got %d", expectedToolEvents, toolStopCount)
				}
			}
		})
	}
}

func TestCustomGeminiChunkingStrategies(t *testing.T) {
	client := &customGeminiClient{}
	
	tests := []struct {
		name     string
		content  string
		config   simulationConfig
		wantMin  int // Minimum expected chunks
		wantMax  int // Maximum expected chunks
	}{
		{
			name:    "fixed_chunking",
			content: "Hello world test message",
			config: simulationConfig{
				chunkSize:        5,
				chunkingStrategy: "fixed",
			},
			wantMin: 4,
			wantMax: 5,
		},
		{
			name:    "word_chunking",
			content: "Hello world test message for word chunking",
			config: simulationConfig{
				chunkSize:        15,
				chunkingStrategy: "word",
			},
			wantMin: 2,
			wantMax: 4,
		},
		{
			name:    "sentence_chunking",
			content: "First sentence. Second sentence! Third sentence?",
			config: simulationConfig{
				chunkSize:        20,
				chunkingStrategy: "sentence",
			},
			wantMin: 2,
			wantMax: 3,
		},
		{
			name:    "natural_chunking",
			content: "This is a natural chunking test with variable sizes",
			config: simulationConfig{
				chunkSize:        10,
				chunkingStrategy: "natural",
			},
			wantMin: 3,
			wantMax: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := client.chunkContent(tt.content, tt.config)
			
			if len(chunks) < tt.wantMin || len(chunks) > tt.wantMax {
				t.Errorf("Expected %d-%d chunks, got %d", tt.wantMin, tt.wantMax, len(chunks))
			}
			
			// Verify content reconstruction
			reconstructed := strings.Join(chunks, "")
			if reconstructed != tt.content {
				t.Errorf("Content reconstruction failed:\nExpected: %q\nGot: %q", tt.content, reconstructed)
			}
			
			// Verify no empty chunks
			for i, chunk := range chunks {
				if chunk == "" {
					t.Errorf("Empty chunk at index %d", i)
				}
			}
		})
	}
}

func TestCustomGeminiSimulationConfig(t *testing.T) {
	tests := []struct {
		name      string
		extraBody map[string]interface{}
		want      simulationConfig
	}{
		{
			name: "default_config",
			extraBody: map[string]interface{}{},
			want: simulationConfig{
				chunkSize:        20,
				baseDelay:        20 * time.Millisecond,
				delayVariation:   5 * time.Millisecond,
				chunkingStrategy: "natural",
				toolCallDelay:    10 * time.Millisecond,
				enableJitter:     true,
			},
		},
		{
			name: "custom_config",
			extraBody: map[string]interface{}{
				"chunk_size":        30,
				"simulation_delay":  50,
				"delay_variation":   10,
				"chunking_strategy": "word",
				"tool_call_delay":   25,
				"enable_jitter":     false,
			},
			want: simulationConfig{
				chunkSize:        30,
				baseDelay:        50 * time.Millisecond,
				delayVariation:   10 * time.Millisecond,
				chunkingStrategy: "word",
				toolCallDelay:    25 * time.Millisecond,
				enableJitter:     false,
			},
		},
		{
			name: "float_values",
			extraBody: map[string]interface{}{
				"chunk_size":       15.5,
				"simulation_delay": 30.7,
				"delay_variation":  7.3,
				"tool_call_delay":  12.8,
			},
			want: simulationConfig{
				chunkSize:        15,
				baseDelay:        30 * time.Millisecond,
				delayVariation:   7 * time.Millisecond,
				chunkingStrategy: "natural",
				toolCallDelay:    12 * time.Millisecond,
				enableJitter:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					extraBody: tt.extraBody,
				},
			}

			got := client.getSimulationConfig()

			if got.chunkSize != tt.want.chunkSize {
				t.Errorf("chunkSize = %d, want %d", got.chunkSize, tt.want.chunkSize)
			}
			if got.baseDelay != tt.want.baseDelay {
				t.Errorf("baseDelay = %v, want %v", got.baseDelay, tt.want.baseDelay)
			}
			if got.delayVariation != tt.want.delayVariation {
				t.Errorf("delayVariation = %v, want %v", got.delayVariation, tt.want.delayVariation)
			}
			if got.chunkingStrategy != tt.want.chunkingStrategy {
				t.Errorf("chunkingStrategy = %q, want %q", got.chunkingStrategy, tt.want.chunkingStrategy)
			}
			if got.toolCallDelay != tt.want.toolCallDelay {
				t.Errorf("toolCallDelay = %v, want %v", got.toolCallDelay, tt.want.toolCallDelay)
			}
			if got.enableJitter != tt.want.enableJitter {
				t.Errorf("enableJitter = %v, want %v", got.enableJitter, tt.want.enableJitter)
			}
		})
	}
}