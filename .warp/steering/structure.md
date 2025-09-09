# Crush 项目架构与代码结构

## 项目目录结构

### 根目录关键文件
```
crush/
├── main.go                    # 程序入口点，启动 CLI 和 pprof
├── go.mod                     # Go 模块定义
├── go.sum                     # 依赖版本锁定
├── Taskfile.yaml              # Task 构建任务配置
├── .goreleaser.yml            # 发布配置，支持多平台构建
├── .golangci.yml              # 静态检查规则配置
├── crush.json                 # 项目级 LSP 配置示例
├── cspell.json                # 拼写检查配置
├── schema.json                # JSON Schema 定义
├── README.md                  # 项目介绍和使用指南
├── CRUSH.md                   # 开发者指南
├── LICENSE.md                 # FSL-1.1-MIT 协议
└── CLA.md                     # 贡献者协议
```

### 主要目录结构
```
internal/                   # 项目内部代码，不对外暴露
├── app/                    # 应用核心服务层
├── cmd/                    # CLI 命令定义和处理
├── config/                 # 配置管理和解析
├── db/                     # 数据库操作和迁移
├── tui/                    # 终端用户界面组件
├── lsp/                    # LSP 客户端实现
├── llm/                    # LLM 提供商集成
├── message/                # 消息实体和服务
├── session/                # 会话管理
├── history/                # 文件历史管理
├── permission/             # 权限控制系统
└── …                       # 其他辅助模块
```

## 系统架构分层

### 分层设计

#### 1. 入口层 (Entry Layer)
- **文件**：`main.go`
- **职责**：
  - 程序启动和终止
  - 全局 panic 恢复
  - pprof 性能分析服务
  - 自动加载 .env 文件

#### 2. CLI 层 (Command Layer)
- **文件**：`internal/cmd/`
- **职责**：
  - 命令行参数解析
  - 子命令定义（run, logs, schema）
  - 应用初始化和配置加载
  - 交互式和非交互式模式调度

#### 3. 应用服务层 (Application Service Layer)
- **文件**：`internal/app/`
- **职责**：
  - 应用生命周期管理
  - 服务组装和依赖注入
  - 事件发布和订阅协调
  - LSP 客户端生命周期管理

#### 4. 领域服务层 (Domain Service Layer)
- **文件**：`internal/{session,message,history,permission}/`
- **职责**：
  - 业务逻辑封装
  - 数据访问抽象
  - 领域事件处理
  - 跨领域协调

#### 5. 基础设施层 (Infrastructure Layer)
- **文件**：`internal/{db,lsp,llm,config}/`
- **职责**：
  - 数据持久化
  - 外部 API 集成
  - 配置管理
  - 日志和监控

#### 6. 表示层 (Presentation Layer)
- **文件**：`internal/tui/`
- **职责**：
  - 用户界面渲染
  - 用户交互处理
  - 状态管理
  - 事件分发

## 核心模块详解

### TUI 模块 (internal/tui/)

#### 组件层次结构
```
tui/
├── page/                   # 页面级组件
│   └── chat/               # 主聊天页面
├── components/             # UI 组件库
│   ├── chat/               # 聊天相关组件
│   │   ├── editor/         # 消息编辑器
│   │   ├── messages/       # 消息列表
│   │   ├── header/         # 页面头部
│   │   ├── sidebar/        # 侧边栏
│   │   └── splash/         # 欢迎界面
│   ├── dialogs/            # 对话框组件
│   ├── core/               # 核心组件和布局
│   └── …                   # 其他专用组件
├── styles/                 # 主题和样式系统
└── util/                   # TUI 工具函数
```

#### 主要功能
- **响应式布局**：自适应终端尺寸，支持紧凑模式
- **键盘导航**：完整的 Vim 风格键位支持
- **主题系统**：可定制的颜色和样式
- **状态管理**：基于 Bubble Tea 的状态机模式

### LSP 模块 (internal/lsp/)

#### 核心组件
```
lsp/
├── client.go               # LSP 客户端主接口
├── transport.go            # JSON-RPC 2.0 传输层
├── protocol.go             # LSP 协议消息定义
├── methods.go              # LSP 方法名常量
├── handlers.go             # 事件处理器
├── caps.go                 # 客户端能力声明
└── watcher/                # 文件监视器
```

#### 主要功能
- **多语言支持**：可配置的语言服务器集成
- **生命周期管理**：自动启停和错误恢复
- **实时同步**：文件内容变更同步
- **事件驱动**：诊断信息、补全建议实时更新

### LLM 模块 (internal/llm/)

#### 模块组织
```
llm/
├── agent/                  # AI 代理和工具调用
│   ├── agent.go            # 主代理服务
│   ├── agent-tool.go       # 工具调用处理
│   └── mcp-tools.go        # MCP 工具集成
├── prompt/                 # Prompt 管理和组装
│   ├── coder.go            # 编程助手 prompt
│   ├── summarizer.go       # 总结生成器
│   └── task.go             # 任务型 prompt
└── provider/               # 提供商集成
    ├── openai.go           # OpenAI API 集成
    ├── anthropic.go        # Anthropic API 集成
    ├── gemini.go           # Google Gemini 集成
    └── …                   # 其他提供商
```

#### 主要功能
- **统一接口**：抽象化的 Provider 接口
- **流式响应**：支持 SSE 流式数据处理
- **上下文管理**：智能的上下文窗口和截断策略
- **成本计算**：实时 token 使用统计和成本评估

### 数据层 (internal/db/)

#### 文件组织
```
db/
├── connect.go              # 数据库连接管理
├── db.go                   # sqlc 生成的查询接口
├── models.go               # 数据模型定义
├── *.sql.go                # 生成的 SQL 操作代码
├── migrations/             # 数据库迁移脚本
│   ├── 20250424200609_initial.sql
│   ├── 20250515105448_add_summary_message_id.sql
│   └── 20250627000000_add_provider_to_messages.sql
└── sql/                    # SQL 查询定义
```

#### 数据访问特性
- **类型安全**：使用 sqlc 生成类型安全的 Go 代码
- **事务支持**：支持 ACID 事务操作
- **自动迁移**：使用 goose 进行数据库版本管理
- **连接池**：单一连接复用，避免连接泄漏

## 数据流和组件交互

### 主要数据流

#### 1. 用户输入流
```
用户输入 → TUI Editor → 消息验证 → Session Service
    ↓
LLM Provider ← Prompt Assembly ← Context Gathering ← Message Service
    ↓
流式响应 → Message Service → TUI Messages → 用户界面
```

#### 2. LSP 集成流
```
文件变更 → LSP Watcher → LSP Client → Language Server
    ↓
诊断结果 → LSP Events → Context Service → AI Prompt
```

#### 3. 数据持久化流
```
业务操作 → Domain Service → Repository → SQLite DB
    ↓
触发器 → 统计更新 → 事件发布 → UI 更新
```

### 事件系统

#### 事件发布模式
- **事件总线**：基于 `internal/pubsub` 的内存事件系统
- **异步处理**：事件处理不阻塞 UI 线程
- **事件类型**：消息更新、会话变更、LSP 事件等

#### 错误处理和日志
- **错误传播**：使用 Go error 接口向上传播
- **日志聚合**：统一的日志记录和输出
- **恢复机制**：Graceful degradation 和错误恢复

## 关键依赖关系

### 模块依赖关系图

#### 垂直依赖 (从上到下)
```
main → cmd → app → {session,message,history,permission}
                 ↓
              {db,lsp,llm,config} ← tui
```

#### 水平依赖 (同层交互)
```
session ↔ message    # 会话与消息相互引用
history ↔ session    # 文件历史与会话关联
llm → lsp            # LLM 使用 LSP 上下文
permission → llm      # 权限控制 LLM 操作
```

### 接口设计原则

#### 依赖倒置 (Dependency Inversion)
- **抽象接口**：高层模块定义接口，低层模块实现
- **注入模式**：通过构造器注入依赖
- **接口分离**：只暴露必要的方法，保持接口精简

#### 跨模块通信
- **事件驱动**：使用事件发布/订阅减少耦合
- **Context 传递**：使用 context.Context 传递取消信号
- **错误边界**：在模块边界处理和转换错误

### 性能和可扩展性考量

#### 并发模型
- **Goroutine 池**：控制并发数量，防止资源耗尽
- **管道通信**：使用 channel 进行线程间通信
- **取消机制**：Context-based 取消传播

#### 缓存策略
- **内存缓存**：LSP 响应和配置数据缓存
- **懒加载**：按需加载组件和资源
- **TTL 管理**：适当的缓存过期策略

#### 扩展点设计
- **插件架构**：LLM Provider 和 LSP 客户端扩展
- **配置驱动**：通过配置启用/禁用功能
- **主题系统**：可定制的 UI 主题和风格

## 部署和运维考量

### 单二进制部署
- **静态编译**：无外部依赖，可直接运行
- **跨平台**：支持多操作系统和架构
- **配置管理**：支持多层级配置覆盖

### 数据管理
- **本地存储**：数据存储在用户本地，保护隐私
- **备份策略**：SQLite 文件可直接复制备份
- **数据迁移**：自动数据库版本升级

### 监控和调试
- **日志集成**：统一的日志输出和管理
- **指标收集**：成本、性能、错误率统计
- **调试支持**：pprof 集成和调试模式
