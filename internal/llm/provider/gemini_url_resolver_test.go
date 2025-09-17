package provider

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveGeminiURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		methodPath  string
		expected    string
		expectedErr error
	}{
		// Standard mode tests
		{
			name:       "standard mode basic",
			baseURL:    "https://api.example.com",
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "standard mode with trailing slash in base",
			baseURL:    "https://api.example.com/",
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "standard mode with leading slash in method path",
			baseURL:    "https://api.example.com",
			methodPath: "/v1beta/models/gemini-pro:generateContent",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "standard mode with both slashes",
			baseURL:    "https://api.example.com/",
			methodPath: "/v1beta/models/gemini-pro:generateContent",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "standard mode with query in base URL",
			baseURL:    "https://api.example.com?key=abc123",
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent?key=abc123",
		},
		{
			name:       "standard mode with query in method path (should override base query)",
			baseURL:    "https://api.example.com?key=old",
			methodPath: "v1beta/models/gemini-pro:generateContent?key=new",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent?key=new",
		},
		{
			name:       "standard mode with IPv4 host",
			baseURL:    "https://192.168.1.1:8080",
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://192.168.1.1:8080/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "standard mode with IPv6 host",
			baseURL:    "https://[2001:db8::1]:8080",
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://[2001:db8::1]:8080/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "standard mode with localhost",
			baseURL:    "http://localhost:3000",
			methodPath: "api/v1/chat",
			expected:   "http://localhost:3000/api/v1/chat",
		},

		// Complete URL mode tests
		{
			name:       "complete URL mode basic",
			baseURL:    "https://api.example.com/complete-endpoint#",
			methodPath: "ignored",
			expected:   "https://api.example.com/complete-endpoint",
		},
		{
			name:       "complete URL mode with query parameters",
			baseURL:    "https://api.example.com/complete-endpoint?key=abc123#",
			methodPath: "ignored",
			expected:   "https://api.example.com/complete-endpoint?key=abc123",
		},
		{
			name:       "complete URL mode with path and query",
			baseURL:    "https://api.example.com/v1beta/models/gemini-pro:generateContent?key=abc123&model=pro#",
			methodPath: "ignored",
			expected:   "https://api.example.com/v1beta/models/gemini-pro:generateContent?key=abc123&model=pro",
		},

		// Error cases
		{
			name:        "empty base URL",
			baseURL:     "",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrInvalidBaseURL,
		},
		{
			name:        "invalid base URL scheme",
			baseURL:     "ftp://api.example.com",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrInvalidScheme,
		},
		{
			name:        "missing scheme",
			baseURL:     "api.example.com",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrInvalidScheme,
		},
		{
			name:        "empty host",
			baseURL:     "https:///path",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrInvalidBaseURL,
		},
		{
			name:        "standard mode empty method path",
			baseURL:     "https://api.example.com",
			methodPath:  "",
			expected:    "",
			expectedErr: ErrInvalidMethodPath,
		},
		{
			name:        "standard mode whitespace-only method path",
			baseURL:     "https://api.example.com",
			methodPath:  "  \t  ",
			expected:    "",
			expectedErr: ErrInvalidMethodPath,
		},
		{
			name:        "multiple trailing hash characters",
			baseURL:     "https://api.example.com##",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrAmbiguousFullURLMarker,
		},
		{
			name:        "hash in middle of URL",
			baseURL:     "https://api.example.com#fragment/path",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrAmbiguousFullURLMarker,
		},
		{
			name:        "just hash character",
			baseURL:     "#",
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrInvalidBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := ResolveGeminiURL(tt.baseURL, tt.methodPath)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Empty(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestResolveGeminiURLFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		methodPath  string
		expected    string
		expectedErr error
	}{
		{
			name: "use CRUSH_GEMINI_BASE_URL (highest priority)",
			envVars: map[string]string{
				"CRUSH_GEMINI_BASE_URL":  "https://crush.example.com",
				"GEMINI_BASE_URL":        "https://gemini.example.com",
				"GOOGLE_GEMINI_BASE_URL": "https://google.example.com",
			},
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://crush.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name: "use GEMINI_BASE_URL (second priority)",
			envVars: map[string]string{
				"GEMINI_BASE_URL":        "https://gemini.example.com",
				"GOOGLE_GEMINI_BASE_URL": "https://google.example.com",
			},
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://gemini.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name: "use GOOGLE_GEMINI_BASE_URL (third priority)",
			envVars: map[string]string{
				"GOOGLE_GEMINI_BASE_URL": "https://google.example.com",
			},
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://google.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:    "use default when no env vars set",
			envVars: map[string]string{
				// Empty env vars
			},
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name: "ignore empty env vars",
			envVars: map[string]string{
				"CRUSH_GEMINI_BASE_URL":  "",
				"GEMINI_BASE_URL":        "  ",
				"GOOGLE_GEMINI_BASE_URL": "https://google.example.com",
			},
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "https://google.example.com/v1beta/models/gemini-pro:generateContent",
		},
		{
			name: "complete URL mode from env",
			envVars: map[string]string{
				"CRUSH_GEMINI_BASE_URL": "https://api.example.com/complete-endpoint?key=abc123#",
			},
			methodPath: "ignored",
			expected:   "https://api.example.com/complete-endpoint?key=abc123",
		},
		{
			name: "error propagated from ResolveGeminiURL",
			envVars: map[string]string{
				"CRUSH_GEMINI_BASE_URL": "invalid-url",
			},
			methodPath:  "path",
			expected:    "",
			expectedErr: ErrInvalidScheme,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: Cannot use t.Parallel() with t.Setenv()

			// Set up environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			result, err := ResolveGeminiURLFromEnv(tt.methodPath)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Empty(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestDetectURLMode(t *testing.T) {
	tests := []struct {
		name         string
		baseURL      string
		expectedMode URLMode
		expectedURL  string
		expectedErr  error
	}{
		{
			name:         "standard mode - no hash",
			baseURL:      "https://api.example.com",
			expectedMode: ModeStandard,
			expectedURL:  "https://api.example.com",
		},
		{
			name:         "complete URL mode - single trailing hash",
			baseURL:      "https://api.example.com/complete#",
			expectedMode: ModeFull,
			expectedURL:  "https://api.example.com/complete",
		},
		{
			name:         "empty base URL",
			baseURL:      "",
			expectedMode: ModeStandard,
			expectedURL:  "",
			expectedErr:  ErrInvalidBaseURL,
		},
		{
			name:         "multiple trailing hash",
			baseURL:      "https://api.example.com##",
			expectedMode: ModeStandard,
			expectedURL:  "",
			expectedErr:  ErrAmbiguousFullURLMarker,
		},
		{
			name:         "hash in middle",
			baseURL:      "https://api.example.com#fragment/path",
			expectedMode: ModeStandard,
			expectedURL:  "",
			expectedErr:  ErrAmbiguousFullURLMarker,
		},
		{
			name:         "just hash",
			baseURL:      "#",
			expectedMode: ModeStandard,
			expectedURL:  "",
			expectedErr:  ErrInvalidBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mode, cleanedURL, err := DetectURLMode(tt.baseURL)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Equal(t, ModeStandard, mode)
				assert.Empty(t, cleanedURL)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedMode, mode)
				assert.Equal(t, tt.expectedURL, cleanedURL)
			}
		})
	}
}

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		expectedErr error
	}{
		{
			name:    "valid HTTPS URL",
			baseURL: "https://api.example.com",
		},
		{
			name:    "valid HTTP URL",
			baseURL: "http://api.example.com",
		},
		{
			name:    "valid URL with port",
			baseURL: "https://api.example.com:8080",
		},
		{
			name:    "valid IPv4 URL",
			baseURL: "https://192.168.1.1",
		},
		{
			name:    "valid IPv6 URL",
			baseURL: "https://[2001:db8::1]",
		},
		{
			name:    "valid localhost",
			baseURL: "http://localhost:3000",
		},
		{
			name:        "empty URL",
			baseURL:     "",
			expectedErr: ErrInvalidBaseURL,
		},
		{
			name:        "invalid URL format",
			baseURL:     "not-a-url",
			expectedErr: ErrInvalidScheme,
		},
		{
			name:        "missing scheme",
			baseURL:     "api.example.com",
			expectedErr: ErrInvalidScheme,
		},
		{
			name:        "invalid scheme",
			baseURL:     "ftp://api.example.com",
			expectedErr: ErrInvalidScheme,
		},
		{
			name:        "missing host",
			baseURL:     "https://",
			expectedErr: ErrInvalidBaseURL,
		},
		{
			name:        "relative URL",
			baseURL:     "/relative/path",
			expectedErr: ErrInvalidScheme,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateBaseURL(tt.baseURL)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateMethodPath(t *testing.T) {
	tests := []struct {
		name        string
		methodPath  string
		expectedErr error
	}{
		{
			name:       "valid path",
			methodPath: "v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "valid path with leading slash",
			methodPath: "/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "valid path with query",
			methodPath: "v1beta/models/gemini-pro:generateContent?key=abc",
		},
		{
			name:       "valid path with tab character",
			methodPath: "path\twith\ttab",
		},
		{
			name:        "empty path",
			methodPath:  "",
			expectedErr: ErrInvalidMethodPath,
		},
		{
			name:        "whitespace-only path",
			methodPath:  "  \t  ",
			expectedErr: ErrInvalidMethodPath,
		},
		{
			name:        "path with control character",
			methodPath:  "path\nwith\nnewline",
			expectedErr: ErrInvalidMethodPath,
		},
		{
			name:        "path with other control characters",
			methodPath:  "path\x01with\x02control",
			expectedErr: ErrInvalidMethodPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateMethodPath(tt.methodPath)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNormalizeMethodPath(t *testing.T) {
	tests := []struct {
		name       string
		methodPath string
		expected   string
	}{
		{
			name:       "path without leading slash",
			methodPath: "v1beta/models/gemini-pro:generateContent",
			expected:   "/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "path with leading slash",
			methodPath: "/v1beta/models/gemini-pro:generateContent",
			expected:   "/v1beta/models/gemini-pro:generateContent",
		},
		{
			name:       "path with leading and trailing whitespace",
			methodPath: "  /path  ",
			expected:   "/path",
		},
		{
			name:       "path with only trailing whitespace, no leading slash",
			methodPath: "path  ",
			expected:   "/path",
		},
		{
			name:       "empty path",
			methodPath: "",
			expected:   "",
		},
		{
			name:       "whitespace-only path",
			methodPath: "  \t  ",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := normalizeMethodPath(tt.methodPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestJoinURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		methodPath  string
		expected    string
		expectedErr error
	}{
		{
			name:       "basic join",
			baseURL:    "https://api.example.com",
			methodPath: "path",
			expected:   "https://api.example.com/path",
		},
		{
			name:       "join with trailing slash in base",
			baseURL:    "https://api.example.com/",
			methodPath: "path",
			expected:   "https://api.example.com/path",
		},
		{
			name:       "join with leading slash in path",
			baseURL:    "https://api.example.com",
			methodPath: "/path",
			expected:   "https://api.example.com/path",
		},
		{
			name:       "join with query in base",
			baseURL:    "https://api.example.com?key=old",
			methodPath: "path?key=new",
			expected:   "https://api.example.com/path?key=new",
		},
		{
			name:       "empty method path",
			baseURL:    "https://api.example.com",
			methodPath: "",
			expected:   "https://api.example.com",
		},
		{
			name:        "invalid base URL",
			baseURL:     ":",
			methodPath:  "path",
			expectedErr: errors.New("failed to parse base URL"),
		},
		{
			name:        "invalid method path with control chars in URL parsing",
			baseURL:     "https://api.example.com",
			methodPath:  "path\x00invalid",
			expectedErr: errors.New("failed to parse method path"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := joinURL(tt.baseURL, tt.methodPath)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr.Error())
				assert.Empty(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetFirstNonEmptyEnv(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		keys     []string
		expected string
	}{
		{
			name: "first key has value",
			envVars: map[string]string{
				"KEY1": "value1",
				"KEY2": "value2",
			},
			keys:     []string{"KEY1", "KEY2"},
			expected: "value1",
		},
		{
			name: "second key has value",
			envVars: map[string]string{
				"KEY1": "",
				"KEY2": "value2",
			},
			keys:     []string{"KEY1", "KEY2"},
			expected: "value2",
		},
		{
			name: "whitespace is trimmed",
			envVars: map[string]string{
				"KEY1": "  value1  ",
			},
			keys:     []string{"KEY1"},
			expected: "value1",
		},
		{
			name: "empty after trimming is ignored",
			envVars: map[string]string{
				"KEY1": "  ",
				"KEY2": "value2",
			},
			keys:     []string{"KEY1", "KEY2"},
			expected: "value2",
		},
		{
			name:     "no keys match",
			envVars:  map[string]string{},
			keys:     []string{"NONEXISTENT1", "NONEXISTENT2"},
			expected: "",
		},
		{
			name:     "empty keys list",
			envVars:  map[string]string{"KEY1": "value1"},
			keys:     []string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: Cannot use t.Parallel() with t.Setenv()

			// Set up environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			result := getFirstNonEmptyEnv(tt.keys)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConcurrentSafety(t *testing.T) {
	const numGoroutines = 100
	const numIterations = 10

	var wg sync.WaitGroup
	results := make(chan string, numGoroutines*numIterations)

	// Test concurrent calls to ResolveGeminiURL
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			baseURL := "https://api.example.com"
			methodPath := "v1beta/models/gemini-pro:generateContent"

			for j := 0; j < numIterations; j++ {
				result, err := ResolveGeminiURL(baseURL, methodPath)
				require.NoError(t, err)
				results <- result
			}
		}(i)
	}

	wg.Wait()
	close(results)

	// Verify all results are consistent
	expectedResult := "https://api.example.com/v1beta/models/gemini-pro:generateContent"
	count := 0
	for result := range results {
		assert.Equal(t, expectedResult, result)
		count++
	}

	assert.Equal(t, numGoroutines*numIterations, count)
}

func TestEnvironmentVariablePriority(t *testing.T) {
	// Note: Cannot use t.Parallel() with t.Setenv()
	// Test exact priority order as specified
	t.Setenv("CRUSH_GEMINI_BASE_URL", "https://crush.example.com")
	t.Setenv("GEMINI_BASE_URL", "https://gemini.example.com")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://google.example.com")

	result, err := ResolveGeminiURLFromEnv("path")
	require.NoError(t, err)

	// Should use CRUSH_GEMINI_BASE_URL (highest priority)
	expected := "https://crush.example.com/path"
	assert.Equal(t, expected, result)
}

// Benchmark tests
func BenchmarkResolveGeminiURL_Standard(b *testing.B) {
	baseURL := "https://generativelanguage.googleapis.com"
	methodPath := "v1beta/models/gemini-pro:generateContent"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ResolveGeminiURL(baseURL, methodPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveGeminiURL_Complete(b *testing.B) {
	baseURL := "https://api.example.com/complete-endpoint?key=abc123#"
	methodPath := "ignored"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ResolveGeminiURL(baseURL, methodPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveGeminiURLFromEnv(b *testing.B) {
	os.Setenv("CRUSH_GEMINI_BASE_URL", "https://api.example.com")
	defer os.Unsetenv("CRUSH_GEMINI_BASE_URL")

	methodPath := "v1beta/models/gemini-pro:generateContent"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ResolveGeminiURLFromEnv(methodPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}
