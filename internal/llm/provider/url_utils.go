package provider

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/openai/openai-go/option"
)

// IsCustomEndpointURL checks if a base URL is marked as a complete endpoint
// Returns true if the URL ends with '#', indicating it's a complete API endpoint
func IsCustomEndpointURL(baseURL string) bool {
	return strings.HasSuffix(baseURL, "#")
}

// GetCleanEndpointURL removes the '#' marker from a custom endpoint URL
func GetCleanEndpointURL(baseURL string) string {
	if IsCustomEndpointURL(baseURL) {
		return strings.TrimSuffix(baseURL, "#")
	}
	return baseURL
}

// CreateCustomEndpointMiddleware creates a middleware that replaces OpenAI SDK URLs
// with a custom complete endpoint for chat completions
func CreateCustomEndpointMiddleware(customEndpointURL string) option.Middleware {
	// Clean the endpoint URL (remove # marker)
	cleanEndpointURL := GetCleanEndpointURL(customEndpointURL)

	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		// Parse the custom endpoint URL
		customURL, err := url.Parse(cleanEndpointURL)
		if err != nil {
			// If parsing fails, proceed with original request
			return next(req)
		}

		// Only replace URLs for chat completions endpoint
		if req.URL.Path == "/v1/chat/completions" {
			// Store original query parameters
			originalQuery := req.URL.RawQuery

			// Replace the entire URL with the custom endpoint
			req.URL.Scheme = customURL.Scheme
			req.URL.Host = customURL.Host
			req.URL.Path = customURL.Path

			// Handle query parameters
			if customURL.RawQuery != "" {
				if originalQuery != "" {
					req.URL.RawQuery = customURL.RawQuery + "&" + originalQuery
				} else {
					req.URL.RawQuery = customURL.RawQuery
				}
			} else {
				req.URL.RawQuery = originalQuery
			}
		}

		// For all other endpoints (like /v1/models), proceed normally
		// This means other API calls will fail as expected per requirement #4
		return next(req)
	}
}

// GetEffectiveBaseURLForOpenAI returns the appropriate base URL for OpenAI SDK initialization
// If the URL is a custom endpoint (ends with #), returns a placeholder base URL
// Otherwise returns the original URL
func GetEffectiveBaseURLForOpenAI(baseURL string) string {
	if IsCustomEndpointURL(baseURL) {
		// Return a placeholder base URL that will generate the standard /v1/chat/completions path
		// which will then be replaced by our middleware
		return "https://api.openai.com/v1"
	}
	return baseURL
}
