package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoggingIntegration tests the logging functionality of custom-gemini provider
func TestLoggingIntegration(t *testing.T) {
	// Set up test environment
	originalLevel := slog.SetLogLoggerLevel(slog.LevelDebug)
	defer slog.SetLogLoggerLevel(originalLevel)

	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Create mock HTTP server
	server := createMockGeminiServer(t)
	defer server.Close()

	// Create client with debug logging enabled
	client := createTestCustomGeminiClient(t, server.URL)

	// Test send operation with logging
	t.Run("send_operation_logging", func(t *testing.T) {
		logBuffer.Reset()

		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello, world!"},
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		response, err := client.send(ctx, messages, nil)
		require.NoError(t, err)
		assert.NotNil(t, response)

		// Verify logging output contains expected debug information
		logOutput := logBuffer.String()
		
		// Check for operation context logging
		assert.Contains(t, logOutput, "Operation context")
		assert.Contains(t, logOutput, "send_starting")
		assert.Contains(t, logOutput, "custom-gemini")
		
		// Check for request details logging  
		assert.Contains(t, logOutput, "HTTP request details")
		assert.Contains(t, logOutput, "application/json")
		assert.Contains(t, logOutput, "body_size_bytes")
		
		// Check for response details logging
		assert.Contains(t, logOutput, "HTTP response details")
		assert.Contains(t, logOutput, "status_code")
		
		// Check for completion logging
		assert.Contains(t, logOutput, "Custom Gemini provider send completed")
		assert.Contains(t, logOutput, "success\":true")
		
		// Verify structured logging format
		lines := strings.Split(strings.TrimSpace(logOutput), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			
			var logEntry map[string]interface{}
			err := json.Unmarshal([]byte(line), &logEntry)
			assert.NoError(t, err, "Log entry should be valid JSON: %s", line)
			
			// Verify required fields are present
			assert.Contains(t, logEntry, "time")
			assert.Contains(t, logEntry, "level")
			assert.Contains(t, logEntry, "msg")
		}
	})

	// Test stream operation with logging  
	t.Run("stream_operation_logging", func(t *testing.T) {
		logBuffer.Reset()

		messages := []message.Message{
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Stream test message"},
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		eventChan := client.stream(ctx, messages, nil)

		var events []ProviderEvent
		for event := range eventChan {
			events = append(events, event)
			if event.Type == EventComplete || event.Type == EventError {
				break
			}
		}

		// Verify we received events
		require.NotEmpty(t, events)

		logOutput := logBuffer.String()
		
		// Check for stream operation logging
		assert.Contains(t, logOutput, "Custom Gemini provider stream starting")
		assert.Contains(t, logOutput, "url_mode")
		
		// Check for streaming-specific logging based on URL mode
		if strings.Contains(logOutput, "ModeStandard") || strings.Contains(logOutput, "standard") {
			assert.Contains(t, logOutput, "SSE stream")
		} else {
			assert.Contains(t, logOutput, "Stream simulation")
		}
	})
}

// TestDebugModeToggle tests that debug logging is properly controlled by debug mode
func TestDebugModeToggle(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	server := createMockGeminiServer(t)
	defer server.Close()

	client := createTestCustomGeminiClient(t, server.URL)

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Debug test message"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("debug_mode_enabled", func(t *testing.T) {
		logBuffer.Reset()
		
		// Enable debug logging
		originalLevel := slog.SetLogLoggerLevel(slog.LevelDebug)
		defer slog.SetLogLoggerLevel(originalLevel)

		_, err := client.send(ctx, messages, nil)
		require.NoError(t, err)

		logOutput := logBuffer.String()
		
		// Should contain debug-level logs
		assert.Contains(t, logOutput, "Operation context")
		assert.Contains(t, logOutput, "HTTP request details")
		assert.Contains(t, logOutput, "Gemini request body")
	})

	t.Run("debug_mode_disabled", func(t *testing.T) {
		logBuffer.Reset()
		
		// Disable debug logging
		originalLevel := slog.SetLogLoggerLevel(slog.LevelInfo)
		defer slog.SetLogLoggerLevel(originalLevel)

		_, err := client.send(ctx, messages, nil)
		require.NoError(t, err)

		logOutput := logBuffer.String()
		
		// Should not contain debug-level logs, but should contain info-level logs
		assert.NotContains(t, logOutput, "Operation context")
		assert.NotContains(t, logOutput, "HTTP request details")
		assert.Contains(t, logOutput, "Custom Gemini provider send completed")
	})
}

// TestErrorLogging tests that errors are properly logged with context
func TestErrorLogging(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Create a server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "Internal server error"}}`))
	}))
	defer server.Close()

	client := createTestCustomGeminiClient(t, server.URL)

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Error test message"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.send(ctx, messages, nil)
	assert.Error(t, err)

	logOutput := logBuffer.String()
	
	// Should contain error logs with proper context
	assert.Contains(t, logOutput, "HTTP request returned non-OK status")
	assert.Contains(t, logOutput, "status_code\":500")
	assert.Contains(t, logOutput, "attempt")
}

// TestPerformanceMetricsLogging tests that performance metrics are logged
func TestPerformanceMetricsLogging(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	server := createMockGeminiServer(t)
	defer server.Close()

	client := createTestCustomGeminiClient(t, server.URL)

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Performance test message"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.send(ctx, messages, nil)
	require.NoError(t, err)

	logOutput := logBuffer.String()
	
	// Should contain performance-related metrics
	assert.Contains(t, logOutput, "total_duration_ms")
	assert.Contains(t, logOutput, "duration_ms")
	assert.Contains(t, logOutput, "body_size_bytes")
	assert.Contains(t, logOutput, "prompt_tokens")
	assert.Contains(t, logOutput, "completion_tokens")
	
	// Verify the metrics are numeric
	lines := strings.Split(strings.TrimSpace(logOutput), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		
		var logEntry map[string]interface{}
		err := json.Unmarshal([]byte(line), &logEntry)
		require.NoError(t, err)
		
		// Check numeric metrics have appropriate types
		if duration, exists := logEntry["total_duration_ms"]; exists {
			_, ok := duration.(float64)
			assert.True(t, ok, "total_duration_ms should be numeric")
			assert.Greater(t, duration.(float64), 0.0, "duration should be positive")
		}
		
		if bodySize, exists := logEntry["body_size_bytes"]; exists {
			_, ok := bodySize.(float64)
			assert.True(t, ok, "body_size_bytes should be numeric")
			assert.GreaterOrEqual(t, bodySize.(float64), 0.0, "body size should be non-negative")
		}
	}
}

// TestSSEStreamLogging tests SSE stream-specific logging
func TestSSEStreamLogging(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Create server with SSE streaming endpoint  
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			
			// Send a simple SSE stream
			fmt.Fprint(w, "data: ")
			json.NewEncoder(w).Encode(geminiStreamChunk{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{{Text: "Hello"}},
						},
					},
				},
			})
			fmt.Fprint(w, "\n\n")
			
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		
		// Fallback to regular response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(createMockGeminiResponse("Standard response"))
	}))
	defer server.Close()

	client := createTestCustomGeminiClient(t, server.URL)

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "SSE test message"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eventChan := client.stream(ctx, messages, nil)

	var events []ProviderEvent
	for event := range eventChan {
		events = append(events, event)
		if event.Type == EventComplete || event.Type == EventError {
			break
		}
	}

	logOutput := logBuffer.String()
	
	// Verify SSE-specific logging
	assert.Contains(t, logOutput, "SSE stream")
	assert.Contains(t, logOutput, "chunks_received")
	assert.Contains(t, logOutput, "bytes_read")
	assert.Contains(t, logOutput, "content_deltas")
}

// Helper functions

func createTestCustomGeminiClient(t *testing.T, baseURL string) *customGeminiClient {
	opts := providerClientOptions{
		baseURL:   baseURL,
		apiKey:    "test-api-key",
		modelType: "test-model",
		systemMessage: "You are a test assistant",
		maxTokens: 100,
	}

	client := newCustomGeminiClient(opts)
	geminiClient, ok := client.(*customGeminiClient)
	require.True(t, ok, "Client should be of type *customGeminiClient")
	
	return geminiClient
}

func createMockGeminiServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle streaming requests
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			
			// Send minimal SSE stream for testing
			fmt.Fprint(w, "data: ")
			json.NewEncoder(w).Encode(geminiStreamChunk{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{{Text: "Stream "}},
						},
					},
				},
			})
			fmt.Fprint(w, "\n\n")
			
			fmt.Fprint(w, "data: ")
			json.NewEncoder(w).Encode(geminiStreamChunk{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role: "model",
							Parts: []geminiPart{{Text: "response"}},
						},
					},
				},
			})
			fmt.Fprint(w, "\n\n")
			
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		
		// Handle regular requests
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		response := createMockGeminiResponse("Hello! This is a test response from the mock Gemini server.")
		json.NewEncoder(w).Encode(response)
	}))
}

func createMockGeminiResponse(content string) geminiResponse {
	return geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Role: "model", // Add the required role field
					Parts: []geminiPart{
						{Text: content},
					},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: &geminiUsage{
			PromptTokenCount:     10,
			CandidatesTokenCount: 15,
		},
	}
}

// BenchmarkLoggingOverhead tests the performance impact of logging
func BenchmarkLoggingOverhead(b *testing.B) {
	server := createMockGeminiServerForBench(b)
	defer server.Close()

	client := createTestCustomGeminiClientForBench(b, server.URL)

	messages := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Benchmark test message"},
			},
		},
	}

	ctx := context.Background()

	b.Run("with_debug_logging", func(b *testing.B) {
		// Enable debug logging
		originalLevel := slog.SetLogLoggerLevel(slog.LevelDebug)
		defer slog.SetLogLoggerLevel(originalLevel)
		
		// Use a no-op writer to avoid I/O overhead in benchmark
		logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		slog.SetDefault(logger)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := client.send(ctx, messages, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("without_debug_logging", func(b *testing.B) {
		// Disable debug logging
		originalLevel := slog.SetLogLoggerLevel(slog.LevelInfo)
		defer slog.SetLogLoggerLevel(originalLevel)

		logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
		slog.SetDefault(logger)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := client.send(ctx, messages, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Helper for benchmark - using type assertion interface{} to avoid duplicate function signatures
func createTestCustomGeminiClientForBench(b *testing.B, baseURL string) *customGeminiClient {
	opts := providerClientOptions{
		baseURL:   baseURL,
		apiKey:    "test-api-key",
		modelType: "test-model",
		systemMessage: "You are a test assistant",
		maxTokens: 100,
	}

	client := newCustomGeminiClient(opts)
	geminiClient, ok := client.(*customGeminiClient)
	if !ok {
		b.Fatal("Client should be of type *customGeminiClient")
	}
	
	return geminiClient
}

func createMockGeminiServerForBench(b *testing.B) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		response := createMockGeminiResponse("Benchmark response")
		json.NewEncoder(w).Encode(response)
	}))
}
