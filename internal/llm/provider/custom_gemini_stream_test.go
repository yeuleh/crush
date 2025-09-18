package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

func TestCustomGeminiClient_StreamStandard(t *testing.T) {
	tests := []struct {
		name           string
		sseResponse    string
		expectedEvents []ProviderEvent
		expectError    bool
	}{
		{
			name: "simple text streaming",
			sseResponse: `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}

data: [DONE]

`,
			expectedEvents: []ProviderEvent{
				{Type: EventContentStart},
				{Type: EventContentDelta, Content: "Hello"},
				{Type: EventContentDelta, Content: " world"},
				{Type: EventComplete, Response: &ProviderResponse{
					Content:      "",
					ToolCalls:    []message.ToolCall{},
					Usage:        TokenUsage{InputTokens: 10, OutputTokens: 5},
					FinishReason: message.FinishReasonEndTurn,
				}},
				{Type: EventContentStop},
				{Type: EventComplete, Response: &ProviderResponse{
					Content:      "Hello world",
					ToolCalls:    []message.ToolCall{},
					Usage:        TokenUsage{InputTokens: 10, OutputTokens: 5},
					FinishReason: message.FinishReasonEndTurn,
				}},
			},
		},
		{
			name: "streaming with tool call",
			sseResponse: `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"location":"New York"}}}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":8}}

data: [DONE]

`,
			expectedEvents: []ProviderEvent{
				{Type: EventContentStart},
				{Type: EventToolUseStart, ToolCall: &message.ToolCall{
					Name:     "get_weather",
					Input:    `{"location":"New York"}`,
					Type:     "function",
					Finished: false,
				}},
				{Type: EventComplete, Response: &ProviderResponse{
					Content:      "",
					ToolCalls:    []message.ToolCall{},
					Usage:        TokenUsage{InputTokens: 15, OutputTokens: 8},
					FinishReason: message.FinishReasonEndTurn,
				}},
				{Type: EventContentStop},
			},
		},
		{
			name: "empty stream with heartbeat",
			sseResponse: `data: {}

data: {"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":0}}

data: [DONE]

`,
			expectedEvents: []ProviderEvent{
				{Type: EventContentStart},
				{Type: EventComplete, Response: &ProviderResponse{
					Content:      "",
					ToolCalls:    []message.ToolCall{},
					Usage:        TokenUsage{InputTokens: 5, OutputTokens: 0},
					FinishReason: message.FinishReasonEndTurn,
				}},
				{Type: EventContentStop},
				{Type: EventComplete, Response: &ProviderResponse{
					Content:      "",
					ToolCalls:    []message.ToolCall{},
					Usage:        TokenUsage{InputTokens: 5, OutputTokens: 0},
					FinishReason: message.FinishReasonEndTurn,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request headers
				if r.Header.Get("Accept") != "text/event-stream" {
					t.Errorf("Expected Accept header 'text/event-stream', got '%s'", r.Header.Get("Accept"))
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected Content-Type header 'application/json', got '%s'", r.Header.Get("Content-Type"))
				}

				// Send SSE response
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)

				// Write response in chunks to simulate streaming
				lines := strings.Split(tt.sseResponse, "\n")
				for _, line := range lines {
					fmt.Fprintf(w, "%s\n", line)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					time.Sleep(1 * time.Millisecond) // Small delay to simulate streaming
				}
			}))
			defer server.Close()

			// Create client
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					apiKey:  "test-key",
					baseURL: server.URL,
					model: func(modelType config.SelectedModelType) catwalk.Model {
						return catwalk.Model{ID: "gemini-1.5-flash"}
					},
					modelType: config.SelectedModelTypeLarge,
				},
				httpClient: server.Client(),
				baseURL:    server.URL,
			}

			// Test streaming
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			messages := []message.Message{
				{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Hello"}}},
			}

			eventChan := client.streamStandard(ctx, messages, nil)

			// Collect events
			var events []ProviderEvent
			for event := range eventChan {
				// Remove dynamic fields for comparison
				if event.Type == EventToolUseStart && event.ToolCall != nil {
					event.ToolCall.ID = "" // Remove generated ID
				}
				events = append(events, event)
			}

			// Verify events (simplified comparison)
			if len(events) < 3 { // At least start, some content/complete, stop
				t.Errorf("Expected at least 3 events, got %d", len(events))
			}

			// Verify start event
			if events[0].Type != EventContentStart {
				t.Errorf("Expected first event to be EventContentStart, got %v", events[0].Type)
			}

			// Verify stop event is present
			hasStop := false
			for _, event := range events {
				if event.Type == EventContentStop {
					hasStop = true
					break
				}
			}
			if !hasStop {
				t.Errorf("Expected EventContentStop event")
			}

			// Verify complete event is present
			hasComplete := false
			for _, event := range events {
				if event.Type == EventComplete {
					hasComplete = true
					break
				}
			}
			if !hasComplete {
				t.Errorf("Expected EventComplete event")
			}
		})
	}
}

func TestCustomGeminiClient_ParseStreamChunk(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name        string
		data        string
		expectEvent bool
		eventType   EventType
		expectError bool
	}{
		{
			name:        "text content chunk",
			data:        `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
			expectEvent: true,
			eventType:   EventContentDelta,
		},
		{
			name:        "function call chunk",
			data:        `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"test","args":{"key":"value"}}}]}}]}`,
			expectEvent: true,
			eventType:   EventToolUseStart,
		},
		{
			name:        "finish reason chunk",
			data:        `{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}`,
			expectEvent: true,
			eventType:   EventComplete,
		},
		{
			name:        "usage metadata only",
			data:        `{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
			expectEvent: true,
			eventType:   EventComplete,
		},
		{
			name:        "empty heartbeat",
			data:        `{}`,
			expectEvent: false,
		},
		{
			name:        "invalid JSON",
			data:        `{invalid json}`,
			expectEvent: false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := client.parseStreamChunk(tt.data)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.expectEvent {
				if event == nil {
					t.Errorf("Expected event, got nil")
					return
				}
				if event.Type != tt.eventType {
					t.Errorf("Expected event type %v, got %v", tt.eventType, event.Type)
				}
			} else {
				if event != nil {
					t.Errorf("Expected no event, got %v", event)
				}
			}
		})
	}
}

// TestCustomGeminiClient_StreamSimulated tests the streaming simulation functionality
func TestCustomGeminiClient_StreamSimulated(t *testing.T) {
	tests := []struct {
		name            string
		responseData    string
		expectedContent string
		expectedEvents  int // minimum expected events
		expectError     bool
	}{
		{
			name: "simple text simulation",
			responseData: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Hello world from simulation"}]
					},
					"finishReason": "STOP"
				}],
				"usageMetadata": {
					"promptTokenCount": 10,
					"candidatesTokenCount": 6
				}
			}`,
			expectedContent: "Hello world from simulation",
			expectedEvents:  4, // start + at least 1 delta + stop + complete
		},
		{
			name: "empty content simulation",
			responseData: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": []
					},
					"finishReason": "STOP"
				}],
				"usageMetadata": {
					"promptTokenCount": 5,
					"candidatesTokenCount": 0
				}
			}`,
			expectedContent: "",
			expectedEvents:  3, // start + stop + complete
		},
		{
			name: "long content simulation",
			responseData: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "This is a longer piece of content that should be broken into multiple chunks for realistic streaming simulation behavior."}]
					},
					"finishReason": "STOP"
				}],
				"usageMetadata": {
					"promptTokenCount": 15,
					"candidatesTokenCount": 20
				}
			}`,
			expectedContent: "This is a longer piece of content that should be broken into multiple chunks for realistic streaming simulation behavior.",
			expectedEvents:  8, // start + multiple deltas + stop + complete
		},
		{
			name: "simulation with tool calls",
			responseData: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [
							{"functionCall": {"name": "get_weather", "args": {"location": "New York"}}},
							{"text": "Let me check the weather for you."}
						]
					},
					"finishReason": "STOP"
				}],
				"usageMetadata": {
					"promptTokenCount": 12,
					"candidatesTokenCount": 8
				}
			}`,
			expectedContent: "Let me check the weather for you.",
			expectedEvents:  7, // start + tool + deltas + stop + complete
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server for complete URL mode
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify this is not a streaming request
				if r.Header.Get("Accept") == "text/event-stream" {
					t.Errorf("Streaming simulation should not use SSE Accept header")
				}

				// Return complete response
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tt.responseData))
			}))
			defer server.Close()

			// Create client with complete URL mode (ending with #)
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					apiKey:  "test-key",
					baseURL: server.URL + "/complete#", // Complete URL mode
					model: func(modelType config.SelectedModelType) catwalk.Model {
						return catwalk.Model{ID: "gemini-1.5-flash"}
					},
					modelType: config.SelectedModelTypeLarge,
				},
				httpClient: server.Client(),
				baseURL:    server.URL + "/complete#",
			}

			// Test streaming simulation
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			messages := []message.Message{
				{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Test message"}}},
			}

			eventChan := client.streamSimulated(ctx, messages, nil)

			// Collect events with timeout protection
			var events []ProviderEvent
			var accumulatedContent strings.Builder

			done := make(chan bool)
			go func() {
				for event := range eventChan {
					events = append(events, event)
					if event.Type == EventContentDelta {
						accumulatedContent.WriteString(event.Content)
					}
				}
				done <- true
			}()

			// Wait for completion or timeout
			select {
			case <-done:
				// Success
			case <-time.After(4 * time.Second):
				t.Fatal("Test timed out waiting for streaming simulation to complete")
			}

			// Verify minimum number of events
			if len(events) < tt.expectedEvents {
				t.Errorf("Expected at least %d events, got %d", tt.expectedEvents, len(events))
			}

			// Verify event sequence
			if len(events) > 0 && events[0].Type != EventContentStart {
				t.Errorf("Expected first event to be EventContentStart, got %v", events[0].Type)
			}

			// Verify content stop and complete events
			hasStop, hasComplete := false, false
			for _, event := range events {
				if event.Type == EventContentStop {
					hasStop = true
				}
				if event.Type == EventComplete {
					hasComplete = true
				}
			}

			if !hasStop {
				t.Error("Expected EventContentStop event")
			}
			if !hasComplete {
				t.Error("Expected EventComplete event")
			}

			// Verify content integrity
			finalContent := accumulatedContent.String()
			if finalContent != tt.expectedContent {
				t.Errorf("Expected content '%s', got '%s'", tt.expectedContent, finalContent)
			}

			// Verify no errors occurred
			for _, event := range events {
				if event.Type == EventError {
					t.Errorf("Unexpected error event: %v", event.Error)
				}
			}
		})
	}
}

// TestCustomGeminiClient_StreamSimulatedCancellation tests context cancellation during simulation
func TestCustomGeminiClient_StreamSimulatedCancellation(t *testing.T) {
	// Create server that returns content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := `{
			"candidates": [{
				"content": {
					"role": "model",
					"parts": [{"text": "This is a very long response that should be cancelled during streaming simulation before it completes fully."}]
				},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 15,
				"candidatesTokenCount": 20
			}
		}`
		w.Write([]byte(response))
	}))
	defer server.Close()

	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:  "test-key",
			baseURL: server.URL + "/complete#",
			model: func(modelType config.SelectedModelType) catwalk.Model {
				return catwalk.Model{ID: "gemini-1.5-flash"}
			},
			modelType: config.SelectedModelTypeLarge,
		},
		httpClient: server.Client(),
		baseURL:    server.URL + "/complete#",
	}

	// Create context with short timeout to trigger cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	messages := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Test"}}},
	}

	eventChan := client.streamSimulated(ctx, messages, nil)

	// Collect events until cancellation
	var events []ProviderEvent
	for event := range eventChan {
		events = append(events, event)
		// Check if we got an error event due to cancellation
		if event.Type == EventError {
			if event.Error != context.DeadlineExceeded && !strings.Contains(event.Error.Error(), "context deadline exceeded") {
				t.Errorf("Expected context cancellation error, got: %v", event.Error)
			}
			return // Expected cancellation
		}
	}

	// If we reach here, the stream completed without cancellation
	// This is also valid if the simulation was fast enough
	t.Logf("Stream completed with %d events (cancellation may not have occurred due to fast execution)", len(events))
}

// TestCustomGeminiClient_StreamSimulatedErrors tests error handling in streaming simulation
func TestCustomGeminiClient_StreamSimulatedErrors(t *testing.T) {
	tests := []struct {
		name        string
		serverSetup func() *httptest.Server
		expectError bool
	}{
		{
			name: "HTTP error response",
			serverSetup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error": {"message": "Internal server error"}}`))
				}))
			},
			expectError: true,
		},
		{
			name: "invalid JSON response",
			serverSetup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{invalid json}`))
				}))
			},
			expectError: true,
		},
		{
			name: "network connection error",
			serverSetup: func() *httptest.Server {
				// Return server with invalid URL to simulate connection error
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// This handler won't be called due to connection error
				}))
				server.Close() // Close immediately to cause connection error
				return server
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.serverSetup()

			// Only defer close if the server isn't already closed
			if tt.name != "network connection error" {
				defer server.Close()
			}

			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					apiKey:  "test-key",
					baseURL: server.URL + "/complete#",
					model: func(modelType config.SelectedModelType) catwalk.Model {
						return catwalk.Model{ID: "gemini-1.5-flash"}
					},
					modelType: config.SelectedModelTypeLarge,
				},
				httpClient: server.Client(),
				baseURL:    server.URL + "/complete#",
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			messages := []message.Message{
				{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Test"}}},
			}

			eventChan := client.streamSimulated(ctx, messages, nil)

			// Look for error event
			gotError := false
			for event := range eventChan {
				if event.Type == EventError {
					gotError = true
					if !tt.expectError {
						t.Errorf("Unexpected error: %v", event.Error)
					}
					break
				}
			}

			if tt.expectError && !gotError {
				t.Error("Expected error event but didn't get one")
			}
		})
	}
}

// TestCustomGeminiClient_CalculateChunkSize tests the chunk size calculation
func TestCustomGeminiClient_CalculateChunkSize(t *testing.T) {
	client := &customGeminiClient{}

	// Test with different word counts
	tests := []struct {
		name        string
		totalWords  int
		currentPos  int
		expectedMin int
		expectedMax int
	}{
		{"very short response", 5, 0, 1, 1}, // Should always return 1 for short responses
		{"short response", 10, 0, 1, 1},     // Should always return 1 for short responses
		{"medium response", 20, 0, 1, 4},    // Should return 1-4
		{"long response", 100, 10, 1, 4},    // Should return 1-4
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test multiple times to check randomness and bounds
			for i := 0; i < 100; i++ {
				chunkSize := client.calculateChunkSize(tt.totalWords, tt.currentPos)
				if chunkSize < tt.expectedMin || chunkSize > tt.expectedMax {
					t.Errorf("Chunk size %d is outside expected range [%d, %d]", chunkSize, tt.expectedMin, tt.expectedMax)
				}
			}
		})
	}
}

// TestCustomGeminiClient_CalculateDelay tests the delay calculation
func TestCustomGeminiClient_CalculateDelay(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name        string
		currentPos  int
		totalWords  int
		expectedMin time.Duration
		expectedMax time.Duration
	}{
		{"beginning delay", 1, 100, 10 * time.Millisecond, 200 * time.Millisecond},
		{"middle delay", 50, 100, 10 * time.Millisecond, 200 * time.Millisecond},
		{"end delay", 90, 100, 10 * time.Millisecond, 200 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test multiple times to verify bounds
			for i := 0; i < 50; i++ {
				delay := client.calculateDelay(tt.currentPos, tt.totalWords)
				if delay < tt.expectedMin || delay > tt.expectedMax {
					t.Errorf("Delay %v is outside expected range [%v, %v]", delay, tt.expectedMin, tt.expectedMax)
				}
			}
		})
	}
}

// TestCustomGeminiClient_StreamContentChunks tests content chunking in isolation
func TestCustomGeminiClient_StreamContentChunks(t *testing.T) {
	client := &customGeminiClient{}

	tests := []struct {
		name           string
		content        string
		expectedDeltas int
		expectError    bool
	}{
		{"empty content", "", 0, false},
		{"single word", "Hello", 1, false},
		{"multiple words", "Hello world test", 3, false}, // At least 3 deltas
		{"punctuation only", "!!!", 1, false},            // Should send as single chunk
		{"long content", "This is a longer piece of content for testing chunking behavior", 10, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			eventChan := make(chan ProviderEvent, 100) // Buffered to prevent blocking

			// Run content chunking
			err := client.streamContentChunks(ctx, tt.content, eventChan)
			close(eventChan)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Count delta events and reconstruct content
			var deltaCount int
			var reconstructedContent strings.Builder

			for event := range eventChan {
				if event.Type == EventContentDelta {
					deltaCount++
					reconstructedContent.WriteString(event.Content)
				}
			}

			// Verify delta count (allow some variance for randomness)
			if tt.expectedDeltas > 0 {
				if deltaCount < 1 {
					t.Errorf("Expected at least 1 delta event, got %d", deltaCount)
				}
				if deltaCount > len(strings.Fields(tt.content))*2 {
					t.Errorf("Too many delta events: %d (content has %d words)", deltaCount, len(strings.Fields(tt.content)))
				}
			} else {
				if deltaCount != 0 {
					t.Errorf("Expected no delta events, got %d", deltaCount)
				}
			}

			// Verify content integrity
			if reconstructedContent.String() != tt.content {
				t.Errorf("Content integrity check failed. Expected '%s', got '%s'", tt.content, reconstructedContent.String())
			}
		})
	}
}

func TestCustomGeminiClient_StreamURLModeSelection(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		expectMode  URLMode
		expectError bool
	}{
		{
			name:       "standard mode URL",
			baseURL:    "https://api.example.com",
			expectMode: ModeStandard,
		},
		{
			name:       "complete URL mode",
			baseURL:    "https://api.example.com/complete#",
			expectMode: ModeFull,
		},
		{
			name:        "invalid URL with multiple #",
			baseURL:     "https://api.example.com##",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test URL mode detection only, not actual streaming
			mode, _, err := DetectURLMode(tt.baseURL)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if mode != tt.expectMode {
				t.Errorf("Expected URL mode %d, got %d", tt.expectMode, mode)
			}
		})
	}
}

func TestCustomGeminiClient_StreamContextCancellation(t *testing.T) {
	// Create a server that streams slowly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send a few chunks then delay
		fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello\"}]}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Long delay to test cancellation
		time.Sleep(2 * time.Second)

		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:  "test-key",
			baseURL: server.URL,
			model: func(modelType config.SelectedModelType) catwalk.Model {
				return catwalk.Model{ID: "gemini-1.5-flash"}
			},
			modelType: config.SelectedModelTypeLarge,
		},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	messages := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Hello"}}},
	}

	eventChan := client.streamStandard(ctx, messages, nil)

	// Collect events until channel closes or timeout
	var events []ProviderEvent
	for event := range eventChan {
		events = append(events, event)
	}

	// Should have received at least start event before cancellation
	if len(events) == 0 {
		t.Errorf("Expected at least one event before cancellation")
	}

	// First event should be content start
	if events[0].Type != EventContentStart {
		t.Errorf("Expected first event to be EventContentStart, got %v", events[0].Type)
	}
}

func TestCustomGeminiClient_StreamHTTPError(t *testing.T) {
	// Create server that returns HTTP error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"code":401,"message":"Invalid API key","status":"UNAUTHENTICATED"}}`)
	}))
	defer server.Close()

	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:  "invalid-key",
			baseURL: server.URL,
			model: func(modelType config.SelectedModelType) catwalk.Model {
				return catwalk.Model{ID: "gemini-1.5-flash"}
			},
			modelType: config.SelectedModelTypeLarge,
		},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	messages := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Hello"}}},
	}

	eventChan := client.streamStandard(ctx, messages, nil)

	// Should receive error event
	event := <-eventChan
	if event.Type != EventError {
		t.Errorf("Expected EventError, got %v", event.Type)
	}

	if event.Error == nil {
		t.Errorf("Expected error to be set")
	}

	// Channel should be closed
	select {
	case _, ok := <-eventChan:
		if ok {
			t.Errorf("Expected channel to be closed after error")
		}
	default:
		t.Errorf("Expected channel to be closed")
	}
}
