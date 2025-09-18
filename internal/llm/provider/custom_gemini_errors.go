package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
	
	"github.com/charmbracelet/crush/internal/config"
)

// HTTPError represents an HTTP error with detailed information
type HTTPError struct {
	StatusCode int
	Status     string
	Message    string
	Headers    http.Header
	Body       []byte
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Status)
}

// Custom-Gemini specific error types
var (
	// ErrMaxRetriesExceeded indicates maximum retry attempts have been reached
	ErrMaxRetriesExceeded = errors.New("maximum retry attempts exceeded")
	
	// ErrAPIKeyInvalid indicates API key is invalid or expired
	ErrAPIKeyInvalid = errors.New("API key is invalid or expired")
	
	// ErrRateLimitExceeded indicates rate limit has been exceeded
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	
	// ErrServerUnavailable indicates server is temporarily unavailable
	ErrServerUnavailable = errors.New("server temporarily unavailable")
	
	// ErrNetworkFailure indicates network connectivity issues
	ErrNetworkFailure = errors.New("network failure")
	
	// ErrContextCancelled indicates context was cancelled
	ErrContextCancelled = errors.New("context cancelled")
)

// RetryConfig defines retry behavior configuration
type RetryConfig struct {
	MaxRetries     int
	BaseBackoffMs  int
	MaxBackoffMs   int
	JitterPercent  float64
	RetryableErrors []int
}

// DefaultRetryConfig provides sensible default retry settings
var DefaultRetryConfig = RetryConfig{
	MaxRetries:      maxRetries,
	BaseBackoffMs:   2000,
	MaxBackoffMs:    60000,
	JitterPercent:   0.2,
	RetryableErrors: []int{429, 500, 502, 503, 504},
}

// shouldRetry determines if an error should trigger a retry attempt
func (c *customGeminiClient) shouldRetry(attempts int, err error) (bool, time.Duration, error) {
	// Check maximum attempts
	if attempts > maxRetries {
		return false, 0, fmt.Errorf("%w: %d retries", ErrMaxRetriesExceeded, maxRetries)
	}

	// Check for context cancellation
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, 0, fmt.Errorf("%w: %v", ErrContextCancelled, err)
	}

	// Handle HTTP errors
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return c.handleHTTPRetry(attempts, httpErr)
	}

	// Handle network errors
	if isNetworkError(err) {
		backoff := calculateExponentialBackoff(attempts, DefaultRetryConfig)
		slog.Warn("Network error, retrying with backoff",
			"attempt", attempts,
			"max_retries", maxRetries,
			"backoff_ms", backoff.Milliseconds(),
			"error", err.Error())
		return true, backoff, nil
	}

	// Non-retryable error
	return false, 0, err
}

// handleHTTPRetry handles HTTP-specific retry logic
func (c *customGeminiClient) handleHTTPRetry(attempts int, httpErr *HTTPError) (bool, time.Duration, error) {
	switch httpErr.StatusCode {
	case http.StatusTooManyRequests: // 429 - Rate limit exceeded
		retryAfter := c.parseRetryAfter(httpErr.Headers)
		if retryAfter > 0 {
			slog.Warn("Rate limit exceeded, using Retry-After header",
				"attempt", attempts,
				"retry_after_seconds", retryAfter.Seconds(),
				"status_code", httpErr.StatusCode)
			return true, retryAfter, nil
		}
		
		// Fall back to exponential backoff if no Retry-After header
		backoff := calculateExponentialBackoff(attempts, DefaultRetryConfig)
		slog.Warn("Rate limit exceeded, using exponential backoff",
			"attempt", attempts,
			"backoff_ms", backoff.Milliseconds(),
			"status_code", httpErr.StatusCode)
		return true, backoff, nil

	case http.StatusUnauthorized, http.StatusForbidden: // 401, 403 - Authentication errors
		// Try to refresh API key
		if err := c.refreshAPIKey(); err != nil {
			return false, 0, fmt.Errorf("%w: failed to refresh API key: %v", ErrAPIKeyInvalid, err)
		}
		
		slog.Info("API key refreshed, retrying request",
			"attempt", attempts,
			"status_code", httpErr.StatusCode)
		return true, 0, nil // No delay for auth refresh

	case http.StatusInternalServerError, // 500
		http.StatusBadGateway,              // 502
		http.StatusServiceUnavailable,      // 503
		http.StatusGatewayTimeout:          // 504
		
		backoff := calculateExponentialBackoff(attempts, DefaultRetryConfig)
		slog.Warn("Server error, retrying with exponential backoff",
			"attempt", attempts,
			"max_retries", maxRetries,
			"backoff_ms", backoff.Milliseconds(),
			"status_code", httpErr.StatusCode,
			"error", httpErr.Message)
		return true, backoff, nil

	default:
		// Non-retryable HTTP error
		slog.Error("Non-retryable HTTP error",
			"status_code", httpErr.StatusCode,
			"error", httpErr.Message)
		return false, 0, httpErr
	}
}

// refreshAPIKey attempts to refresh the API key by re-resolving configuration
func (c *customGeminiClient) refreshAPIKey() error {
	newAPIKey, err := config.Get().Resolve(c.providerOptions.config.APIKey)
	if err != nil {
		return fmt.Errorf("failed to resolve API key: %w", err)
	}

	// Check if the key actually changed
	if newAPIKey == c.providerOptions.apiKey {
		return fmt.Errorf("API key unchanged after refresh")
	}

	// Update the API key
	oldKey := c.providerOptions.apiKey
	c.providerOptions.apiKey = newAPIKey

	slog.Info("API key refreshed",
		"old_key_suffix", maskAPIKey(oldKey),
		"new_key_suffix", maskAPIKey(newAPIKey))

	return nil
}

// parseRetryAfter parses the Retry-After header and returns the delay duration
func (c *customGeminiClient) parseRetryAfter(headers http.Header) time.Duration {
	retryAfter := headers.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}

	// Try to parse as seconds (integer)
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try to parse as HTTP-date (not commonly used by APIs, but spec-compliant)
	if t, err := http.ParseTime(retryAfter); err == nil {
		delay := time.Until(t)
		if delay > 0 {
			return delay
		}
	}

	return 0
}

// calculateExponentialBackoff calculates exponential backoff with jitter
func calculateExponentialBackoff(attempts int, config RetryConfig) time.Duration {
	// Calculate base backoff: min(base * 2^(attempts-1), max)
	backoffMs := config.BaseBackoffMs * int(math.Pow(2, float64(attempts-1)))
	if backoffMs > config.MaxBackoffMs {
		backoffMs = config.MaxBackoffMs
	}

	// Add jitter to prevent thundering herd
	jitterRange := int(float64(backoffMs) * config.JitterPercent)
	jitter := rand.Intn(jitterRange*2) - jitterRange // Random value in [-jitterRange, +jitterRange]

	totalMs := backoffMs + jitter
	if totalMs < 0 {
		totalMs = config.BaseBackoffMs // Fall back to base if jitter made it negative
	}

	// Ensure we don't exceed the maximum even after jitter
	if totalMs > config.MaxBackoffMs {
		totalMs = config.MaxBackoffMs
	}

	return time.Duration(totalMs) * time.Millisecond
}

// isNetworkError determines if an error is a network connectivity issue
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Check for DNS errors first
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Check for OpError (includes connection errors)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Check for temporary or timeout errors
		if opErr.Temporary() || opErr.Timeout() {
			return true
		}
		
		// Check for specific syscall errors
		if opErr.Err != nil {
			if syscallErr, ok := opErr.Err.(syscall.Errno); ok {
				return isRetryableSyscallError(syscallErr)
			}
			
			// Check syscall errno by type assertion (different systems might wrap differently)
			if errno, ok := opErr.Err.(*syscall.Errno); ok {
				return isRetryableSyscallError(*errno)
			}
		}
	}

	// Check for common network error types (generic net.Error interface)
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Temporary() || netErr.Timeout()
	}

	// Check for specific error messages (fallback for string matching)
	errStr := strings.ToLower(err.Error())
	networkIndicators := []string{
		"connection refused",
		"connection reset",
		"connection timeout",
		"no route to host",
		"network is unreachable",
		"host is unreachable",
		"i/o timeout",
		"broken pipe",
		"no such host",
	}

	for _, indicator := range networkIndicators {
		if strings.Contains(errStr, indicator) {
			return true
		}
	}

	return false
}

// isRetryableSyscallError checks if a syscall error should trigger a retry
func isRetryableSyscallError(errno syscall.Errno) bool {
	switch errno {
	case syscall.ECONNREFUSED, // Connection refused
		syscall.ECONNRESET,      // Connection reset by peer
		syscall.ECONNABORTED,    // Connection aborted
		syscall.ETIMEDOUT,       // Connection timed out
		syscall.EHOSTUNREACH,    // Host is unreachable
		syscall.ENETUNREACH,     // Network is unreachable
		syscall.EPIPE:           // Broken pipe
		return true
	default:
		return false
	}
}

// createHTTPError creates an HTTPError from an HTTP response
func createHTTPError(resp *http.Response, body []byte) *HTTPError {
	httpErr := &HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Headers:    resp.Header.Clone(),
		Body:       body,
	}

	// Try to parse Gemini API error format
	if geminiErr := parseGeminiAPIError(body); geminiErr != nil {
		httpErr.Message = geminiErr.Error.Message
	} else {
		// Fall back to raw response body
		httpErr.Message = string(body)
	}

	return httpErr
}

// parseGeminiAPIError attempts to parse Gemini API error response format
func parseGeminiAPIError(body []byte) *geminiErrorResponse {
	var errorResp geminiErrorResponse
	if err := json.Unmarshal(body, &errorResp); err != nil {
		return nil
	}

	// Validate that we have an error structure
	if errorResp.Error.Message == "" {
		return nil
	}

	return &errorResp
}

// maskAPIKey masks an API key for logging (shows only last 4 characters)
func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 4 {
		return "****"
	}
	return "***" + apiKey[len(apiKey)-4:]
}

