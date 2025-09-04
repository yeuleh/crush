package provider

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestCustomGeminiRetryLogic(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		statusCode     int
		expectedType   ErrorType
		expectedRetry  bool
		expectedDelay  bool // Whether delay should be > 0
	}{
		{
			name:          "rate_limit_error_429",
			err:           errors.New("rate limit exceeded"),
			statusCode:    429,
			expectedType:  ErrorTypeRateLimit,
			expectedRetry: true,
			expectedDelay: true,
		},
		{
			name:          "authentication_error_401",
			err:           errors.New("unauthorized"),
			statusCode:    401,
			expectedType:  ErrorTypeAuthentication,
			expectedRetry: true, // Classification marks as retryable
			expectedDelay: false,
		},
		{
			name:          "server_error_500",
			err:           errors.New("internal server error"),
			statusCode:    500,
			expectedType:  ErrorTypeServer,
			expectedRetry: true,
			expectedDelay: true,
		},
		{
			name:          "client_error_400",
			err:           errors.New("bad request"),
			statusCode:    400,
			expectedType:  ErrorTypeClient,
			expectedRetry: false,
			expectedDelay: false,
		},
		{
			name:          "timeout_error",
			err:           errors.New("context deadline exceeded"),
			statusCode:    0,
			expectedType:  ErrorTypeTimeout,
			expectedRetry: true,
			expectedDelay: true,
		},
		{
			name:          "network_error",
			err:           errors.New("connection refused"),
			statusCode:    0,
			expectedType:  ErrorTypeNetwork,
			expectedRetry: true,
			expectedDelay: true,
		},
		{
			name:          "unknown_error",
			err:           errors.New("unknown error"),
			statusCode:    0,
			expectedType:  ErrorTypeUnknown,
			expectedRetry: false,
			expectedDelay: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					extraBody: make(map[string]interface{}),
				},
			}

			// Test error classification
			retryableErr := client.classifyError(tt.err, tt.statusCode)
			if retryableErr.Type != tt.expectedType {
				t.Errorf("Expected error type %v, got %v", tt.expectedType, retryableErr.Type)
			}
			if retryableErr.Retryable != tt.expectedRetry {
				t.Errorf("Expected retryable %v, got %v", tt.expectedRetry, retryableErr.Retryable)
			}

			// Test shouldRetry logic (skip for authentication as it depends on refresh availability)
			if tt.expectedType != ErrorTypeAuthentication {
				shouldRetry, delay := client.shouldRetry(1, tt.err, tt.statusCode)
				if shouldRetry != tt.expectedRetry {
					t.Errorf("Expected shouldRetry %v, got %v", tt.expectedRetry, shouldRetry)
				}
				if tt.expectedDelay && delay == 0 {
					t.Errorf("Expected delay > 0, got %v", delay)
				}
				if !tt.expectedDelay && delay > 0 {
					t.Errorf("Expected no delay, got %v", delay)
				}
			}
		})
	}
}

func TestCustomGeminiAuthenticationRetryWithRefresh(t *testing.T) {
	// Test authentication retry when refresh is available
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			extraBody: map[string]interface{}{
				"alternative_api_key": "alt-key-123",
			},
		},
		apiKey: "original-key",
	}

	err := errors.New("unauthorized")
	shouldRetry, delay := client.shouldRetry(1, err, 401)
	
	if !shouldRetry {
		t.Error("Should retry authentication error when refresh is available")
	}
	
	if delay != 0 {
		t.Errorf("Should retry immediately after key refresh, got delay %v", delay)
	}
	
	// Verify API key was changed
	if client.apiKey == "original-key" {
		t.Error("API key should have been refreshed")
	}
}

func TestCustomGeminiRetryConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		extraBody   map[string]interface{}
		expectedCfg RetryConfig
	}{
		{
			name:      "default_config",
			extraBody: map[string]interface{}{},
			expectedCfg: RetryConfig{
				MaxRetries:    3,
				BaseDelay:     1 * time.Second,
				MaxDelay:      30 * time.Second,
				BackoffFactor: 2.0,
				JitterFactor:  0.1,
			},
		},
		{
			name: "custom_config",
			extraBody: map[string]interface{}{
				"max_retries":           3,
				"retry_base_delay_ms":   500,
				"retry_max_delay_ms":    30000,
				"retry_backoff_factor":  1.5,
				"retry_jitter_factor":   0.2,
			},
			expectedCfg: RetryConfig{
				MaxRetries:    3,
				BaseDelay:     500 * time.Millisecond,
				MaxDelay:      30 * time.Second,
				BackoffFactor: 1.5,
				JitterFactor:  0.2,
			},
		},
		{
			name: "float_values",
			extraBody: map[string]interface{}{
				"max_retries":           float64(2),
				"retry_base_delay_ms":   float64(2000),
				"retry_max_delay_ms":    float64(45000),
				"retry_backoff_factor":  2.5,
				"retry_jitter_factor":   0.15,
			},
			expectedCfg: RetryConfig{
				MaxRetries:    2,
				BaseDelay:     2 * time.Second,
				MaxDelay:      45 * time.Second,
				BackoffFactor: 2.5,
				JitterFactor:  0.15,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					extraBody: tt.extraBody,
				},
			}

			config := client.getRetryConfig()
			if config.MaxRetries != tt.expectedCfg.MaxRetries {
				t.Errorf("Expected MaxRetries %d, got %d", tt.expectedCfg.MaxRetries, config.MaxRetries)
			}
			if config.BaseDelay != tt.expectedCfg.BaseDelay {
				t.Errorf("Expected BaseDelay %v, got %v", tt.expectedCfg.BaseDelay, config.BaseDelay)
			}
			if config.MaxDelay != tt.expectedCfg.MaxDelay {
				t.Errorf("Expected MaxDelay %v, got %v", tt.expectedCfg.MaxDelay, config.MaxDelay)
			}
			if config.BackoffFactor != tt.expectedCfg.BackoffFactor {
				t.Errorf("Expected BackoffFactor %f, got %f", tt.expectedCfg.BackoffFactor, config.BackoffFactor)
			}
			if config.JitterFactor != tt.expectedCfg.JitterFactor {
				t.Errorf("Expected JitterFactor %f, got %f", tt.expectedCfg.JitterFactor, config.JitterFactor)
			}
		})
	}
}

func TestCustomGeminiBackoffCalculation(t *testing.T) {
	config := RetryConfig{
		MaxRetries:    5,
		BaseDelay:     1 * time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		JitterFactor:  0.1,
	}

	// Test exponential backoff progression
	delays := make([]time.Duration, 5)
	for i := 0; i < 5; i++ {
		delays[i] = calculateBackoffDelay(i+1, config)
	}

	// Verify delays are increasing (accounting for jitter)
	for i := 1; i < len(delays); i++ {
		// Allow for jitter by checking if delay is at least 80% of expected exponential value
		expectedMin := time.Duration(float64(config.BaseDelay) * 0.8 * math.Pow(2, float64(i))) // 2^i with 20% tolerance
		if delays[i] < expectedMin {
			t.Errorf("Delay[%d] = %v is too small, expected at least %v", i, delays[i], expectedMin)
		}
	}

	// Verify max delay is respected (allow small tolerance for jitter)
	maxDelayWithTolerance := config.MaxDelay + time.Duration(float64(config.MaxDelay)*config.JitterFactor)
	for i, delay := range delays {
		if delay > maxDelayWithTolerance {
			t.Errorf("Delay[%d] = %v exceeds max delay with tolerance %v", i, delay, maxDelayWithTolerance)
		}
	}
}

func TestCustomGeminiRateLimitDelay(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			extraBody: make(map[string]interface{}),
		},
	}

	config := client.getRetryConfig()

	// Test rate limit delay calculation
	delay1 := client.calculateRateLimitDelay(1, config)
	delay2 := client.calculateRateLimitDelay(2, config)
	delay3 := client.calculateRateLimitDelay(3, config)

	// Rate limit delays should be longer than regular delays
	if delay1 < 5*time.Second {
		t.Errorf("Rate limit delay should be at least 5 seconds, got %v", delay1)
	}

	// Should increase with attempts
	if delay2 <= delay1 {
		t.Errorf("Rate limit delay should increase with attempts: delay1=%v, delay2=%v", delay1, delay2)
	}

	if delay3 <= delay2 {
		t.Errorf("Rate limit delay should increase with attempts: delay2=%v, delay3=%v", delay2, delay3)
	}
}

func TestCustomGeminiAPIKeyRefresh(t *testing.T) {
	tests := []struct {
		name        string
		extraBody   map[string]interface{}
		envVars     map[string]string
		expectRefresh bool
	}{
		{
			name:        "no_refresh_config",
			extraBody:   map[string]interface{}{},
			envVars:     map[string]string{},
			expectRefresh: false,
		},
		{
			name: "alternative_key_available",
			extraBody: map[string]interface{}{
				"alternative_api_key": "alt-key-123",
			},
			envVars:     map[string]string{},
			expectRefresh: true,
		},
		{
			name:      "env_key_available",
			extraBody: map[string]interface{}{},
			envVars: map[string]string{
				"GEMINI_API_KEY": "env-key-456",
			},
			expectRefresh: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables for test
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			client := &customGeminiClient{
				providerOptions: providerClientOptions{
					extraBody: tt.extraBody,
				},
				apiKey: "original-key",
			}

			originalKey := client.apiKey
			refreshed := client.tryRefreshAPIKey()

			if refreshed != tt.expectRefresh {
				t.Errorf("Expected refresh %v, got %v", tt.expectRefresh, refreshed)
			}

			if tt.expectRefresh && client.apiKey == originalKey {
				t.Errorf("API key should have changed after refresh")
			}

			if !tt.expectRefresh && client.apiKey != originalKey {
				t.Errorf("API key should not have changed when refresh not expected")
			}
		})
	}
}

func TestCustomGeminiMaxRetriesReached(t *testing.T) {
	client := &customGeminiClient{
		providerOptions: providerClientOptions{
			extraBody: map[string]interface{}{
				"max_retries": 3, // Allow 3 attempts for testing
			},
		},
	}

	// Test that retry stops after max attempts
	err := errors.New("rate limit exceeded")
	
	// First attempt should retry (attempt < max_retries)
	shouldRetry1, _ := client.shouldRetry(1, err, 429)
	if !shouldRetry1 {
		t.Error("First retry attempt should be allowed")
	}

	// Second attempt should retry (attempt < max_retries)
	shouldRetry2, _ := client.shouldRetry(2, err, 429)
	if !shouldRetry2 {
		t.Error("Second retry attempt should be allowed")
	}

	// Third attempt should not retry (attempt >= max_retries)
	shouldRetry3, _ := client.shouldRetry(3, err, 429)
	if shouldRetry3 {
		t.Error("Third retry attempt should not be allowed (attempt >= max_retries)")
	}
}