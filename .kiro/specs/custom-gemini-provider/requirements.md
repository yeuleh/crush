# Crush Custom-Gemini HTTP Provider 需求文档

## 项目背景

当前 Crush 项目支持多种 LLM 提供商，包括 Google Gemini（使用 `google.golang.org/genai` SDK）。为了提供更好的控制和灵活性，需要添加一个新的 `custom-gemini` Provider，该 Provider 通过原生 HTTP 请求与 Gemini API 交互，而不依赖官方 SDK。

## 重要约束

1. **非破坏性**: 不能影响现有 Provider 的行为和配置格式
2. **独立实现**: 创建新的 `custom-gemini` 类型，与现有 `gemini` 类型并存
3. **灵活 URL 支持**: 支持两种 base_url 格式
4. **流式模拟**: 对完整 URL 实现流式响应模拟

## 用户故事

### US001: Custom-Gemini Provider 基础支持
**作为** Crush 用户  
**我希望** 能够配置和使用新的 `custom-gemini` Provider  
**以便** 我可以在不依赖官方 SDK 的情况下使用 Gemini 模型

#### 验收条件
- AC001.1: 创建新的 Provider 类型 `custom-gemini`，不影响现有 `gemini` 类型
- AC001.2: 支持通过 JSON 配置文件配置 `custom-gemini` Provider
- AC001.3: 配置格式与现有 Provider 保持一致（base_url, api_key, models 等）
- AC001.4: 能够在 Crush TUI 中选择和使用 `custom-gemini` Provider
- AC001.5: 现有 `gemini` Provider 功能完全不受影响

### US002: 双模式 Base URL 支持
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 支持两种 base_url 格式  
**以便** 我可以灵活配置不同的 API 端点

#### 验收条件
- AC002.1: **标准模式**: base_url 不以 "#" 结尾时，自动拼接 Gemini API 方法路径
  - 例如: `base_url: "https://generativelanguage.googleapis.com/v1beta"` 
  - 实际请求: `{base_url}/models/{model}:generateContent`
- AC002.2: **完整 URL 模式**: base_url 以 "#" 结尾时，直接使用该 URL 作为完整端点
  - 例如: `base_url: "https://custom.api.com/gemini/chat#"`
  - 实际请求: `https://custom.api.com/gemini/chat`（去掉 "#"）
- AC002.3: URL 解析逻辑正确处理两种模式的区别
- AC002.4: 支持环境变量和配置解析的 base_url 格式
- AC002.5: 错误处理：无效 URL 格式时给出清晰错误信息

### US003: 基础消息发送和接收
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 能够发送消息并接收响应  
**以便** 我可以进行基本的 AI 对话

#### 验收条件
- AC003.1: 正确构建 Gemini API 格式的 HTTP 请求体
- AC003.2: 设置正确的 HTTP 头信息（Content-Type, Authorization 等）
- AC003.3: 将 Crush 内部消息格式转换为 Gemini API 格式
- AC003.4: 正确解析 Gemini API 的 JSON 响应
- AC003.5: 将 API 响应转换为 Crush 内部消息格式
- AC003.6: 处理基本的 HTTP 错误和 API 错误响应

### US004: 流式响应支持和模拟
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 支持流式响应，包括对完整 URL 的模拟  
**以便** 我可以实时看到模型的回复

#### 验收条件
- AC004.1: **标准模式流式支持**: 
  - 使用 `{base_url}/models/{model}:streamGenerateContent` 端点
  - 正确处理 SSE (Server-Sent Events) 响应
- AC004.2: **完整 URL 模式流式模拟**:
  - 当 base_url 以 "#" 结尾时，先调用完整 URL 获取完整响应
  - 将完整响应分块模拟成流式响应事件
  - 模拟合理的延迟和分块策略
- AC004.3: 实时更新 TUI 界面显示流式内容
- AC004.4: 正确解析流式响应中的 JSON 数据块
- AC004.5: 处理流式响应的中断、错误和完成事件
- AC004.6: 流式模拟的性能不应明显低于真实流式响应

### US005: 系统消息和上下文管理
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 正确处理系统消息和对话历史  
**以便** 我可以进行连续的上下文对话

#### 验收条件
- AC005.1: 支持 Gemini API 的系统指令 (systemInstruction) 格式
- AC005.2: 正确构建对话历史 (contents) 数组
- AC005.3: 处理用户消息、助手消息的角色转换
- AC005.4: 支持多轮对话的上下文保持
- AC005.5: 处理上下文长度限制和合理截断
- AC005.6: 支持自定义系统提示前缀配置

### US006: Token 使用统计
**作为** Crush 用户  
**我希望** 能够查看准确的 Token 使用情况和成本统计  
**以便** 我可以监控 API 使用情况

#### 验收条件
- AC006.1: 解析 `usageMetadata` 中的 Token 统计信息
- AC006.2: 正确计算 `promptTokenCount`（输入 Token）
- AC006.3: 正确计算 `candidatesTokenCount`（输出 Token）
- AC006.4: 支持 `cachedContentTokenCount`（缓存 Token）
- AC006.5: 将 Token 统计信息存储到数据库
- AC006.6: 在 TUI 中显示 Token 使用情况

### US007: 工具调用支持
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 支持工具调用（Function Calling）  
**以便** 我可以使用 Crush 的工具集成功能

#### 验收条件
- AC007.1: 将 Crush 工具定义转换为 Gemini API 的 `tools` 格式
- AC007.2: 正确处理 `functionCall` 响应类型
- AC007.3: 执行工具调用并构建 `functionResponse` 格式
- AC007.4: 支持多个工具的同时定义和调用
- AC007.5: 处理工具调用的参数验证和错误情况
- AC007.6: 工具调用结果的正确往返处理

### US008: 多模态支持（图像）
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 支持图像输入  
**以便** 我可以进行多模态对话

#### 验收条件
- AC008.1: 支持图像的 base64 编码处理
- AC008.2: 正确设置 `inlineData` 格式（mimeType 和 data）
- AC008.3: 支持常见图像格式（jpeg, png, gif, webp）
- AC008.4: 处理图像大小限制和验证
- AC008.5: 支持文本和图像的组合输入
- AC008.6: 图像数据的内存优化处理

### US009: 错误处理和重试机制
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 具备健壮的错误处理能力  
**以便** 在网络问题或 API 限制时能够自动恢复

#### 验收条件
- AC009.1: 实现指数退避重试机制（最大 8 次重试）
- AC009.2: 处理 HTTP 状态码：429（速率限制）、401/403（认证错误）
- AC009.3: 处理网络超时和连接错误
- AC009.4: 处理 API 密钥过期和刷新
- AC009.5: 解析 Gemini API 错误响应格式
- AC009.6: 避免无限重试和资源泄漏

### US010: 调试和监控支持
**作为** Crush 开发者  
**我希望** `custom-gemini` Provider 提供完整的调试信息  
**以便** 我可以排查和解决问题

#### 验收条件
- AC010.1: 在调试模式下记录 HTTP 请求和响应
- AC010.2: 使用结构化日志记录关键操作
- AC010.3: 提供详细的错误信息和上下文
- AC010.4: 支持性能监控指标（响应时间、重试次数等）
- AC010.5: 遵循项目的日志规范和格式

### US011: 配置验证和兼容性
**作为** Crush 用户  
**我希望** `custom-gemini` Provider 提供清晰的配置验证  
**以便** 我可以快速识别和修复配置问题

#### 验收条件
- AC011.1: 验证必要配置项的存在性（api_key, base_url）
- AC011.2: 验证 base_url 格式的正确性
- AC011.3: 支持与现有配置格式的完全兼容
- AC011.4: 提供配置错误的清晰诊断信息
- AC011.5: 支持配置热重载（如果项目支持）

### US012: 安全防护（后续需求）
**作为** Crush 管理员  
**我希望** `custom-gemini` Provider 具备安全防护能力  
**以便** 在生产环境中安全使用完整URL模式

#### 验收条件
- AC012.1: 实现SSRF防护机制，验证目标主机名
- AC012.2: 支持URL白名单配置
- AC012.3: 禁止访问私网IP段和本地地址
- AC012.4: 限制支持的协议（仅http/https）
- AC012.5: 提供安全审计日志
- AC012.6: 支持安全策略的动态配置

## 技术实现约束

### CON001: 架构约束
- 必须实现现有的 `ProviderClient` 接口
- 必须符合 `catwalk.Type` 类型系统扩展
- 代码结构遵循现有 Provider 实现模式
- 不能修改现有 Provider 接口定义

### CON002: 依赖约束
- 使用 Go 标准库 `net/http` 进行 HTTP 请求
- 不引入额外的第三方 HTTP 客户端库
- 复用现有的日志、配置和错误处理组件
- 使用现有的 `message` 和 `tools` 包定义

### CON003: 性能约束
- HTTP 客户端连接复用和连接池管理
- 流式模拟的内存使用控制
- 错误重试的退避算法优化
- 并发安全的客户端实现

## 实现优先级

### P0: 核心功能（第一个里程碑）
- US001: 基础 Provider 支持
- US002: 双模式 URL 支持  
- US003: 基础消息收发
- US004: 流式响应和模拟
- US005: 系统消息处理
- US006: Token 统计
- US009: 错误处理和重试

### P1: 功能完善（第二个里程碑）
- US010: 调试支持
- US011: 配置验证

### P2: 扩展功能（第三个里程碑）
- US007: 工具调用支持
- US008: 图像支持
- US012: 安全防护（新增）

## 验收标准

### 整体验收标准
1. 所有 P0 和 P1 用户故事的验收条件都已满足
2. 单元测试覆盖率达到 85% 以上
3. 集成测试覆盖两种 base_url 模式
4. 与现有 `gemini` Provider 功能对比测试通过
5. 性能测试：响应时间不超过现有实现的 120%
6. 内存使用测试：流式模拟内存占用可控
7. 错误注入测试：各种异常场景的恢复能力
8. 配置兼容性测试：不影响现有配置的解析和使用
9. 代码审查通过，符合项目编码规范
10. 文档完善，包括两种 URL 模式的配置示例
