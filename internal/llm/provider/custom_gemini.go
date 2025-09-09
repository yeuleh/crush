package provider

import (
	"context"
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
}

type CustomGeminiClient ProviderClient

func newCustomGeminiClient(opts providerClientOptions) CustomGeminiClient {
	httpClient := createCustomGeminiHTTPClient(opts)

	return &customGeminiClient{
		providerOptions: opts,
		httpClient:      httpClient,
	}
}

func createCustomGeminiHTTPClient(opts providerClientOptions) *http.Client {
	// Use debug-aware HTTP client if debug mode is enabled
	if config.Get().Options.Debug {
		return log.NewHTTPClient()
	}

	// Standard HTTP client configuration
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// send implements the ProviderClient interface for non-streaming requests
func (c *customGeminiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	// TODO: Implement actual Gemini API call in later tasks
	slog.Info("Custom Gemini provider send called (stub)", "messages_count", len(messages), "tools_count", len(tools))

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
	// TODO: Implement actual streaming in later tasks
	eventChan := make(chan ProviderEvent)

	go func() {
		defer close(eventChan)

		slog.Info("Custom Gemini provider stream called (stub)", "messages_count", len(messages), "tools_count", len(tools))

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
