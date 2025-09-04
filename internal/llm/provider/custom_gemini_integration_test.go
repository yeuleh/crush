package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// TestCustomGeminiIntegration tests the custom Gemini provider with the actual API
// This test requires a valid GEMINI_API_KEY environment variable
func TestCustomGeminiIntegration(t *testing.T) {
	// Skip if no API key is provided
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY environment variable not set, skipping integration test")
	}

	// Create a custom Gemini client with the free Gemini 2.5 Flash Experimental model
	opts := providerClientOptions{
		baseURL:      "https://generativelanguage.googleapis.com",
		apiKey:       apiKey,
		modelType:    config.SelectedModelTypeLarge,
		extraHeaders: make(map[string]string),
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{
				ID:                "gemini-2.5-flash-exp",
				Name:              "Gemini 2.5 Flash Experimental",
				ContextWindow:     1048576, // 1M tokens context
				DefaultMaxTokens:  8192,
			}
		},
	}

	client := newCustomGeminiClient(opts)

	// Test basic message conversion
	t.Run("MessageConversion", func(t *testing.T) {
		messages := []message.Message{
			{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: "You are a helpful assistant."},
				},
			},
			{
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "Hello! Please respond with just 'Hi there!' and nothing else."},
				},
			},
		}

		customClient := client.(*customGeminiClient)
		contents, systemInstruction, err := customClient.convertMessagesToGemini(messages)
		if err != nil {
			t.Fatalf("Failed to convert messages: %v", err)
		}

		// Check system instruction
		if systemInstruction == nil {
			t.Error("Expected system instruction to be set")
		} else if len(systemInstruction.Parts) == 0 || systemInstruction.Parts[0].Text != "You are a helpful assistant." {
			t.Error("System instruction not converted correctly")
		}

		// Check user message
		if len(contents) != 1 {
			t.Errorf("Expected 1 content, got %d", len(contents))
		} else {
			content := contents[0]
			if content.Role != GeminiRoleUser {
				t.Errorf("Expected role %s, got %s", GeminiRoleUser, content.Role)
			}
			if len(content.Parts) != 1 || content.Parts[0].Text != "Hello! Please respond with just 'Hi there!' and nothing else." {
				t.Error("User message not converted correctly")
			}
		}
	})

	// Test HTTP client creation
	t.Run("HTTPClientCreation", func(t *testing.T) {
		customClient := client.(*customGeminiClient)
		
		if customClient.httpClient == nil {
			t.Error("HTTP client should not be nil")
		}

		if customClient.baseURL != "https://generativelanguage.googleapis.com" {
			t.Errorf("Expected base URL to be https://generativelanguage.googleapis.com, got %s", customClient.baseURL)
		}

		if customClient.apiKey != apiKey {
			t.Error("API key not set correctly")
		}
	})

	// Test request building
	t.Run("RequestBuilding", func(t *testing.T) {
		customClient := client.(*customGeminiClient)
		
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		testBody := []byte(`{"test": "data"}`)
		req, err := customClient.buildRequest(ctx, "POST", "https://example.com/test", testBody)
		if err != nil {
			t.Fatalf("Failed to build request: %v", err)
		}

		// Check that API key is in query parameters
		if req.URL.Query().Get("key") != apiKey {
			t.Error("API key not set in query parameters")
		}

		// Check headers
		if req.Header.Get("Content-Type") != "application/json" {
			t.Error("Content-Type header not set correctly")
		}

		if req.Header.Get("User-Agent") != "CustomGeminiClient/1.0" {
			t.Error("User-Agent header not set correctly")
		}
	})

	// Test actual API call (if we want to test with real API)
	t.Run("APIConnection", func(t *testing.T) {
		// This is a basic connectivity test - we'll just test that we can build
		// a proper request URL and headers without actually making the call
		customClient := client.(*customGeminiClient)
		
		// Build a test request to the models endpoint to verify connectivity setup
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := customClient.baseURL + "/v1beta/models"
		req, err := customClient.buildRequest(ctx, "GET", url, nil)
		if err != nil {
			t.Fatalf("Failed to build API request: %v", err)
		}

		// Verify the request is properly formed
		expectedURL := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
		if req.URL.String() != expectedURL {
			t.Errorf("Expected URL %s, got %s", expectedURL, req.URL.String())
		}

		// Note: We're not actually making the HTTP call here to avoid hitting the API
		// in automated tests. For manual testing, you could uncomment the following:
		/*
		resp, err := customClient.httpClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to make API request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		*/
	})
}

// TestCustomGeminiWithoutAPIKey tests that the client handles missing API key gracefully
func TestCustomGeminiWithoutAPIKey(t *testing.T) {
	opts := providerClientOptions{
		baseURL:      "https://generativelanguage.googleapis.com",
		apiKey:       "", // No API key
		modelType:    config.SelectedModelTypeLarge,
		extraHeaders: make(map[string]string),
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{
				ID:                "gemini-2.5-flash-exp",
				Name:              "Gemini 2.5 Flash Experimental",
				ContextWindow:     1048576,
				DefaultMaxTokens:  8192,
			}
		},
	}

	client := newCustomGeminiClient(opts)
	customClient := client.(*customGeminiClient)

	// Test that request building works even without API key
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := customClient.buildRequest(ctx, "GET", "https://example.com/test", nil)
	if err != nil {
		t.Fatalf("Failed to build request: %v", err)
	}

	// Should not have API key in query parameters
	if req.URL.Query().Get("key") != "" {
		t.Error("API key should not be set when not provided")
	}
}

// TestRetryConfiguration tests the retry logic
func TestRetryConfiguration(t *testing.T) {
	opts := providerClientOptions{
		baseURL:      "https://generativelanguage.googleapis.com",
		apiKey:       "test-key",
		modelType:    config.SelectedModelTypeLarge,
		extraHeaders: make(map[string]string),
		model: func(config.SelectedModelType) catwalk.Model {
			return catwalk.Model{
				ID:                "gemini-2.5-flash-exp",
				ContextWindow:     1048576,
				DefaultMaxTokens:  8192,
			}
		},
	}

	client := newCustomGeminiClient(opts)
	customClient := client.(*customGeminiClient)

	// Test retry logic for different scenarios
	testCases := []struct {
		name         string
		attempts     int
		statusCode   int
		shouldRetry  bool
	}{
		{"First attempt with 429", 0, 429, true},
		{"Second attempt with 500", 1, 500, true},
		{"Third attempt with 502", 2, 502, true},
		{"Fourth attempt (max reached)", 3, 500, false},
		{"Success status", 0, 200, false},
		{"Client error", 0, 400, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			shouldRetry, delay := customClient.shouldRetry(tc.attempts, nil, tc.statusCode)
			if shouldRetry != tc.shouldRetry {
				t.Errorf("Expected shouldRetry=%v, got %v", tc.shouldRetry, shouldRetry)
			}
			if shouldRetry && delay <= 0 {
				t.Error("Expected positive delay when retrying")
			}
		})
	}
}