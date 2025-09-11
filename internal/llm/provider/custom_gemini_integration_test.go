package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// TestCustomGeminiClient_StreamingIntegration tests the complete streaming workflow
func TestCustomGeminiClient_StreamingIntegration(t *testing.T) {
	// Create a mock server that simulates Gemini API streaming
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify this is a streaming request
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Expected Accept header 'text/event-stream', got '%s'", r.Header.Get("Accept"))
		}

		// Verify API key is in query parameter
		if r.URL.Query().Get("key") != "test-api-key" {
			t.Errorf("Expected API key 'test-api-key' in query, got '%s'", r.URL.Query().Get("key"))
		}

		// Send SSE streaming response
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Simulate streaming response
		responses := []string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" there"}]}}]}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]}}]}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3}}`,
			`data: [DONE]`,
		}

		for _, response := range responses {
			fmt.Fprintf(w, "%s\n\n", response)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond) // Small delay to simulate streaming
		}
	}))
	defer server.Close()

	// Create client with standard mode URL
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			apiKey:  "test-api-key",
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
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Say hello"}}},
	}

	// Test streaming
	eventChan := client.stream(ctx, messages, nil)

	// Collect all events
	var events []ProviderEvent
	for event := range eventChan {
		events = append(events, event)
	}

	// Verify event sequence
	if len(events) < 5 {
		t.Fatalf("Expected at least 5 events, got %d", len(events))
	}

	// Verify start event
	if events[0].Type != EventContentStart {
		t.Errorf("Expected first event to be EventContentStart, got %v", events[0].Type)
	}

	// Verify content delta events
	contentEvents := 0
	var accumulatedContent string
	for _, event := range events {
		if event.Type == EventContentDelta {
			contentEvents++
			accumulatedContent += event.Content
		}
	}

	if contentEvents != 3 {
		t.Errorf("Expected 3 content delta events, got %d", contentEvents)
	}

	if accumulatedContent != "Hello there!" {
		t.Errorf("Expected accumulated content 'Hello there!', got '%s'", accumulatedContent)
	}

	// Verify stop event
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

	// Verify complete event with final response
	hasComplete := false
	for _, event := range events {
		if event.Type == EventComplete && event.Response != nil {
			hasComplete = true
			if event.Response.Content != "Hello there!" {
				t.Errorf("Expected final content 'Hello there!', got '%s'", event.Response.Content)
			}
			if event.Response.Usage.InputTokens != 5 {
				t.Errorf("Expected 5 input tokens, got %d", event.Response.Usage.InputTokens)
			}
			if event.Response.Usage.OutputTokens != 3 {
				t.Errorf("Expected 3 output tokens, got %d", event.Response.Usage.OutputTokens)
			}
			if event.Response.FinishReason != message.FinishReasonEndTurn {
				t.Errorf("Expected FinishReasonEndTurn, got %v", event.Response.FinishReason)
			}
			break
		}
	}
	if !hasComplete {
		t.Errorf("Expected EventComplete event with response")
	}
}

// TestCustomGeminiClient_StreamingModeSelection tests URL mode detection for streaming
func TestCustomGeminiClient_StreamingModeSelection(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		expectMode  URLMode
		expectError bool
	}{
		{
			name:       "standard mode streaming",
			baseURL:    "https://api.example.com",
			expectMode: ModeStandard,
		},
		{
			name:       "complete URL mode streaming",
			baseURL:    "https://api.example.com/stream#",
			expectMode: ModeFull,
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
				httpClient: &http.Client{Timeout: 1 * time.Second},
				baseURL:    tt.baseURL,
			}

			// Detect URL mode
			mode, _, err := DetectURLMode(tt.baseURL)
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

			if mode != tt.expectMode {
				t.Errorf("Expected mode %v, got %v", tt.expectMode, mode)
			}

			// Test that streaming method selection works
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			messages := []message.Message{
				{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "Test"}}},
			}

			eventChan := client.stream(ctx, messages, nil)

			// Just verify we get some events (will be errors due to network, but that's expected)
			eventCount := 0
			for range eventChan {
				eventCount++
				if eventCount > 10 { // Prevent infinite loop
					break
				}
			}

			if eventCount == 0 {
				t.Errorf("Expected at least one event")
			}
		})
	}
}
