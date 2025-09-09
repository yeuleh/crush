package provider

import "github.com/charmbracelet/catwalk/pkg/catwalk"

// Extended provider types for custom implementations
const (
	// TypeCustomGemini represents a custom Gemini provider implementation
	// that uses HTTP requests instead of the official SDK
	TypeCustomGemini catwalk.Type = "custom-gemini"
)
