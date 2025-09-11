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
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					apiKey: "test-key",
					model: func(modelType config.SelectedModelType) catwalk.Model {
						return catwalk.Model{ID: "gemini-1.5-flash"}
					},
					modelType: config.SelectedModelTypeLarge,
				},
				httpClient: &http.Client{},
				baseURL:    tt.baseURL,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			messages := []message.Message{
				{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Test"}}},
			}

			eventChan := client.stream(ctx, messages, nil)

			// Read first event to check for errors
			select {
			case event := <-eventChan:
				if tt.expectError {
					if event.Type != EventError {
						t.Errorf("Expected error event, got %v", event.Type)
					}
				} else {
					if event.Type == EventError {
						t.Errorf("Unexpected error: %v", event.Error)
					}
				}
			case <-ctx.Done():
				if !tt.expectError {
					t.Errorf("Expected events, but context timed out")
				}
			}

			// Drain remaining events
			for range eventChan {
				// Consume all events
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
