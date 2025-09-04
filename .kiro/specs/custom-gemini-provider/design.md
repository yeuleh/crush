# 设计文档

## 概述

本设计文档描述了自定义 Gemini 提供商的实现，该提供商使用直接 HTTP API 调用而不是 `google.golang.org/genai` 客户端库。这种方法提供了对请求/响应处理的更多控制，减少了外部依赖，并支持系统代理配置。

## 架构

### 高层架构

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   Provider      │    │ CustomGemini     │    │  Gemini API         │
│   Interface     │───▶│ Client           │───▶│  (googleapis.com)   │
└─────────────────┘    └──────────────────┘    └─────────────────────┘
                              │
                              ▼
                       ┌──────────────────┐
                       │   HTTP Client    │
                       │  (with Proxy)    │
                       └──────────────────┘
```

### 组件关系

1. **CustomGeminiClient**: 实现 `ProviderClient` 接口
2. **HTTP Client**: 使用标准 `net/http` 包，支持系统代理
3. **Message Converter**: 在内部消息格式和 Gemini API 格式之间转换
4. **Error Handler**: 处理重试逻辑和错误恢复
5. **Token Usage Tracker**: 跟踪和报告令牌使用情况

## 组件和接口

### CustomGeminiClient 结构

```go
type customGeminiClient struct {
    providerOptions providerClientOptions
    httpClient      *http.Client
    baseURL         string
    apiKey          string
    streamingSupported bool // 缓存流式支持状态
    streamingChecked   bool // 是否已检查流式支持
    mu                 sync.RWMutex // 保护并发访问
}

type CustomGeminiClient ProviderClient
```

### 核心方法

```go
func (c *customGeminiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error)
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent
func (c *customGeminiClient) Model() catwalk.Model
```

### HTTP 客户端配置

```go
func createHTTPClient(opts providerClientOptions) *http.Client {
    transport := &http.Transport{
        Proxy: http.ProxyFromEnvironment, // 支持系统代理
        TLSHandshakeTimeout: 10 * time.Second,
        IdleConnTimeout:     90 * time.Second,
    }
    
    if config.Get().Options.Debug {
        transport = log.NewHTTPTransport(transport)
    }
    
    return &http.Client{
        Transport: transport,
        Timeout:   60 * time.Second,
    }
}
```

## 数据模型

### Gemini API 请求格式

```go
type GeminiRequest struct {
    Contents         []GeminiContent      `json:"contents"`
    Tools            []GeminiTool         `json:"tools,omitempty"`
    ToolConfig       *GeminiToolConfig    `json:"toolConfig,omitempty"`
    SafetySettings   []GeminiSafetySetting `json:"safetySettings,omitempty"`
    SystemInstruction *GeminiContent      `json:"systemInstruction,omitempty"`
    GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

type GeminiContent struct {
    Parts []GeminiPart `json:"parts"`
    Role  string       `json:"role,omitempty"`
}

type GeminiPart struct {
    Text         string                `json:"text,omitempty"`
    InlineData   *GeminiInlineData     `json:"inlineData,omitempty"`
    FunctionCall *GeminiFunctionCall   `json:"functionCall,omitempty"`
    FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
}

type GeminiInlineData struct {
    MimeType string `json:"mimeType"`
    Data     string `json:"data"` // base64 encoded
}

type GeminiFunctionCall struct {
    Name string                 `json:"name"`
    Args map[string]interface{} `json:"args"`
}

type GeminiFunctionResponse struct {
    Name     string                 `json:"name"`
    Response map[string]interface{} `json:"response"`
}
```

### Gemini API 响应格式

```go
type GeminiResponse struct {
    Candidates    []GeminiCandidate `json:"candidates"`
    UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
}

type GeminiCandidate struct {
    Content      *GeminiContent    `json:"content"`
    FinishReason string           `json:"finishReason"`
    Index        int              `json:"index"`
}

type GeminiUsageMetadata struct {
    PromptTokenCount     int `json:"promptTokenCount"`
    CandidatesTokenCount int `json:"candidatesTokenCount"`
    TotalTokenCount      int `json:"totalTokenCount"`
    CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}
```

### 工具定义格式

```go
type GeminiTool struct {
    FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations"`
}

type GeminiFunctionDeclaration struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Parameters  map[string]interface{} `json:"parameters"`
}
```

## 降级机制

### 流式支持检测

```go
func (c *customGeminiClient) checkStreamingSupport(ctx context.Context) bool {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    if c.streamingChecked {
        return c.streamingSupported
    }
    
    // 尝试一个简单的流式请求来检测支持
    testRequest := &GeminiRequest{
        Contents: []GeminiContent{
            {
                Parts: []GeminiPart{{Text: "test"}},
                Role:  "user",
            },
        },
    }
    
    url := fmt.Sprintf("%s/v1beta/models/gemini-pro:streamGenerateContent", c.baseURL)
    req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
    req.Header.Set("Accept", "text/event-stream")
    
    resp, err := c.httpClient.Do(req)
    if err != nil || resp.StatusCode == 404 || resp.StatusCode == 501 {
        c.streamingSupported = false
    } else {
        c.streamingSupported = true
    }
    
    if resp != nil {
        resp.Body.Close()
    }
    
    c.streamingChecked = true
    return c.streamingSupported
}
```

### 降级流式实现

```go
func (c *customGeminiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
    eventChan := make(chan ProviderEvent)
    
    go func() {
        defer close(eventChan)
        
        // 检查流式支持
        if c.checkStreamingSupport(ctx) {
            // 尝试真正的流式响应
            if c.tryRealStreaming(ctx, messages, tools, eventChan) {
                return // 成功完成
            }
            // 如果流式失败，标记为不支持并降级
            c.mu.Lock()
            c.streamingSupported = false
            c.mu.Unlock()
            slog.Warn("Streaming failed, falling back to non-streaming mode")
        }
        
        // 降级到模拟流式响应
        c.simulateStreaming(ctx, messages, tools, eventChan)
    }()
    
    return eventChan
}

func (c *customGeminiClient) tryRealStreaming(ctx context.Context, messages []message.Message, tools []tools.BaseTool, eventChan chan<- ProviderEvent) bool {
    request := c.buildRequest(messages, tools)
    
    url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent", c.baseURL, c.getModelID())
    req, err := c.buildStreamRequest(ctx, url, request)
    if err != nil {
        return false
    }
    
    resp, err := c.httpClient.Do(req)
    if err != nil || resp.StatusCode >= 400 {
        if resp != nil {
            resp.Body.Close()
        }
        return false
    }
    defer resp.Body.Close()
    
    return c.processStreamResponse(resp, eventChan)
}

func (c *customGeminiClient) simulateStreaming(ctx context.Context, messages []message.Message, tools []tools.BaseTool, eventChan chan<- ProviderEvent) {
    // 调用非流式 API
    response, err := c.send(ctx, messages, tools)
    if err != nil {
        eventChan <- ProviderEvent{Type: EventError, Error: err}
        return
    }
    
    // 模拟流式事件序列
    eventChan <- ProviderEvent{Type: EventContentStart}
    
    // 分块发送内容
    content := response.Content
    if content != "" {
        chunkSize := 20 // 每次发送20个字符
        for i := 0; i < len(content); i += chunkSize {
            // 检查上下文取消
            select {
            case <-ctx.Done():
                eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
                return
            default:
            }
            
            end := i + chunkSize
            if end > len(content) {
                end = len(content)
            }
            
            chunk := content[i:end]
            eventChan <- ProviderEvent{
                Type:    EventContentDelta,
                Content: chunk,
            }
            
            // 添加小延迟模拟真实流式体验
            time.Sleep(20 * time.Millisecond)
        }
    }
    
    // 处理工具调用
    for _, toolCall := range response.ToolCalls {
        eventChan <- ProviderEvent{
            Type:     EventToolUseStart,
            ToolCall: &toolCall,
        }
        // 模拟工具调用处理时间
        time.Sleep(10 * time.Millisecond)
        eventChan <- ProviderEvent{
            Type:     EventToolUseStop,
            ToolCall: &toolCall,
        }
    }
    
    eventChan <- ProviderEvent{Type: EventContentStop}
    eventChan <- ProviderEvent{
        Type:     EventComplete,
        Response: response,
    }
}
```

### 配置选项

```go
type CustomGeminiConfig struct {
    ForceNonStreaming bool `json:"force_non_streaming,omitempty"` // 强制使用非流式
    SimulationDelay   int  `json:"simulation_delay,omitempty"`   // 模拟延迟（毫秒）
    ChunkSize         int  `json:"chunk_size,omitempty"`         // 模拟块大小
}
```

## 错误处理

### 重试策略

```go
type RetryConfig struct {
    MaxRetries      int
    BaseDelay       time.Duration
    MaxDelay        time.Duration
    BackoffFactor   float64
    JitterFactor    float64
}

func (c *customGeminiClient) shouldRetry(attempts int, err error) (bool, time.Duration, error) {
    if attempts > maxRetries {
        return false, 0, fmt.Errorf("maximum retry attempts reached: %d", maxRetries)
    }
    
    // 检查是否为可重试的错误
    if isRateLimitError(err) || isTemporaryNetworkError(err) {
        delay := calculateBackoffDelay(attempts)
        return true, delay, nil
    }
    
    // 检查 API 密钥过期
    if isAuthError(err) {
        // 尝试刷新 API 密钥
        if newKey, refreshErr := refreshAPIKey(); refreshErr == nil {
            c.apiKey = newKey
            return true, 0, nil
        }
    }
    
    return false, 0, err
}
```

### 错误类型识别

```go
func isRateLimitError(err error) bool {
    // 检查 HTTP 状态码 429 或相关错误消息
    return strings.Contains(err.Error(), "rate limit") ||
           strings.Contains(err.Error(), "quota exceeded") ||
           strings.Contains(err.Error(), "too many requests")
}

func isAuthError(err error) bool {
    // 检查 HTTP 状态码 401 或相关错误消息
    return strings.Contains(err.Error(), "unauthorized") ||
           strings.Contains(err.Error(), "invalid api key") ||
           strings.Contains(err.Error(), "authentication failed")
}
```

## 测试策略

### 单元测试

1. **消息转换测试**: 验证内部消息格式与 Gemini API 格式之间的正确转换
2. **工具调用测试**: 验证工具定义和响应的正确处理
3. **错误处理测试**: 验证重试逻辑和错误恢复机制
4. **令牌使用测试**: 验证令牌计数的准确性

### 集成测试

1. **API 连接测试**: 验证与真实 Gemini API 的连接
2. **流式响应测试**: 验证流式响应的正确处理
3. **代理支持测试**: 验证通过代理的连接功能
4. **并发测试**: 验证多个并发请求的处理

### 测试数据

```go
var testMessages = []message.Message{
    {
        Role: message.User,
        Parts: []message.Part{
            {Content: "Hello, how are you?"},
        },
    },
    {
        Role: message.Assistant,
        Parts: []message.Part{
            {Content: "I'm doing well, thank you!"},
        },
    },
}

var testTools = []tools.BaseTool{
    &mockTool{
        name: "get_weather",
        description: "Get current weather information",
        parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "location": map[string]interface{}{
                    "type": "string",
                    "description": "The city name",
                },
            },
            "required": []string{"location"},
        },
    },
}
```

## 性能考虑

### HTTP 连接池

- 使用 `http.Transport` 的连接池功能
- 配置适当的 `MaxIdleConns` 和 `IdleConnTimeout`
- 支持 HTTP/2 以提高性能

### 内存管理

- 使用流式处理避免大响应的内存问题
- 及时释放不再需要的资源
- 使用对象池减少 GC 压力

### 并发处理

- 支持多个并发请求
- 使用 context 进行请求取消和超时控制
- 实现适当的速率限制

## 安全考虑

### API 密钥管理

- 从环境变量或配置文件安全读取 API 密钥
- 不在日志中记录敏感信息
- 支持 API 密钥轮换

### 网络安全

- 强制使用 HTTPS
- 验证 TLS 证书
- 支持自定义 CA 证书

### 数据保护

- 不在日志中记录用户输入内容
- 支持请求/响应的加密传输
- 遵循数据最小化原则

## 配置示例

### 基本配置

```json
{
  "providers": {
    "custom-gemini": {
      "type": "custom-gemini",
      "base_url": "https://generativelanguage.googleapis.com",
      "api_key": "$GEMINI_API_KEY",
      "models": [
        {
          "id": "gemini-1.5-pro",
          "name": "Gemini 1.5 Pro",
          "context_window": 2097152,
          "default_max_tokens": 8192,
          "supports_attachments": true
        }
      ]
    }
  }
}
```

### 高级配置

```json
{
  "providers": {
    "custom-gemini": {
      "type": "custom-gemini",
      "base_url": "https://generativelanguage.googleapis.com",
      "api_key": "$GEMINI_API_KEY",
      "extra_headers": {
        "User-Agent": "CustomGeminiClient/1.0"
      },
      "extra_body": {
        "safetySettings": [
          {
            "category": "HARM_CATEGORY_HARASSMENT",
            "threshold": "BLOCK_MEDIUM_AND_ABOVE"
          }
        ],
        "force_non_streaming": false,
        "simulation_delay": 20,
        "chunk_size": 20
      },
      "models": [
        {
          "id": "gemini-1.5-pro",
          "name": "Gemini 1.5 Pro",
          "context_window": 2097152,
          "default_max_tokens": 8192,
          "supports_attachments": true
        },
        {
          "id": "gemini-1.5-flash",
          "name": "Gemini 1.5 Flash",
          "context_window": 1048576,
          "default_max_tokens": 8192,
          "supports_attachments": true
        }
      ]
    }
  }
}
```

### 本地/不完整 API 配置

```json
{
  "providers": {
    "local-gemini": {
      "type": "custom-gemini",
      "base_url": "http://localhost:8080",
      "api_key": "local-key",
      "extra_body": {
        "force_non_streaming": true,
        "simulation_delay": 50,
        "chunk_size": 10
      },
      "models": [
        {
          "id": "gemini-pro",
          "name": "Local Gemini Pro",
          "context_window": 32768,
          "default_max_tokens": 4096
        }
      ]
    }
  }
}
```