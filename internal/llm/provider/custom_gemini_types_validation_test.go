package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGeminiRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		request geminiRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid request",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role: "user",
						Parts: []geminiPart{
							{Text: "Hello"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty contents",
			request: geminiRequest{
				Contents: []geminiContent{},
			},
			wantErr: true,
			errMsg:  "contents cannot be empty",
		},
		{
			name: "invalid content",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role:  "", // Invalid role
						Parts: []geminiPart{{Text: "Hello"}},
					},
				},
			},
			wantErr: true,
			errMsg:  "contents[0]: invalid role",
		},
		{
			name: "invalid system instruction",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role:  "user",
						Parts: []geminiPart{{Text: "Hello"}},
					},
				},
				SystemInstruction: &geminiContent{
					Role:  "invalid",
					Parts: []geminiPart{{Text: "System"}},
				},
			},
			wantErr: true,
			errMsg:  "systemInstruction: invalid role: invalid",
		},
		{
			name: "invalid generation config",
			request: geminiRequest{
				Contents: []geminiContent{
					{
						Role:  "user",
						Parts: []geminiPart{{Text: "Hello"}},
					},
				},
				GenerationConfig: &generationConfig{
					MaxOutputTokens: -1, // Invalid negative value
				},
			},
			wantErr: true,
			errMsg:  "generationConfig: token count must be non-negative: maxOutputTokens cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiContentValidation(t *testing.T) {
	tests := []struct {
		name    string
		content geminiContent
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid user content",
			content: geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: "Hello"}},
			},
			wantErr: false,
		},
		{
			name: "valid model content",
			content: geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: "Hi there"}},
			},
			wantErr: false,
		},
		{
			name: "empty role",
			content: geminiContent{
				Role:  "",
				Parts: []geminiPart{{Text: "Hello"}},
			},
			wantErr: true,
			errMsg:  "invalid role",
		},
		{
			name: "invalid role",
			content: geminiContent{
				Role:  "system",
				Parts: []geminiPart{{Text: "Hello"}},
			},
			wantErr: true,
			errMsg:  "invalid role: system",
		},
		{
			name: "empty parts",
			content: geminiContent{
				Role:  "user",
				Parts: []geminiPart{},
			},
			wantErr: true,
			errMsg:  "parts cannot be empty",
		},
		{
			name: "invalid part",
			content: geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{}, // Empty part
				},
			},
			wantErr: true,
			errMsg:  "parts[0]: part must have at least one field set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.content.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiPartValidation(t *testing.T) {
	tests := []struct {
		name    string
		part    geminiPart
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid text part",
			part: geminiPart{
				Text: "Hello world",
			},
			wantErr: false,
		},
		{
			name: "valid inline data part",
			part: geminiPart{
				InlineData: &geminiInlineData{
					MimeType: "image/jpeg",
					Data:     "base64data",
				},
			},
			wantErr: false,
		},
		{
			name: "valid function call part",
			part: geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: "get_weather",
					Args: map[string]interface{}{"location": "SF"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid function response part",
			part: geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name:     "get_weather",
					Response: map[string]interface{}{"temp": "75F"},
				},
			},
			wantErr: false,
		},
		{
			name:    "empty part",
			part:    geminiPart{},
			wantErr: true,
			errMsg:  "part must have at least one field set",
		},
		{
			name: "invalid inline data",
			part: geminiPart{
				InlineData: &geminiInlineData{
					MimeType: "", // Invalid empty MIME type
					Data:     "data",
				},
			},
			wantErr: true,
			errMsg:  "inlineData: invalid MIME type",
		},
		{
			name: "invalid function call",
			part: geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: "", // Invalid empty name
				},
			},
			wantErr: true,
			errMsg:  "functionCall: function name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.part.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiInlineDataValidation(t *testing.T) {
	tests := []struct {
		name    string
		data    geminiInlineData
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid image data",
			data: geminiInlineData{
				MimeType: "image/jpeg",
				Data:     "base64encodeddata",
			},
			wantErr: false,
		},
		{
			name: "valid video data",
			data: geminiInlineData{
				MimeType: "video/mp4",
				Data:     "videodata",
			},
			wantErr: false,
		},
		{
			name: "empty mime type",
			data: geminiInlineData{
				MimeType: "",
				Data:     "data",
			},
			wantErr: true,
			errMsg:  "invalid MIME type",
		},
		{
			name: "invalid mime type format",
			data: geminiInlineData{
				MimeType: "invalidmimetype",
				Data:     "data",
			},
			wantErr: true,
			errMsg:  "invalid MIME type: invalidmimetype",
		},
		{
			name: "empty data",
			data: geminiInlineData{
				MimeType: "image/jpeg",
				Data:     "",
			},
			wantErr: true,
			errMsg:  "data cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.data.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiFunctionCallValidation(t *testing.T) {
	tests := []struct {
		name    string
		call    geminiFunctionCall
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid function call with args",
			call: geminiFunctionCall{
				Name: "get_weather",
				Args: map[string]interface{}{"location": "SF"},
			},
			wantErr: false,
		},
		{
			name: "valid function call without args",
			call: geminiFunctionCall{
				Name: "get_time",
				Args: nil,
			},
			wantErr: false,
		},
		{
			name: "empty function name",
			call: geminiFunctionCall{
				Name: "",
				Args: map[string]interface{}{"param": "value"},
			},
			wantErr: true,
			errMsg:  "function name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiToolValidation(t *testing.T) {
	tests := []struct {
		name    string
		tool    geminiTool
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid tool",
			tool: geminiTool{
				FunctionDeclarations: []geminiFunctionDeclaration{
					{
						Name:        "get_weather",
						Description: "Get weather information",
						Parameters:  map[string]interface{}{"type": "object"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty function declarations",
			tool: geminiTool{
				FunctionDeclarations: []geminiFunctionDeclaration{},
			},
			wantErr: true,
			errMsg:  "functionDeclarations cannot be empty",
		},
		{
			name: "invalid function declaration",
			tool: geminiTool{
				FunctionDeclarations: []geminiFunctionDeclaration{
					{
						Name:        "", // Invalid empty name
						Description: "Description",
					},
				},
			},
			wantErr: true,
			errMsg:  "functionDeclarations[0]: function name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tool.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiFunctionDeclarationValidation(t *testing.T) {
	tests := []struct {
		name    string
		decl    geminiFunctionDeclaration
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid declaration",
			decl: geminiFunctionDeclaration{
				Name:        "get_weather",
				Description: "Get weather information",
				Parameters:  map[string]interface{}{"type": "object"},
			},
			wantErr: false,
		},
		{
			name: "valid declaration without parameters",
			decl: geminiFunctionDeclaration{
				Name:        "get_time",
				Description: "Get current time",
				Parameters:  nil,
			},
			wantErr: false,
		},
		{
			name: "empty name",
			decl: geminiFunctionDeclaration{
				Name:        "",
				Description: "Description",
			},
			wantErr: true,
			errMsg:  "function name cannot be empty",
		},
		{
			name: "empty description",
			decl: geminiFunctionDeclaration{
				Name:        "function_name",
				Description: "",
			},
			wantErr: true,
			errMsg:  "description cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.decl.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiUsageValidation(t *testing.T) {
	tests := []struct {
		name    string
		usage   geminiUsage
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid usage",
			usage: geminiUsage{
				PromptTokenCount:        10,
				CandidatesTokenCount:    5,
				CachedContentTokenCount: 2,
			},
			wantErr: false,
		},
		{
			name: "valid usage with zero values",
			usage: geminiUsage{
				PromptTokenCount:        0,
				CandidatesTokenCount:    0,
				CachedContentTokenCount: 0,
			},
			wantErr: false,
		},
		{
			name: "negative prompt tokens",
			usage: geminiUsage{
				PromptTokenCount:     -1,
				CandidatesTokenCount: 5,
			},
			wantErr: true,
			errMsg:  "token count must be non-negative: promptTokenCount cannot be negative",
		},
		{
			name: "negative candidates tokens",
			usage: geminiUsage{
				PromptTokenCount:     10,
				CandidatesTokenCount: -1,
			},
			wantErr: true,
			errMsg:  "token count must be non-negative: candidatesTokenCount cannot be negative",
		},
		{
			name: "negative cached content tokens",
			usage: geminiUsage{
				PromptTokenCount:        10,
				CandidatesTokenCount:    5,
				CachedContentTokenCount: -1,
			},
			wantErr: true,
			errMsg:  "token count must be non-negative: cachedContentTokenCount cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.usage.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiResponseValidation(t *testing.T) {
	tests := []struct {
		name     string
		response geminiResponse
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid response",
			response: geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role:  "model",
							Parts: []geminiPart{{Text: "Hello"}},
						},
						FinishReason: "STOP",
					},
				},
				UsageMetadata: &geminiUsage{
					PromptTokenCount:     10,
					CandidatesTokenCount: 5,
				},
			},
			wantErr: false,
		},
		{
			name: "empty candidates",
			response: geminiResponse{
				Candidates: []geminiCandidate{},
			},
			wantErr: true,
			errMsg:  "candidates cannot be empty",
		},
		{
			name: "invalid candidate",
			response: geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role:  "", // Invalid role
							Parts: []geminiPart{{Text: "Hello"}},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "candidates[0]: content: invalid role",
		},
		{
			name: "invalid usage metadata",
			response: geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role:  "model",
							Parts: []geminiPart{{Text: "Hello"}},
						},
					},
				},
				UsageMetadata: &geminiUsage{
					PromptTokenCount:     -1, // Invalid negative value
					CandidatesTokenCount: 5,
				},
			},
			wantErr: true,
			errMsg:  "usageMetadata: token count must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.response.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeminiErrorValidation(t *testing.T) {
	tests := []struct {
		name    string
		err     geminiError
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid error",
			err: geminiError{
				Code:    400,
				Message: "Bad request",
				Status:  "INVALID_ARGUMENT",
			},
			wantErr: false,
		},
		{
			name: "zero code",
			err: geminiError{
				Code:    0,
				Message: "Error message",
				Status:  "ERROR",
			},
			wantErr: true,
			errMsg:  "error code must be positive",
		},
		{
			name: "negative code",
			err: geminiError{
				Code:    -1,
				Message: "Error message",
				Status:  "ERROR",
			},
			wantErr: true,
			errMsg:  "error code must be positive",
		},
		{
			name: "empty message",
			err: geminiError{
				Code:    400,
				Message: "",
				Status:  "ERROR",
			},
			wantErr: true,
			errMsg:  "error message cannot be empty",
		},
		{
			name: "empty status",
			err: geminiError{
				Code:    400,
				Message: "Error message",
				Status:  "",
			},
			wantErr: true,
			errMsg:  "error status cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.err.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
