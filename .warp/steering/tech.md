# Crush 项目技术栈与开发规范

## 技术栈概览

### 核心技术
- **开发语言**：Go 1.25.0，支持 GreenTeaGC 实验性特性
- **数据库**：SQLite3，使用 `github.com/ncruces/go-sqlite3 v0.28.0`
- **TUI 框架**：Bubble Tea v2 (`github.com/charmbracelet/bubbletea/v2 v2.0.0-beta.4`)
- **CLI 框架**：Cobra (`github.com/spf13/cobra v1.9.1`)
- **日志系统**：`log/slog` 结构化日志
- **配置格式**：JSON 配置文件，支持 JSON Schema 验证
- **错误处理**：标准 Go 错误处理模式

### 主要依赖库

#### LLM Provider SDK
- **OpenAI**：`github.com/openai/openai-go v1.12.0`
- **Anthropic**：`github.com/anthropics/anthropic-sdk-go v1.9.1`
- **Google Gemini**：`google.golang.org/genai v1.21.0`
- **Azure OpenAI**：集成在 OpenAI SDK 中
- **AWS Bedrock**：使用 AWS SDK v2

#### LSP 相关
- **自建 LSP 客户端**：基于 `internal/lsp` 包实现
- **JSON-RPC 2.0**：用于 LSP 协议通信
- **语言服务器支持**：gopls、typescript-language-server、nil 等

#### MCP 集成
- **MCP Go SDK**：`github.com/mark3labs/mcp-go v0.38.0`
- **传输支持**：stdio、HTTP、Server-Sent Events

#### TUI 组件
- **Lipgloss v2**：`github.com/charmbracelet/lipgloss/v2` 样式系统
- **Bubbles v2**：`github.com/charmbracelet/bubbles/v2` UI 组件
- **Glamour v2**：`github.com/charmbracelet/glamour/v2` Markdown 渲染
- **Chroma v2**：`github.com/alecthomas/chroma/v2` 语法高亮

#### 工具库
- **UUID 生成**：`github.com/google/uuid v1.6.0`
- **模糊搜索**：`github.com/sahilm/fuzzy v0.1.1`
- **文件遍历**：`github.com/charlievieth/fastwalk v1.0.12`
- **图像处理**：`github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646`
- **环境变量**：`github.com/joho/godotenv/autoload` 自动加载

## 数据库 Schema

### 数据表结构

#### sessions 表
```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT,
    title TEXT NOT NULL,
    message_count INTEGER NOT NULL DEFAULT 0,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost REAL NOT NULL DEFAULT 0.0,
    updated_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    summary_message_id TEXT
);
```

#### messages 表
```sql
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    parts TEXT NOT NULL DEFAULT '[]',
    model TEXT,
    provider TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);
```

#### files 表
```sql
CREATE TABLE files (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    path TEXT NOT NULL,
    content TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE,
    UNIQUE(path, session_id, version)
);
```

### 索引策略
- **会话索引**：`idx_messages_session_id`、`idx_files_session_id`
- **文件路径索引**：`idx_files_path`
- **时间戳索引**：`idx_messages_created_at`、`idx_files_created_at`

### 触发器
- **自动更新时间戳**：`update_sessions_updated_at`、`update_messages_updated_at`、`update_files_updated_at`
- **消息计数**：`update_session_message_count_on_insert/delete`

## 构建工具与流程

### Task 任务
```bash
# 构建
task build

# 测试
task test

# 代码格式化
task fmt

# 静态检查
task lint

# 修复 lint 问题
task lint-fix

# 开发模式（开启性能分析）
task dev
```

### 环境变量
- **CGO_ENABLED=0**：静态编译，无外部依赖
- **GOEXPERIMENT=greenteagc**：开启 GreenTea 垃圾收集器

### goreleaser 配置
```bash
# 本地快照构建
goreleaser release --snapshot --clean

# 生成 completions 和 man pages
goreleaser build --single-target
```

## 开发规范

### 代码风格

#### 格式化约定
- **使用 gofumpt**：比 gofmt 更严格的格式化规则
- **Import 分组**：标准库、第三方库、项目内部包
- **行长度**：遵循 gofumpt 的默认设置

#### 命名约定
- **包名**：使用小写，简洁且有意义
- **接口**：在使用方定义，保持小而专注
- **类型别名**：使用 type alias 提高可读性，如 `type AgentName string`
- **常量**：使用有类型常量和 iota，在 const 块中分组

#### 错误处理
- **显式返回**：使用 error 作为最后一个返回参数
- **错误包装**：使用 `fmt.Errorf` 进行错误包装
- **Context 传递**：所有异步操作都传递 context.Context

### 项目结构约定
- **internal/ 包**：项目私有代码，不对外暴露
- **组件分离**：按领域功能组织代码结构
- **依赖注入**：使用构造器注入依赖

### 测试约定

#### 测试类型
- **单元测试**：`go test ./...`
- **并发测试**：使用 `t.Parallel()`
- **文件系统测试**：使用 `t.TempDir()` 创建临时目录
- **环境变量**：使用 `t.SetEnv()` 设置测试环境

#### Golden File 测试
- **更新命令**：`go test ./... -update`
- **特定包更新**：`go test ./internal/tui/components/core -update`

#### Mock 提供商
```go
// 测试中启用 mock 提供商
config.UseMockProviders = true
defer func() {
    config.UseMockProviders = false
    config.ResetProviders()
}()
```

### 静态检查

#### golangci-lint 配置
- **配置文件**：`.golangci.yml`
- **运行方式**：`task lint` 或 `golangci-lint run`
- **修复模式**：`task lint-fix` 或 `golangci-lint run --fix`
- **超时设置**：5 分钟超时限制

#### 检查规则
- **代码质量**：循环复杂度、重复代码、未使用变量
- **安全检查**：潜在的安全风险、数据竞争
- **性能检查**：内存分配、循环优化

### 提交约定

#### Commit Message 格式
```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

#### 类型 (type)
- **feat**: 新功能
- **fix**: Bug 修复
- **docs**: 文档更新
- **style**: 代码风格调整
- **refactor**: 代码重构
- **test**: 测试相关
- **chore**: 构建过程或辅助工具变动

#### 范围 (scope) 示例
- **tui**: TUI 界面相关
- **lsp**: LSP 集成
- **llm**: LLM 提供商相关
- **db**: 数据库相关
- **config**: 配置管理

## 配置管理

### 配置文件位置优先级
1. `.crush.json` （项目根目录）
2. `crush.json` （项目根目录）
3. `$HOME/.config/crush/crush.json` （用户配置）

### 最小配置示例
```json
{
  "$schema": "https://charm.land/crush.json",
  "providers": {
    "openai": {
      "type": "openai",
      "base_url": "https://api.openai.com/v1",
      "api_key": "$OPENAI_API_KEY"
    }
  },
  "lsp": {
    "go": {
      "command": "gopls"
    }
  },
  "options": {
    "debug": false,
    "data_directory": ".crush"
  }
}
```

### 环境变量
- **ANTHROPIC_API_KEY**: Anthropic API 密钥
- **OPENAI_API_KEY**: OpenAI API 密钥
- **GEMINI_API_KEY**: Google Gemini API 密钥
- **CRUSH_PROFILE**: 启用性能分析
- **CRUSH_DEBUG**: 启用调试日志

## 性能优化

### 内存管理
- **静态编译**: CGO_ENABLED=0 减少内存占用
- **GreenTeaGC**: 低延迟垃圾收集器
- **池化**: 对频繁分配的对象使用对象池

### I/O 优化
- **并发读取**: 文件系统操作使用 goroutine
- **流式处理**: LLM 响应流式处理
- **缓存策略**: LSP 响应和文件内容缓存

### 数据库优化
- **预备语句**: 使用 sqlc 生成的预备语句
- **事务批处理**: 批量插入和更新
- **连接池**: 单一 SQLite 连接复用

## 调试与监控

### 日志系统
- **结构化日志**: 使用 slog 记录结构化信息
- **日志级别**: DEBUG, INFO, WARN, ERROR
- **日志轮转**: 使用 lumberjack 进行日志轮转

### 性能分析
- **pprof 集成**: HTTP 服务器在 localhost:6060
- **CPU 分析**: `task profile:cpu`
- **内存分析**: `task profile:heap`
- **分配分析**: `task profile:allocs`

### 监控指标
- **响应时间**: LLM API 调用延迟
- **成本跟踪**: Token 使用量和 API 成本
- **系统资源**: CPU 和内存使用情况
