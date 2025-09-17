package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestIsCustomEndpointURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://api.openai.com/v1", false},
		{"https://api.openai.com/v1/", false},
		{"https://my.custom.domain/custom/path", false},
		{"https://my.custom.domain/custom/path#", true},
		{"https://openrouter.ai/api/v1/chat/completions#", true},
		{"", false},
		{"#", true},
	}

	for _, test := range tests {
		result := IsCustomEndpointURL(test.url)
		if result != test.expected {
			t.Errorf("IsCustomEndpointURL(%q) = %v, expected %v", test.url, result, test.expected)
		}
	}
}

func TestGetCleanEndpointURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1"},
		{"https://my.custom.domain/custom/path#", "https://my.custom.domain/custom/path"},
		{"https://openrouter.ai/api/v1/chat/completions#", "https://openrouter.ai/api/v1/chat/completions"},
		{"", ""},
		{"#", ""},
	}

	for _, test := range tests {
		result := GetCleanEndpointURL(test.url)
		if result != test.expected {
			t.Errorf("GetCleanEndpointURL(%q) = %q, expected %q", test.url, result, test.expected)
		}
	}
}

func TestGetEffectiveBaseURLForOpenAI(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1"},
		{"https://my.custom.domain/custom/path", "https://my.custom.domain/custom/path"},
		{"https://my.custom.domain/custom/path#", "https://api.openai.com/v1"},
		{"https://openrouter.ai/api/v1/chat/completions#", "https://api.openai.com/v1"},
		{"", ""},
	}

	for _, test := range tests {
		result := GetEffectiveBaseURLForOpenAI(test.url)
		if result != test.expected {
			t.Errorf("GetEffectiveBaseURLForOpenAI(%q) = %q, expected %q", test.url, result, test.expected)
		}
	}
}

// Mock HTTP client that captures requests
type testHTTPClient struct {
	requests []*http.Request
}

func (c *testHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.requests = append(c.requests, req)
	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}, nil
}

func TestCustomEndpointMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		customEndpoint string
		expectedURL    string
		expectedPath   string
	}{
		{
			name:           "Custom endpoint replacement",
			customEndpoint: "https://my.custom.domain/custom/openai/gpt#",
			expectedURL:    "https://my.custom.domain/custom/openai/gpt",
			expectedPath:   "/custom/openai/gpt",
		},
		{
			name:           "OpenRouter-style endpoint",
			customEndpoint: "https://openrouter.ai/api/v1/chat/completions#",
			expectedURL:    "https://openrouter.ai/api/v1/chat/completions",
			expectedPath:   "/api/v1/chat/completions",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &testHTTPClient{}

			client := openai.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL("https://api.openai.com/v1"), // placeholder
				option.WithMiddleware(CreateCustomEndpointMiddleware(test.customEndpoint)),
				option.WithHTTPClient(httpClient),
			)

			_, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
				Model: "gpt-4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("test"),
				},
			})

			// We expect an error due to mock response, but that's okay
			if err == nil {
				t.Errorf("Expected error due to mock response")
			}

			// Check that the request was made to the correct URL
			if len(httpClient.requests) != 1 {
				t.Fatalf("Expected 1 request, got %d", len(httpClient.requests))
			}

			req := httpClient.requests[0]
			if req.URL.String() != test.expectedURL {
				t.Errorf("Expected URL %q, got %q", test.expectedURL, req.URL.String())
			}

			if req.URL.Path != test.expectedPath {
				t.Errorf("Expected path %q, got %q", test.expectedPath, req.URL.Path)
			}
		})
	}
}

func TestCustomEndpointMiddlewareIgnoresOtherPaths(t *testing.T) {
	customEndpoint := "https://my.custom.domain/custom/openai/gpt#"

	// Create a mock request with a different path (not /v1/chat/completions)
	req, _ := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)

	middleware := CreateCustomEndpointMiddleware(customEndpoint)

	// Call middleware directly
	_, err := middleware(req, func(r *http.Request) (*http.Response, error) {
		// The URL should not be modified for non-chat-completions paths
		if r.URL.String() != "https://api.openai.com/v1/models" {
			t.Errorf("Expected URL not to be modified, but got %q", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}
