# Crush Custom-Gemini Provider 设计文档

## 架构概览

### 整体架构
`custom-gemini` Provider 作为新的独立 Provider 类型，完全通过 HTTP 请求与 Gemini API 交互，不依赖 `google.golang.org/genai` SDK。

```
┌─────────────────────────────────────────────────────────────┐
│                    Crush Application                        │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────────────┐  ┌─────────────────────────────────┤
│  │  Existing Providers │  │     Custom-Gemini Provider      │
│  │  ┌─────────────────┐│  │  ┌───────────────────────────── │
│  │  │ gemini (genai)  ││  │  │ customGeminiClient          │
│  │  │ openai          ││  │  │  ├── urlResolver            │
│  │  │ anthropic       ││  │  │  ├── httpClient             │
│  │  │ ...             ││  │  │  ├── requestBuilder         │
│  │  └─────────────────┘│  │  │  ├── responseParser         │
│  └─────────────────────┘  │  │  └── streamSimulator        │
│                           │  └─────────────────────────────  │
└───────────────────────────┴─────────────────────────────────┘
                            │
                            ▼
                  ┌─────────────────────────────────────────────┐
                  │           Gemini API                        │
                  │  ┌───────────────────┬──────────────────── │
                  │  │  Standard Mode    │  Complete URL Mode  │
                  │  │  /v1beta/models   │  Custom Endpoint    │
                  │  │  /{model}:        │  (Direct Access)   │
                  │  │  generateContent  │                     │
                  │  │  streamGenerate   │                     │
                  │  │  Content          │                     │
                  │  └───────────────────┴──────────────────── │
                  └─────────────────────────────────────────────┘
```

## 核心组件设计

### 1. Provider 类型系统扩展

#### catwalk.Type 扩展
```go
// 在 catwalk 包或相应位置添加新类型
const (
    // ... 现有类型
    TypeCustomGemini catwalk.Type = "custom-gemini"
)
```

#### Provider 工厂扩展
```go
// internal/llm/provider/provider.go
func NewProvider(cfg config.ProviderConfig, opts ...ProviderClientOption) (Provider, error) {
    // ... 现有代码
    switch cfg.Type {
    // ... 现有 case
    case "custom-gemini": // 使用字符串常量，避免修改 catwalk 包
        return &baseProvider[CustomGeminiClient]{
            options: clientOptions,
            client:  newCustomGeminiClient(clientOptions),
        }, nil
    }
    // ...
}
```

### 2. URL 解析器设计

#### URL 模式检测
```go
type urlMode int

const (
    urlModeStandard urlMode = iota  // 标准模式：拼接路径
    urlModeComplete                 // 完整模式：直接使用
)

type urlResolver struct {
    baseURL string
    mode    urlMode
}

func newURLResolver(baseURL string) (*urlResolver, error) {
    resolver := &urlResolver{baseURL: baseURL}
    
    // 检测 URL 模式
    if strings.HasSuffix(baseURL, "#") {
        resolver.mode = urlModeComplete
        resolver.baseURL = strings.TrimSuffix(baseURL, "#")
    } else {
        resolver.mode = urlModeStandard
    }
    
    // 验证 URL 格式
    if _, err := url.Parse(resolver.baseURL); err != nil {
        return nil, fmt.Errorf("invalid base URL: %w", err)
    }
    
    return resolver, nil
}

func (r *urlResolver) buildURL(model string, operation string) string {
    switch r.mode {
    case urlModeStandard:
        // https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent
        return fmt.Sprintf("%s/models/%s:%s", r.baseURL, model, operation)
    case urlModeComplete:
        // https://custom.api.com/gemini/chat (直接使用完整 URL)
        return r.baseURL
    default:
        return r.baseURL
    }
}
```

### 3. HTTP 客户端实现

#### 核心客户端结构
```go
type customGeminiClient struct {
    providerOptions providerClientOptions
    httpClient      *http.Client
    urlResolver     *urlResolver
}

type CustomGeminiClient ProviderClient

func newCustomGeminiClient(opts providerClientOptions) CustomGeminiClient {
    resolver, err := newURLResolver(opts.baseURL)
    if err != nil {
        slog.Error("Failed to create URL resolver", "error", err)
        return nil
    }
    
    httpClient := &http.Client{
        Timeout: 30 * time.Second,
    }
    
    // 在调试模式下使用日志 HTTP 客户端
    if config.Get().Options.Debug {
        httpClient = log.NewHTTPClient()
    }
    
    return &customGeminiClient{
        providerOptions: opts,
        httpClient:      httpClient,
        urlResolver:     resolver,
    }
}
```

### 4. 请求构建器

#### Gemini API 请求格式
```go
// Gemini API 请求结构定义
type geminiRequest struct {
    Contents          []geminiContent    `json:"contents"`
    SystemInstruction *geminiContent     `json:"systemInstruction,omitempty"`
    Tools             []geminiTool       `json:"tools,omitempty"`
    GenerationConfig  *generationConfig  `json:"generationConfig,omitempty"`
}

type geminiContent struct {
    Role  string        `json:"role"`
    Parts []geminiPart  `json:"parts"`
}

type geminiPart struct {
    Text         string                 `json:"text,omitempty"`
    InlineData   *geminiInlineData      `json:"inlineData,omitempty"`
    FunctionCall *geminiFunctionCall    `json:"functionCall,omitempty"`
    FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
    MimeType string `json:"mimeType"`
    Data     string `json:"data"` // base64 encoded
}

type geminiFunctionCall struct {
    Name string                 `json:"name"`
    Args map[string]interface{} `json:"args"`
}

type geminiFunctionResponse struct {
    Name     string                 `json:"name"`
    Response map[string]interface{} `json:"response"`
}

type generationConfig struct {
    MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}
```

#### 消息转换逻辑
```go
func (c *customGeminiClient) convertMessages(messages []message.Message) (*geminiRequest, error) {
    request := &geminiRequest{
        Contents: make([]geminiContent, 0),
    }
    
    // 处理系统消息
    systemMessage := c.providerOptions.systemMessage
    if c.providerOptions.systemPromptPrefix != "" {
        systemMessage = c.providerOptions.systemPromptPrefix + "\n" + systemMessage
    }
    
    if systemMessage != "" {
        request.SystemInstruction = &geminiContent{
            Role: "user", // Gemini 的系统指令格式
            Parts: []geminiPart{{Text: systemMessage}},
        }
    }
    
    // 转换对话消息
    for _, msg := range messages {
        content := c.convertMessage(msg)
        if content != nil {
            request.Contents = append(request.Contents, *content)
        }
    }
    
    return request, nil
}

func (c *customGeminiClient) convertMessage(msg message.Message) *geminiContent {
    switch msg.Role {
    case message.User:
        return c.convertUserMessage(msg)
    case message.Assistant:
        return c.convertAssistantMessage(msg)
    case message.Tool:
        return c.convertToolMessage(msg)
    default:
        return nil
    }
}
```

### 5. 响应解析器

#### Gemini API 响应格式
```go
type geminiResponse struct {
    Candidates    []geminiCandidate `json:"candidates"`
    UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}

type geminiCandidate struct {
    Content      geminiContent     `json:"content"`
    FinishReason string           `json:"finishReason"`
}

type geminiUsage struct {
    PromptTokenCount     int `json:"promptTokenCount"`
    CandidatesTokenCount int `json:"candidatesTokenCount"`
    CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// 流式响应格式
type geminiStreamChunk struct {
    Candidates    []geminiCandidate `json:"candidates,omitempty"`
    UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}
```

#### 响应解析实现
```go
func (c *customGeminiClient) parseResponse(body []byte) (*ProviderResponse, error) {
    var response geminiResponse
    if err := json.Unmarshal(body, &response); err != nil {
        return nil, fmt.Errorf("failed to parse response: %w", err)
    }
    
    if len(response.Candidates) == 0 {
        return nil, fmt.Errorf("no candidates in response")
    }
    
    candidate := response.Candidates[0]
    content := ""
    toolCalls := make([]message.ToolCall, 0)
    
    // 解析内容和工具调用
    for _, part := range candidate.Content.Parts {
        if part.Text != "" {
            content += part.Text
        }
        if part.FunctionCall != nil {
            toolCall := c.convertToToolCall(part.FunctionCall)
            toolCalls = append(toolCalls, toolCall)
        }
    }
    
    return &ProviderResponse{
        Content:      content,
        ToolCalls:    toolCalls,
        Usage:        c.convertUsage(response.UsageMetadata),
        FinishReason: c.convertFinishReason(candidate.FinishReason),
    }, nil
}
```

### 6. 流式响应处理

#### 标准模式流式处理
```go
func (c *customGeminiClient) streamStandard(ctx context.Context, request *geminiRequest, model string) <-chan ProviderEvent {
    eventChan := make(chan ProviderEvent)
    
    go func() {
        defer close(eventChan)
        
        url := c.urlResolver.buildURL(model, "streamGenerateContent")
        
        req, err := c.buildHTTPRequest(ctx, "POST", url, request)
        if err != nil {
            eventChan <- ProviderEvent{Type: EventError, Error: err}
            return
        }
        
        resp, err := c.httpClient.Do(req)
        if err != nil {
            eventChan <- ProviderEvent{Type: EventError, Error: err}
            return
        }
        defer resp.Body.Close()
        
        // 处理 SSE 流
        scanner := bufio.NewScanner(resp.Body)
        eventChan <- ProviderEvent{Type: EventContentStart}
        
        for scanner.Scan() {
            line := scanner.Text()
            if !strings.HasPrefix(line, "data: ") {
                continue
            }
            
            data := strings.TrimPrefix(line, "data: ")
            if data == "[DONE]" {
                break
            }
            
            event := c.parseStreamChunk(data)
            if event != nil {
                eventChan <- *event
            }
        }
        
        eventChan <- ProviderEvent{Type: EventContentStop}
        // 发送完成事件...
    }()
    
    return eventChan
}
```

#### 完整 URL 模式流式模拟
```go
func (c *customGeminiClient) streamSimulated(ctx context.Context, request *geminiRequest, model string) <-chan ProviderEvent {
    eventChan := make(chan ProviderEvent)
    
    go func() {
        defer close(eventChan)
        
        // 首先调用完整 URL 获取完整响应
        response, err := c.sendRequest(ctx, request, model)
        if err != nil {
            eventChan <- ProviderEvent{Type: EventError, Error: err}
            return
        }
        
        // 模拟流式响应
        c.simulateStream(response, eventChan)
    }()
    
    return eventChan
}

func (c *customGeminiClient) simulateStream(response *ProviderResponse, eventChan chan<- ProviderEvent) {
    eventChan <- ProviderEvent{Type: EventContentStart}
    
    content := response.Content
    if content == "" {
        eventChan <- ProviderEvent{Type: EventContentStop}
        eventChan <- ProviderEvent{Type: EventComplete, Response: response}
        return
    }
    
    // 分块策略：按单词分割，每次发送 1-3 个单词
    words := strings.Fields(content)
    currentPos := 0
    
    for currentPos < len(words) {
        // 随机选择每次发送的单词数量
        chunkSize := rand.Intn(3) + 1
        if currentPos+chunkSize > len(words) {
            chunkSize = len(words) - currentPos
        }
        
        chunk := strings.Join(words[currentPos:currentPos+chunkSize], " ")
        if currentPos+chunkSize < len(words) {
            chunk += " " // 添加空格，除了最后一块
        }
        
        eventChan <- ProviderEvent{
            Type:    EventContentDelta,
            Content: chunk,
        }
        
        currentPos += chunkSize
        
        // 模拟延迟
        select {
        case <-time.After(time.Duration(rand.Intn(100)+50) * time.Millisecond):
        case <-context.Background().Done():
            return
        }
    }
    
    eventChan <- ProviderEvent{Type: EventContentStop}
    eventChan <- ProviderEvent{Type: EventComplete, Response: response}
}
```

### 7. 错误处理和重试

#### 重试策略实现
```go
func (c *customGeminiClient) shouldRetry(attempts int, err error) (bool, time.Duration, error) {
    if attempts > maxRetries {
        return false, 0, fmt.Errorf("maximum retry attempts reached: %w", err)
    }
    
    // 检查错误类型
    var httpErr *HTTPError
    if errors.As(err, &httpErr) {
        switch httpErr.StatusCode {
        case http.StatusTooManyRequests: // 429
            // 从响应头获取 Retry-After
            retryAfter := c.parseRetryAfter(httpErr.Headers)
            return true, retryAfter, nil
        case http.StatusUnauthorized, http.StatusForbidden: // 401, 403
            // 尝试刷新 API 密钥
            if err := c.refreshAPIKey(); err != nil {
                return false, 0, fmt.Errorf("failed to refresh API key: %w", err)
            }
            return true, 0, nil
        case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
            // 5xx 错误使用指数退避
            backoff := time.Duration(math.Pow(2, float64(attempts-1))) * time.Second
            jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
            return true, backoff + jitter, nil
        }
    }
    
    // 网络错误
    if isNetworkError(err) {
        backoff := time.Duration(math.Pow(2, float64(attempts-1))) * time.Second
        return true, backoff, nil
    }
    
    return false, 0, err
}
```

## 数据库 Schema

现有 Schema 无需修改，`custom-gemini` Provider 使用相同的数据表结构：
- `sessions` 表：存储会话信息和 token 统计
- `messages` 表：`provider` 字段将存储 "custom-gemini" 
- `files` 表：文件历史记录（无变化）

## 配置格式

### 配置示例

#### 标准模式配置
```json
{
  "providers": {
    "my-custom-gemini": {
      "type": "custom-gemini",
      "base_url": "https://generativelanguage.googleapis.com/v1beta",
      "api_key": "$GEMINI_API_KEY",
      "models": [
        {
          "id": "gemini-pro",
          "name": "Gemini Pro",
          "context_window": 32768,
          "default_max_tokens": 8192
        }
      ]
    }
  }
}
```

#### 完整 URL 模式配置
```json
{
  "providers": {
    "my-proxy-gemini": {
      "type": "custom-gemini", 
      "base_url": "https://my-proxy.com/api/gemini/chat#",
      "api_key": "$PROXY_API_KEY",
      "models": [
        {
          "id": "gemini-pro",
          "name": "Gemini Pro (via Proxy)",
          "context_window": 32768,
          "default_max_tokens": 8192
        }
      ]
    }
  }
}
```

## 测试策略

### 单元测试

#### 1. URL 解析器测试
```go
func TestURLResolver(t *testing.T) {
    tests := []struct {
        name        string
        baseURL     string
        expectedMode urlMode
        expectedURL  string
    }{
        {
            name:        "standard mode",
            baseURL:     "https://api.example.com/v1",
            expectedMode: urlModeStandard,
            expectedURL:  "https://api.example.com/v1/models/gemini-pro:generateContent",
        },
        {
            name:        "complete mode",
            baseURL:     "https://proxy.com/chat#",
            expectedMode: urlModeComplete,
            expectedURL:  "https://proxy.com/chat",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            resolver, err := newURLResolver(tt.baseURL)
            require.NoError(t, err)
            assert.Equal(t, tt.expectedMode, resolver.mode)
            
            actualURL := resolver.buildURL("gemini-pro", "generateContent")
            assert.Equal(t, tt.expectedURL, actualURL)
        })
    }
}
```

#### 2. 消息转换测试
```go
func TestMessageConversion(t *testing.T) {
    client := &customGeminiClient{
        providerOptions: providerClientOptions{
            systemMessage: "You are a helpful assistant",
        },
    }
    
    messages := []message.Message{
        {
            Role: message.User,
            Parts: []message.Part{
                {Content: message.TextContent{Text: "Hello"}},
            },
        },
    }
    
    request, err := client.convertMessages(messages)
    require.NoError(t, err)
    
    assert.NotNil(t, request.SystemInstruction)
    assert.Equal(t, "You are a helpful assistant", request.SystemInstruction.Parts[0].Text)
    assert.Len(t, request.Contents, 1)
    assert.Equal(t, "user", request.Contents[0].Role)
}
```

#### 3. 流式模拟测试
```go
func TestStreamSimulation(t *testing.T) {
    client := &customGeminiClient{}
    response := &ProviderResponse{
        Content: "This is a test response with multiple words",
    }
    
    eventChan := make(chan ProviderEvent, 10)
    go client.simulateStream(response, eventChan)
    
    var events []ProviderEvent
    for event := range eventChan {
        events = append(events, event)
    }
    
    assert.Equal(t, EventContentStart, events[0].Type)
    assert.Equal(t, EventContentStop, events[len(events)-2].Type)
    assert.Equal(t, EventComplete, events[len(events)-1].Type)
    
    // 验证内容完整性
    var fullContent string
    for _, event := range events {
        if event.Type == EventContentDelta {
            fullContent += event.Content
        }
    }
    assert.Equal(t, response.Content, fullContent)
}
```

### 集成测试

#### 模拟 Gemini API 服务器
```go
func setupMockGeminiServer(t *testing.T) *httptest.Server {
    mux := http.NewServeMux()
    
    // 标准模式端点
    mux.HandleFunc("/v1beta/models/gemini-pro:generateContent", func(w http.ResponseWriter, r *http.Request) {
        response := geminiResponse{
            Candidates: []geminiCandidate{
                {
                    Content: geminiContent{
                        Parts: []geminiPart{{Text: "Hello, how can I help you?"}},
                    },
                    FinishReason: "STOP",
                },
            },
            UsageMetadata: &geminiUsage{
                PromptTokenCount:     10,
                CandidatesTokenCount: 8,
            },
        }
        json.NewEncoder(w).Encode(response)
    })
    
    // 流式端点
    mux.HandleFunc("/v1beta/models/gemini-pro:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        w.WriteHeader(http.StatusOK)
        
        chunks := []string{"Hello", ", how", " can I", " help you?"}
        for _, chunk := range chunks {
            fmt.Fprintf(w, "data: %s\n\n", createStreamChunk(chunk))
            w.(http.Flusher).Flush()
            time.Sleep(50 * time.Millisecond)
        }
        fmt.Fprint(w, "data: [DONE]\n\n")
    })
    
    // 完整 URL 模式端点
    mux.HandleFunc("/custom/chat", func(w http.ResponseWriter, r *http.Request) {
        // 处理完整 URL 模式请求
    })
    
    return httptest.NewServer(mux)
}
```

### 测试数据

#### 标准测试用例
```go
var testMessages = []message.Message{
    {
        Role: message.User,
        Parts: []message.Part{
            {Content: message.TextContent{Text: "What is Go programming language?"}},
        },
    },
}

var expectedGeminiRequest = geminiRequest{
    Contents: []geminiContent{
        {
            Role:  "user",
            Parts: []geminiPart{{Text: "What is Go programming language?"}},
        },
    },
    SystemInstruction: &geminiContent{
        Role:  "user",
        Parts: []geminiPart{{Text: "You are a helpful programming assistant"}},
    },
}
```

## 性能考量

### 1. HTTP 连接复用
- 使用 `http.Client` 的连接池
- 设置合理的 `MaxIdleConns` 和 `MaxIdleConnsPerHost`
- 复用 HTTP 客户端实例

### 2. 流式模拟优化
- 分块大小动态调整
- 使用 buffered channel 避免阻塞
- 内存友好的分块策略

### 3. 错误处理优化  
- 缓存重试状态，避免重复计算
- 使用 context 支持取消和超时
- 合理的退避算法，避免雪崩

### 4. 内存管理
- 及时释放大型响应数据
- 使用流式处理减少内存峰值
- 避免不必要的数据复制

这个设计文档为实现提供了完整的技术路线图，确保 `custom-gemini` Provider 能够满足所有需求，同时保持与现有系统的兼容性。
