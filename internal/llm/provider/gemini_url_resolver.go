package provider

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// URLMode represents the URL parsing mode for Gemini API endpoints
type URLMode int

const (
	// ModeStandard indicates standard mode where base_url needs methodPath to form complete request URL
	ModeStandard URLMode = iota
	// ModeFull indicates complete URL mode where base_url (ending with #) is used directly
	ModeFull
)

const (
	// Default Gemini API base URL
	defaultGeminiBase = "https://generativelanguage.googleapis.com"
)

// Environment variable names in priority order
var geminiBaseURLEnvKeys = []string{
	"CRUSH_GEMINI_BASE_URL",
	"GEMINI_BASE_URL",
	"GOOGLE_GEMINI_BASE_URL",
}

// Allowed URL schemes
var allowedSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// Sentinel errors for URL resolver
var (
	// ErrInvalidBaseURL indicates base URL format is invalid
	ErrInvalidBaseURL = errors.New("invalid base URL")
	// ErrInvalidMethodPath indicates method path format is invalid
	ErrInvalidMethodPath = errors.New("invalid method path")
	// ErrInvalidScheme indicates URL scheme is not supported
	ErrInvalidScheme = errors.New("invalid URL scheme")
	// ErrAmbiguousFullURLMarker indicates ambiguous full URL marker usage
	ErrAmbiguousFullURLMarker = errors.New("ambiguous full URL marker")
)

// ResolveGeminiURL resolves Gemini API endpoint URL based on base URL and method path.
// It supports two modes:
// 1. Standard mode: baseURL + methodPath -> full request URL
// 2. Complete URL mode: baseURL ending with '#' is used directly (methodPath ignored)
//
// Examples:
//   - ResolveGeminiURL("https://api.example.com", "v1beta/models/gemini-pro:generateContent")
//     -> "https://api.example.com/v1beta/models/gemini-pro:generateContent"
//   - ResolveGeminiURL("https://api.example.com/complete-endpoint#", "ignored")
//     -> "https://api.example.com/complete-endpoint"
func ResolveGeminiURL(baseURL, methodPath string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("%w: baseURL cannot be empty", ErrInvalidBaseURL)
	}

	// Detect URL mode and get cleaned base URL
	mode, cleanedBase, err := DetectURLMode(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to detect URL mode: %w", err)
	}

	// Validate base URL
	if err := ValidateBaseURL(cleanedBase); err != nil {
		return "", fmt.Errorf("base URL validation failed: %w", err)
	}

	switch mode {
	case ModeStandard:
		if err := ValidateMethodPath(methodPath); err != nil {
			return "", fmt.Errorf("method path validation failed: %w", err)
		}
		return joinURL(cleanedBase, methodPath)
	case ModeFull:
		// In complete URL mode, methodPath is ignored
		return cleanedBase, nil
	default:
		return "", fmt.Errorf("unknown URL mode: %d", mode)
	}
}

// ResolveGeminiURLFromEnv resolves Gemini API URL using environment variables for base URL.
// It follows this priority order:
// 1. CRUSH_GEMINI_BASE_URL
// 2. GEMINI_BASE_URL
// 3. GOOGLE_GEMINI_BASE_URL
// 4. Default: https://generativelanguage.googleapis.com
//
// Environment variables can end with '#' to enable complete URL mode.
func ResolveGeminiURLFromEnv(methodPath string) (string, error) {
	baseURL := getFirstNonEmptyEnv(geminiBaseURLEnvKeys)
	if baseURL == "" {
		baseURL = defaultGeminiBase
	}
	return ResolveGeminiURL(baseURL, methodPath)
}

// DetectURLMode detects URL parsing mode and returns cleaned base URL.
// Returns ModeFull if baseURL ends with single '#', ModeStandard otherwise.
// The '#' marker is removed from the returned cleaned base URL.
func DetectURLMode(baseURL string) (URLMode, string, error) {
	if baseURL == "" {
		return ModeStandard, "", fmt.Errorf("%w: empty base URL", ErrInvalidBaseURL)
	}

	// Check for multiple trailing '#' characters (invalid)
	if strings.HasSuffix(baseURL, "##") {
		return ModeStandard, "", fmt.Errorf("%w: multiple trailing '#' characters are not allowed", ErrAmbiguousFullURLMarker)
	}

	// Check for '#' in the middle (invalid for our purposes)
	if strings.Contains(baseURL, "#") && !strings.HasSuffix(baseURL, "#") {
		return ModeStandard, "", fmt.Errorf("%w: '#' character is only allowed at the end", ErrAmbiguousFullURLMarker)
	}

	// Complete URL mode: ends with single '#'
	if strings.HasSuffix(baseURL, "#") {
		cleaned := strings.TrimSuffix(baseURL, "#")
		if cleaned == "" {
			return ModeStandard, "", fmt.Errorf("%w: base URL cannot be just '#'", ErrInvalidBaseURL)
		}
		return ModeFull, cleaned, nil
	}

	// Standard mode
	return ModeStandard, baseURL, nil
}

// ValidateBaseURL validates base URL format.
// It must be a valid absolute URL with http or https scheme and non-empty host.
func ValidateBaseURL(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("%w: empty base URL", ErrInvalidBaseURL)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("%w: failed to parse URL: %v", ErrInvalidBaseURL, err)
	}

	if parsed.Scheme == "" {
		return fmt.Errorf("%w: missing URL scheme (http or https required)", ErrInvalidScheme)
	}

	if !allowedSchemes[parsed.Scheme] {
		return fmt.Errorf("%w: unsupported scheme '%s' (http or https required)", ErrInvalidScheme, parsed.Scheme)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidBaseURL)
	}

	if !parsed.IsAbs() {
		return fmt.Errorf("%w: must be absolute URL", ErrInvalidBaseURL)
	}

	return nil
}

// ValidateMethodPath validates method path format.
// It must be non-empty and not contain only whitespace characters.
func ValidateMethodPath(methodPath string) error {
	if methodPath == "" {
		return fmt.Errorf("%w: empty method path", ErrInvalidMethodPath)
	}

	trimmed := strings.TrimSpace(methodPath)
	if trimmed == "" {
		return fmt.Errorf("%w: method path contains only whitespace", ErrInvalidMethodPath)
	}

	// Check for control characters that could be problematic
	for _, r := range methodPath {
		if r < 32 && r != '\t' { // Allow tab but not other control chars
			return fmt.Errorf("%w: method path contains invalid control character", ErrInvalidMethodPath)
		}
	}

	return nil
}

// normalizeMethodPath normalizes method path by trimming whitespace and ensuring leading slash.
func normalizeMethodPath(methodPath string) string {
	trimmed := strings.TrimSpace(methodPath)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

// joinURL joins base URL with relative method path using URL resolution rules.
// This ensures proper handling of trailing slashes, query parameters, etc.
func joinURL(baseURL, methodPath string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse base URL: %w", err)
	}

	normalizedPath := normalizeMethodPath(methodPath)
	if normalizedPath == "" {
		return baseURL, nil
	}

	// Parse the method path as relative URL
	rel, err := url.Parse(normalizedPath)
	if err != nil {
		return "", fmt.Errorf("failed to parse method path: %w", err)
	}

	// If method path has no query, preserve base query parameters
	if rel.RawQuery == "" {
		// Build result manually to preserve base query
		// Clean up path to avoid double slashes
		combinedPath := base.Path + rel.Path
		if strings.HasSuffix(base.Path, "/") && strings.HasPrefix(rel.Path, "/") {
			combinedPath = base.Path + strings.TrimPrefix(rel.Path, "/")
		}
		
		result := &url.URL{
			Scheme:   base.Scheme,
			Host:     base.Host,
			Path:     combinedPath,
			RawQuery: base.RawQuery,
			Fragment: base.Fragment,
		}
		return result.String(), nil
	}

	// If method path has query, use normal resolution (query will override)
	resolved := base.ResolveReference(rel)
	return resolved.String(), nil
}

// getFirstNonEmptyEnv returns the first non-empty environment variable value from the given keys.
// Returns empty string if none of the environment variables are set or all are empty.
func getFirstNonEmptyEnv(keys []string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
