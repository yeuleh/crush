package provider

import (
	"errors"
	"fmt"
	"strings"
)

// Validation errors
var (
	ErrEmptyContents     = errors.New("contents cannot be empty")
	ErrInvalidRole       = errors.New("invalid role")
	ErrEmptyParts        = errors.New("parts cannot be empty")
	ErrInvalidMimeType   = errors.New("invalid MIME type")
	ErrEmptyFunctionName = errors.New("function name cannot be empty")
	ErrInvalidTokenCount = errors.New("token count must be non-negative")
)

// Gemini API request structures

// geminiRequest represents the main request structure for Gemini API
type geminiRequest struct {
	Contents          []geminiContent   `json:"contents"`
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	Tools             []geminiTool      `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

// Validate validates the geminiRequest structure
func (r *geminiRequest) Validate() error {
	if len(r.Contents) == 0 {
		return ErrEmptyContents
	}

	for i, content := range r.Contents {
		if err := content.Validate(); err != nil {
			return fmt.Errorf("contents[%d]: %w", i, err)
		}
	}

	if r.SystemInstruction != nil {
		if err := r.SystemInstruction.Validate(); err != nil {
			return fmt.Errorf("systemInstruction: %w", err)
		}
	}

	for i, tool := range r.Tools {
		if err := tool.Validate(); err != nil {
			return fmt.Errorf("tools[%d]: %w", i, err)
		}
	}

	if r.GenerationConfig != nil {
		if err := r.GenerationConfig.Validate(); err != nil {
			return fmt.Errorf("generationConfig: %w", err)
		}
	}

	return nil
}

// geminiContent represents a content block in Gemini API format
type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// Validate validates the geminiContent structure
func (c *geminiContent) Validate() error {
	if c.Role == "" {
		return ErrInvalidRole
	}

	// Validate role is one of the allowed values
	switch c.Role {
	case "user", "model":
		// Valid roles
	default:
		return fmt.Errorf("%w: %s", ErrInvalidRole, c.Role)
	}

	if len(c.Parts) == 0 {
		return ErrEmptyParts
	}

	for i, part := range c.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("parts[%d]: %w", i, err)
		}
	}

	return nil
}

// geminiPart represents a part within content (text, image, function call, etc.)
type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

// Validate validates the geminiPart structure
func (p *geminiPart) Validate() error {
	// Count non-empty fields to ensure exactly one is set
	fieldCount := 0
	if p.Text != "" {
		fieldCount++
	}
	if p.InlineData != nil {
		fieldCount++
		if err := p.InlineData.Validate(); err != nil {
			return fmt.Errorf("inlineData: %w", err)
		}
	}
	if p.FunctionCall != nil {
		fieldCount++
		if err := p.FunctionCall.Validate(); err != nil {
			return fmt.Errorf("functionCall: %w", err)
		}
	}
	if p.FunctionResponse != nil {
		fieldCount++
		if err := p.FunctionResponse.Validate(); err != nil {
			return fmt.Errorf("functionResponse: %w", err)
		}
	}

	if fieldCount == 0 {
		return errors.New("part must have at least one field set")
	}

	return nil
}

// geminiInlineData represents inline binary data (images, etc.)
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// Validate validates the geminiInlineData structure
func (d *geminiInlineData) Validate() error {
	if d.MimeType == "" {
		return ErrInvalidMimeType
	}

	// Validate MIME type format (basic check)
	if !strings.Contains(d.MimeType, "/") {
		return fmt.Errorf("%w: %s", ErrInvalidMimeType, d.MimeType)
	}

	if d.Data == "" {
		return errors.New("data cannot be empty")
	}

	return nil
}

// geminiFunctionCall represents a function call in Gemini format
type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// Validate validates the geminiFunctionCall structure
func (f *geminiFunctionCall) Validate() error {
	if f.Name == "" {
		return ErrEmptyFunctionName
	}

	// Args can be nil or empty, which is valid
	return nil
}

// geminiFunctionResponse represents a function response in Gemini format
type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// Validate validates the geminiFunctionResponse structure
func (f *geminiFunctionResponse) Validate() error {
	if f.Name == "" {
		return ErrEmptyFunctionName
	}

	// Response can be nil or empty, which is valid
	return nil
}

// geminiTool represents a tool definition in Gemini format
type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

// Validate validates the geminiTool structure
func (t *geminiTool) Validate() error {
	if len(t.FunctionDeclarations) == 0 {
		return errors.New("functionDeclarations cannot be empty")
	}

	for i, decl := range t.FunctionDeclarations {
		if err := decl.Validate(); err != nil {
			return fmt.Errorf("functionDeclarations[%d]: %w", i, err)
		}
	}

	return nil
}

// geminiFunctionDeclaration represents a function declaration for tools
type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Validate validates the geminiFunctionDeclaration structure
func (d *geminiFunctionDeclaration) Validate() error {
	if d.Name == "" {
		return ErrEmptyFunctionName
	}

	if d.Description == "" {
		return errors.New("description cannot be empty")
	}

	// Parameters can be nil or empty, which is valid
	return nil
}

// generationConfig represents generation configuration options
type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// Validate validates the generationConfig structure
func (g *generationConfig) Validate() error {
	if g.MaxOutputTokens < 0 {
		return fmt.Errorf("%w: maxOutputTokens cannot be negative", ErrInvalidTokenCount)
	}

	return nil
}

// Gemini API response structures

// geminiResponse represents the main response structure from Gemini API
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}

// Validate validates the geminiResponse structure
func (r *geminiResponse) Validate() error {
	if len(r.Candidates) == 0 {
		return errors.New("candidates cannot be empty")
	}

	for i, candidate := range r.Candidates {
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("candidates[%d]: %w", i, err)
		}
	}

	if r.UsageMetadata != nil {
		if err := r.UsageMetadata.Validate(); err != nil {
			return fmt.Errorf("usageMetadata: %w", err)
		}
	}

	return nil
}

// geminiCandidate represents a candidate response from Gemini API
type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

// Validate validates the geminiCandidate structure
func (c *geminiCandidate) Validate() error {
	if err := c.Content.Validate(); err != nil {
		return fmt.Errorf("content: %w", err)
	}

	// FinishReason can be empty in streaming responses, so we don't validate it strictly
	return nil
}

// geminiUsage represents token usage information from Gemini API
type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// Validate validates the geminiUsage structure
func (u *geminiUsage) Validate() error {
	if u.PromptTokenCount < 0 {
		return fmt.Errorf("%w: promptTokenCount cannot be negative", ErrInvalidTokenCount)
	}

	if u.CandidatesTokenCount < 0 {
		return fmt.Errorf("%w: candidatesTokenCount cannot be negative", ErrInvalidTokenCount)
	}

	if u.CachedContentTokenCount < 0 {
		return fmt.Errorf("%w: cachedContentTokenCount cannot be negative", ErrInvalidTokenCount)
	}

	return nil
}

// geminiStreamChunk represents a streaming response chunk from Gemini API
type geminiStreamChunk struct {
	Candidates    []geminiCandidate `json:"candidates,omitempty"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}

// Validate validates the geminiStreamChunk structure
func (c *geminiStreamChunk) Validate() error {
	for i, candidate := range c.Candidates {
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("candidates[%d]: %w", i, err)
		}
	}

	if c.UsageMetadata != nil {
		if err := c.UsageMetadata.Validate(); err != nil {
			return fmt.Errorf("usageMetadata: %w", err)
		}
	}

	return nil
}

// geminiErrorResponse represents an error response from Gemini API
type geminiErrorResponse struct {
	Error geminiError `json:"error"`
}

// Validate validates the geminiErrorResponse structure
func (e *geminiErrorResponse) Validate() error {
	return e.Error.Validate()
}

// geminiError represents error details from Gemini API
type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Validate validates the geminiError structure
func (e *geminiError) Validate() error {
	if e.Code <= 0 {
		return errors.New("error code must be positive")
	}

	if e.Message == "" {
		return errors.New("error message cannot be empty")
	}

	if e.Status == "" {
		return errors.New("error status cannot be empty")
	}

	return nil
}
