package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/message"
)

type customGeminiClient struct {
	providerOptions providerClientOptions
	httpClient      *http.Client
	baseURL         string
}

type CustomGeminiClient ProviderClient

func newCustomGeminiClient(opts providerClientOptions) CustomGeminiClient {
	httpClient := createCustomGeminiHTTPClient(opts)

	return &customGeminiClient{
		providerOptions: opts,
		httpClient:      httpClient,
		baseURL:         opts.baseURL,
	}
}

func createCustomGeminiHTTPClient(opts providerClientOptions) *http.Client {
	// Use debug-aware HTTP client if debug mode is enabled
	if config.Get().Options.Debug {
		return log.NewHTTPClient()
	}

	// Standard HTTP client configuration optimized for Gemini API
	return &http.Client{
		Timeout: 120 * time.Second, // Longer timeout for streaming responses
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// send implements the ProviderClient interface for non-streaming requests
func (c *customGeminiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	// Build request URL using URL resolver
	methodPath := c.buildGeminiMethodPath(c.getModelName(), "generateContent")
	requestURL, err := c.buildRequestURL(methodPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build request URL: %w", err)
	}

	// TODO: Implement actual Gemini API call in later tasks
	slog.Info("Custom Gemini provider send called",
		"messages_count", len(messages),
		"tools_count", len(tools),
		"request_url", requestURL,
		"model", c.getModelName())

	// Return a minimal mock response for now
	return &ProviderResponse{
		Content:      "Custom Gemini provider response (stub)",
		ToolCalls:    []message.ToolCall{},
		Usage:        TokenUsage{},
		FinishReason: message.FinishReasonEndTurn,
	}, nil
}

// stream implements the ProviderClient interface for streaming requests
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)

		// Build request URL using URL resolver
		methodPath := c.buildGeminiMethodPath(c.getModelName(), "streamGenerateContent")
		requestURL, err := c.buildRequestURL(methodPath)
		if err != nil {
			slog.Error("Failed to build streaming request URL", "error", err)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("failed to build request URL: %w", err)}
			return
		}

		// TODO: Implement actual streaming in later tasks
		slog.Info("Custom Gemini provider stream called",
			"messages_count", len(messages),
			"tools_count", len(tools),
			"request_url", requestURL,
			"model", c.getModelName())

		// Send minimal mock events
		eventChan <- ProviderEvent{Type: EventContentStart}
		eventChan <- ProviderEvent{
			Type:    EventContentDelta,
			Content: "Custom Gemini streaming response (stub)",
		}
		eventChan <- ProviderEvent{Type: EventContentStop}
		eventChan <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content:      "Custom Gemini streaming response (stub)",
				ToolCalls:    []message.ToolCall{},
				Usage:        TokenUsage{},
				FinishReason: message.FinishReasonEndTurn,
			},
		}
	}()

	return eventChan
}

// Model implements the ProviderClient interface
func (c *customGeminiClient) Model() catwalk.Model {
	return c.providerOptions.model(c.providerOptions.modelType)
}

// buildRequestURL constructs the full request URL for a given Gemini API method
func (c *customGeminiClient) buildRequestURL(methodPath string) (string, error) {
	return ResolveGeminiURL(c.baseURL, methodPath)
}

// buildGeminiMethodPath constructs the method path for Gemini API endpoints
func (c *customGeminiClient) buildGeminiMethodPath(model, operation string) string {
	return fmt.Sprintf("v1beta/models/%s:%s", model, operation)
}

// getModelName extracts the model name from the provider options
func (c *customGeminiClient) getModelName() string {
	return c.Model().ID
}
