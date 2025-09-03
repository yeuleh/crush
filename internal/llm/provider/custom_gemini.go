package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/llm/tools"
	"github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/message"
)

// CustomGeminiClient implements ProviderClient interface for direct HTTP API calls to Gemini
type customGeminiClient struct {
	providerOptions    providerClientOptions
	httpClient         *http.Client
	baseURL            string
	apiKey             string
	streamingSupported bool // cached streaming support status
	streamingChecked   bool // whether streaming support has been checked
	mu                 sync.RWMutex // protects concurrent access to streaming fields
}

// CustomGeminiClient type alias for ProviderClient interface
type CustomGeminiClient ProviderClient

// newCustomGeminiClient creates a new CustomGeminiClient instance
func newCustomGeminiClient(opts providerClientOptions) CustomGeminiClient {
	baseURL := opts.baseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}

	return &customGeminiClient{
		providerOptions:    opts,
		httpClient:         createHTTPClient(opts),
		baseURL:            baseURL,
		apiKey:             opts.apiKey,
		streamingSupported: false, // will be detected on first use
		streamingChecked:   false,
	}
}

// createHTTPClient creates an HTTP client with proxy support, debug logging, and proper timeouts
func createHTTPClient(opts providerClientOptions) *http.Client {
	// Create base transport with proxy support and optimized settings
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment, // Support system proxy
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ResponseHeaderTimeout: 30 * time.Second, // Timeout for reading response headers
		ExpectContinueTimeout: 1 * time.Second,  // Timeout for 100-continue responses
	}

	// Use debug HTTP client if debug mode is enabled
	if config.Get().Options.Debug {
		httpClient := log.NewHTTPClient()
		// Override transport to add proxy support and optimized settings
		if logger, ok := httpClient.Transport.(*log.HTTPRoundTripLogger); ok {
			logger.Transport = transport
		}
		httpClient.Timeout = 60 * time.Second
		return httpClient
	}

	// Create standard HTTP client with optimized settings
	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
}

// send implements the ProviderClient interface for non-streaming requests
func (c *customGeminiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	// TODO: Implement non-streaming request to Gemini API
	// This will be implemented in task 6
	return nil, nil
}

// stream implements the ProviderClient interface for streaming requests
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	// TODO: Implement streaming request with fallback to simulation
	// This will be implemented in tasks 7, 8, and 9
	eventChan := make(chan ProviderEvent)
	close(eventChan)
	return eventChan
}

// Model returns the model configuration for this client
func (c *customGeminiClient) Model() catwalk.Model {
	return c.providerOptions.model(c.providerOptions.modelType)
}

// buildRequest creates an HTTP request with proper authentication and headers
func (c *customGeminiClient) buildRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}

	// Set request body if provided
	if body != nil {
		req.Body = http.NoBody
		if len(body) > 0 {
			req.Body = io.NopCloser(strings.NewReader(string(body)))
			req.ContentLength = int64(len(body))
		}
	}

	// Set authentication and standard headers
	c.setRequestHeaders(req)

	return req, nil
}

// setRequestHeaders sets authentication and standard headers for requests
func (c *customGeminiClient) setRequestHeaders(req *http.Request) {
	// Set API key authentication
	if c.apiKey != "" {
		// For Gemini API, we can use either query parameter or header
		// Using query parameter as it's the standard for Gemini API
		q := req.URL.Query()
		q.Set("key", c.apiKey)
		req.URL.RawQuery = q.Encode()
	}

	// Set standard headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CustomGeminiClient/1.0")

	// Add extra headers from provider configuration
	for key, value := range c.providerOptions.extraHeaders {
		req.Header.Set(key, value)
	}
}

// setStreamingHeaders sets headers specific to streaming requests
func (c *customGeminiClient) setStreamingHeaders(req *http.Request) {
	c.setRequestHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
}

// RetryConfig holds configuration for retry behavior
type RetryConfig struct {
	MaxRetries    int
	BaseDelay     time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	JitterFactor  float64
}

// defaultRetryConfig returns the default retry configuration
func defaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,
		BaseDelay:     1 * time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		JitterFactor:  0.1,
	}
}

// shouldRetry determines if a request should be retried based on the error and attempt count
func (c *customGeminiClient) shouldRetry(attempts int, err error, statusCode int) (bool, time.Duration) {
	config := defaultRetryConfig()
	
	if attempts >= config.MaxRetries {
		return false, 0
	}

	// Check if error is retryable
	if isRetryableError(err, statusCode) {
		delay := calculateBackoffDelay(attempts, config)
		return true, delay
	}

	return false, 0
}

// isRetryableError checks if an error is retryable
func isRetryableError(err error, statusCode int) bool {
	if err != nil {
		errStr := strings.ToLower(err.Error())
		// Network errors that are typically temporary
		if strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "temporary failure") {
			return true
		}
	}

	// HTTP status codes that are retryable
	switch statusCode {
	case 429: // Too Many Requests (rate limiting)
		return true
	case 500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// calculateBackoffDelay calculates the delay for exponential backoff with jitter
func calculateBackoffDelay(attempt int, config RetryConfig) time.Duration {
	// Calculate exponential backoff
	delay := float64(config.BaseDelay) * math.Pow(config.BackoffFactor, float64(attempt))
	
	// Apply maximum delay limit
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	// Add jitter to avoid thundering herd
	jitter := delay * config.JitterFactor * (rand.Float64()*2 - 1) // Random between -jitter and +jitter
	delay += jitter

	// Ensure delay is not negative
	if delay < 0 {
		delay = float64(config.BaseDelay)
	}

	return time.Duration(delay)
}

// Gemini API Data Structures

// GeminiRequest represents the request payload for Gemini API
type GeminiRequest struct {
	Contents          []GeminiContent        `json:"contents"`
	Tools             []GeminiTool           `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig      `json:"toolConfig,omitempty"`
	SafetySettings    []GeminiSafetySetting  `json:"safetySettings,omitempty"`
	SystemInstruction *GeminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

// GeminiResponse represents the response from Gemini API
type GeminiResponse struct {
	Candidates    []GeminiCandidate     `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata  `json:"usageMetadata,omitempty"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback,omitempty"`
}

// GeminiContent represents content with parts and role
type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

// GeminiPart represents a part of content (text, inline data, function call, etc.)
type GeminiPart struct {
	Text             string                   `json:"text,omitempty"`
	InlineData       *GeminiInlineData        `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall      `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse  `json:"functionResponse,omitempty"`
}

// GeminiInlineData represents inline binary data (e.g., images)
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// GeminiFunctionCall represents a function call in the request/response
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// GeminiFunctionResponse represents a function response
type GeminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// GeminiCandidate represents a response candidate
type GeminiCandidate struct {
	Content       *GeminiContent         `json:"content"`
	FinishReason  string                 `json:"finishReason"`
	Index         int                    `json:"index"`
	SafetyRatings []GeminiSafetyRating   `json:"safetyRatings,omitempty"`
}

// GeminiTool represents tool definitions for function calling
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations"`
}

// GeminiFunctionDeclaration represents a function declaration for tools
type GeminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// GeminiToolConfig represents tool configuration
type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// GeminiFunctionCallingConfig represents function calling configuration
type GeminiFunctionCallingConfig struct {
	Mode                string   `json:"mode,omitempty"` // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GeminiSafetySetting represents safety settings for content filtering
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiSafetyRating represents safety rating in response
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// GeminiPromptFeedback represents feedback about the prompt
type GeminiPromptFeedback struct {
	BlockReason   string               `json:"blockReason,omitempty"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
}

// GeminiGenerationConfig represents generation configuration
type GeminiGenerationConfig struct {
	StopSequences   []string `json:"stopSequences,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
}

// GeminiUsageMetadata represents token usage statistics
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// GeminiError represents error response from Gemini API
type GeminiError struct {
	Error GeminiErrorDetails `json:"error"`
}

// GeminiErrorDetails represents detailed error information
type GeminiErrorDetails struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// GeminiStreamResponse represents a streaming response chunk
type GeminiStreamResponse struct {
	Candidates    []GeminiCandidate     `json:"candidates,omitempty"`
	UsageMetadata *GeminiUsageMetadata  `json:"usageMetadata,omitempty"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback,omitempty"`
}

// Message Format Converters

// convertMessagesToGemini converts internal messages to Gemini API format
func (c *customGeminiClient) convertMessagesToGemini(messages []message.Message) ([]GeminiContent, *GeminiContent, error) {
	var contents []GeminiContent
	var systemInstruction *GeminiContent

	for _, msg := range messages {
		switch msg.Role {
		case message.System:
			// System messages become system instruction
			if systemInstruction == nil {
				systemInstruction = &GeminiContent{}
			}
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert system message parts: %w", err)
			}
			systemInstruction.Parts = append(systemInstruction.Parts, parts...)

		case message.User:
			// User messages
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert user message parts: %w", err)
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  GeminiRoleUser,
					Parts: parts,
				})
			}

		case message.Assistant:
			// Assistant messages
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert assistant message parts: %w", err)
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  GeminiRoleModel,
					Parts: parts,
				})
			}

		case message.Tool:
			// Tool result messages - these should be combined with the previous assistant message
			// or added as a separate user message containing function responses
			parts, err := c.convertPartsToGemini(msg.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert tool message parts: %w", err)
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  GeminiRoleUser,
					Parts: parts,
				})
			}
		}
	}

	return contents, systemInstruction, nil
}

// convertPartsToGemini converts internal message parts to Gemini API parts
func (c *customGeminiClient) convertPartsToGemini(parts []message.ContentPart) ([]GeminiPart, error) {
	var geminiParts []GeminiPart

	for _, part := range parts {
		switch p := part.(type) {
		case message.TextContent:
			// Text content
			if p.Text != "" {
				geminiParts = append(geminiParts, GeminiPart{
					Text: p.Text,
				})
			}

		case message.ReasoningContent:
			// Reasoning content - convert thinking to text
			if p.Thinking != "" {
				geminiParts = append(geminiParts, GeminiPart{
					Text: p.Thinking,
				})
			}

		case message.BinaryContent:
			// Binary content (images) - convert to inline data
			if len(p.Data) > 0 {
				encodedData := base64.StdEncoding.EncodeToString(p.Data)
				geminiParts = append(geminiParts, GeminiPart{
					InlineData: &GeminiInlineData{
						MimeType: p.MIMEType,
						Data:     encodedData,
					},
				})
			}

		case message.ImageURLContent:
			// Image URL content - we can't directly use URLs in Gemini API
			// This would need to be downloaded and converted to binary content
			// For now, we'll skip it or could add a text description
			// TODO: Consider downloading the image and converting to binary

		case message.ToolCall:
			// Tool calls - convert to function calls
			if p.Name != "" {
				args := make(map[string]interface{})
				if p.Input != "" {
					// Parse the input JSON into args
					if err := json.Unmarshal([]byte(p.Input), &args); err != nil {
						// If parsing fails, treat as a single string argument
						args["input"] = p.Input
					}
				}
				geminiParts = append(geminiParts, GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: p.Name,
						Args: args,
					},
				})
			}

		case message.ToolResult:
			// Tool results - convert to function responses
			if p.Name != "" {
				response := make(map[string]interface{})
				response["content"] = p.Content
				if p.Metadata != "" {
					// Try to parse metadata as JSON, otherwise include as string
					var metadata interface{}
					if err := json.Unmarshal([]byte(p.Metadata), &metadata); err == nil {
						response["metadata"] = metadata
					} else {
						response["metadata"] = p.Metadata
					}
				}
				if p.IsError {
					response["is_error"] = true
				}

				geminiParts = append(geminiParts, GeminiPart{
					FunctionResponse: &GeminiFunctionResponse{
						Name:     p.Name,
						Response: response,
					},
				})
			}

		case message.Finish:
			// Finish parts don't need to be converted to Gemini API format
			// They are internal metadata
			continue

		default:
			// Unknown part type - skip it
			continue
		}
	}

	return geminiParts, nil
}

// convertToolsToGemini converts internal tools to Gemini API tool format
func (c *customGeminiClient) convertToolsToGemini(tools []tools.BaseTool) ([]GeminiTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	var functionDeclarations []GeminiFunctionDeclaration

	for _, tool := range tools {
		info := tool.Info()
		
		// Convert tool parameters to Gemini function declaration format
		parameters := make(map[string]interface{})
		if info.Parameters != nil {
			parameters = info.Parameters
		} else {
			// Default empty object schema
			parameters = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		functionDeclarations = append(functionDeclarations, GeminiFunctionDeclaration{
			Name:        info.Name,
			Description: info.Description,
			Parameters:  parameters,
		})
	}

	return []GeminiTool{
		{
			FunctionDeclarations: functionDeclarations,
		},
	}, nil
}

// convertGeminiResponseToInternal converts Gemini API response to internal format
func (c *customGeminiClient) convertGeminiResponseToInternal(response *GeminiResponse) (*ProviderResponse, error) {
	if len(response.Candidates) == 0 {
		return &ProviderResponse{
			Content:   "",
			ToolCalls: []message.ToolCall{},
			Usage: TokenUsage{
				InputTokens:         0,
				OutputTokens:        0,
				CacheCreationTokens: 0,
				CacheReadTokens:     0,
			},
		}, nil
	}

	// Use the first candidate
	candidate := response.Candidates[0]
	
	var content strings.Builder
	var toolCalls []message.ToolCall

	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				content.WriteString(part.Text)
			}

			if part.FunctionCall != nil {
				// Convert function call to internal tool call format
				argsJSON, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal function call args: %w", err)
				}

				toolCalls = append(toolCalls, message.ToolCall{
					ID:    generateToolCallID(), // Generate a unique ID
					Name:  part.FunctionCall.Name,
					Input: string(argsJSON),
					Type:  "function", // Default type for function calls
				})
			}
		}
	}

	// Extract token usage
	usage := TokenUsage{}
	if response.UsageMetadata != nil {
		usage.InputTokens = int64(response.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int64(response.UsageMetadata.CandidatesTokenCount)
		usage.CacheReadTokens = int64(response.UsageMetadata.CachedContentTokenCount)
	}

	return &ProviderResponse{
		Content:   content.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
	}, nil
}

// generateToolCallID generates a unique ID for tool calls
func generateToolCallID() string {
	// Generate a simple unique ID for tool calls
	// In a real implementation, you might want to use a more sophisticated ID generation
	return fmt.Sprintf("call_%d", time.Now().UnixNano())
}

// Constants for Gemini API

// Gemini API roles
const (
	GeminiRoleUser  = "user"
	GeminiRoleModel = "model"
)

// Gemini API finish reasons
const (
	GeminiFinishReasonStop         = "STOP"
	GeminiFinishReasonMaxTokens    = "MAX_TOKENS"
	GeminiFinishReasonSafety       = "SAFETY"
	GeminiFinishReasonRecitation   = "RECITATION"
	GeminiFinishReasonOther        = "OTHER"
)

// Gemini API safety categories
const (
	GeminiSafetyCategoryHarassment       = "HARM_CATEGORY_HARASSMENT"
	GeminiSafetyCategoryHateSpeech       = "HARM_CATEGORY_HATE_SPEECH"
	GeminiSafetyCategorySexuallyExplicit = "HARM_CATEGORY_SEXUALLY_EXPLICIT"
	GeminiSafetyCategoryDangerousContent = "HARM_CATEGORY_DANGEROUS_CONTENT"
)

// Gemini API safety thresholds
const (
	GeminiSafetyThresholdBlockNone         = "BLOCK_NONE"
	GeminiSafetyThresholdBlockOnlyHigh     = "BLOCK_ONLY_HIGH"
	GeminiSafetyThresholdBlockMediumAndAbove = "BLOCK_MEDIUM_AND_ABOVE"
	GeminiSafetyThresholdBlockLowAndAbove  = "BLOCK_LOW_AND_ABOVE"
)

// Gemini API function calling modes
const (
	GeminiFunctionCallingModeAuto = "AUTO"
	GeminiFunctionCallingModeAny  = "ANY"
	GeminiFunctionCallingModeNone = "NONE"
)