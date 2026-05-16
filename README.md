# KnowCompact

**KnowCompact** 是 Go 语言的 LLM Agent 上下文压缩插件，4 层递进压缩（规则清理 -> 阈值判断 -> LLM 摘要 -> 降级截断），专为 [eino](https://github.com/cloudwego/eino) v0.8+ 设计。

## 安装

```bash
go get github.com/huangzheng2016/KnowCompact
```

## 快速开始

完整可运行的示例：

```go
package main

import (
    "context"
    "os"

    "github.com/cloudwego/eino/adk"
    "github.com/cloudwego/eino/schema"
    openai "github.com/cloudwego/eino-ext/components/model/openai"

    "github.com/huangzheng2016/KnowCompact/compact"
    einocompact "github.com/huangzheng2016/KnowCompact/eino"
)

func main() {
    ctx := context.Background()

    // 1. 创建 ChatModel（OpenAI 兼容格式）
    chatModel, _ := openai.NewChatModel(ctx, &openai.ChatModelConfig{
        Model:   "gpt-4o",                         // 你的模型名
        APIKey:  os.Getenv("OPENAI_API_KEY"),      // API Key
        BaseURL: os.Getenv("OPENAI_BASE_URL"),     // 可选，默认 OpenAI 官方
    })

    // 2. 一行创建压缩器（复用 ChatModel 做摘要）
    config := compact.DefaultCompactionConfig()
    compactor := einocompact.NewDefaultCompactorWithEinoModel(config, chatModel, "gpt-4o")

    // 3. 创建中间件（按需选用，eino 内置）
    // patchMw  := patchtoolcalls.NewMiddleware()       // 修补悬空 ToolCall
    // fsMw     := fsmw.NewMiddleware(fsBackend)        // 文件系统工具 (ls/read/write/edit/glob/grep)
    // skillMw  := skillmw.NewMiddleware(fsBackend)     // Skill.md 技能加载
    // planMw   := plantaskmw.NewMiddleware(backend)    // 任务管理 (TaskCreate/Get/Update/List)
    // reduceMw := reduction.NewMiddleware(counter)     // 工具结果截断与清理
    mw := einocompact.NewCompactMiddleware(compactor, 0)  // 0 = 使用默认值 256K，或传入模型实际窗口

    // 4. 创建 Agent，注册中间件
    agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
        Name:  "my-agent",
        Model: chatModel,
        Tools: myTools,
        Handlers: []adk.ChatModelAgentMiddleware{
            // patchMw, fsMw, skillMw, planMw, reduceMw,
            mw,
        },
    })

    // 5. 运行
    events := agent.Run(ctx, &adk.AgentInput{
        Messages: []*schema.Message{
            {Role: schema.User, Content: "帮我分析项目依赖"},
        },
    })
    // ... 处理 events
}
```

环境变量：

```bash
export OPENAI_API_KEY="sk-xxx"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # 可选，第三方兼容端点才需要
```

**`NewDefaultCompactorWithEinoModel`** 一行开启：
- Layer 1 MicroCompact（清理旧工具输出，<1ms）
- Layer 2 AutoCompact（阈值判断 + 断路器）
- Layer 3 Full Compact（LLM 生成摘要，复用 Agent ChatModel）

---

## 工作原理

每轮模型调用前，Middleware 自动执行：

```
[原始消息] -> Layer 1: MicroCompact -> Layer 2: AutoCompact -> [ChatModel]
                  |                        |
            清理旧工具输出              超过阈值时:
            (<1ms, 不丢语义)          -> Layer 3: Full Compact (LLM 摘要, 5-30s)
```

| 层级 | 名称 | 默认 | 作用 |
|------|------|------|------|
| Layer 1 | MicroCompact | 开 | 规则清理旧工具输出，保留最近 3 个 |
| Layer 2 | AutoCompact | 开 | 超过阈值触发，先尝试 Session Memory |
| Layer 3 | Full Compact | 开（需 Summarizer）| LLM 生成结构化摘要 |
| Layer 4 | Session Memory | 关（需 Store）| 复用已有记忆，<10ms |

---

## 配置

```go
config := compact.DefaultCompactionConfig().
    WithRecentToolsKeep(2).                    // 保留最近 2 个工具结果
    AppendFileReadToolNames("MyRead", "Show"). // 追加文件读取工具名
    WithAutoCompactBufferTokens(8_000)         // 降低压缩触发阈值
```

Builder 方法一览：

```go
config := compact.DefaultCompactionConfig().
    WithAutoCompactBufferTokens(10_000).       // 缓冲 tokens（默认 13K）
    WithMaxOutputTokensForSummary(10_000).     // 摘要输出上限（默认 20K）
    WithRecentToolsKeep(2).                    // 保留工具结果数（默认 3）
    WithRecentFilesMax(5).                     // 跟踪文件数（默认 5）
    WithRecentFileMaxTokens(5_000).            // 单文件上限（默认 5K）
    WithFileReinjectBudget(50_000).            // 附件总预算（默认 50K）
    WithFallbackTruncateRatio(0.25).           // 降级保留比例（默认 0.3）
    WithFallbackTruncateMinKeep(6).            // 降级最少保留（默认 4）
    WithFullCompactMaxRetries(2).              // 重试次数（默认 3）
    WithMicroCompactWhitelist(map[string]bool{ // 微压缩白名单
        "Read": true,
        "Bash": true,
    }).
    WithMicroCompactCaseInsensitive(true)      // 白名单大小写不敏感（默认 true）
```

---

## 轻量模型做压缩（降低成本）

如果 Agent 用的大模型成本较高，可用轻量模型专门做压缩摘要：

```go
compactModel, _ := openai.NewChatModel(ctx, &openai.ChatModelConfig{Model: "gpt-4o-mini"})
summarizer := einocompact.NewSummarizerFromEinoModel(compactModel, "gpt-4o-mini")
compactor := compact.NewDefaultCompactor(config, summarizer, nil)
```

---

## 启用 Session Memory（Layer 4）

Layer 4 复用先前累积的摘要快照（<10ms），命中时绕过 LLM。需要传入 `SessionMemoryStore`：

```go
store := compact.NewInMemorySessionMemoryStore()  // 内置内存实现（线程安全）
summarizer := einocompact.NewSummarizerFromEinoModel(chatModel, "gpt-4o")
compactor := compact.NewDefaultCompactor(config, summarizer, store)
```

需要持久化时，实现 `compact.SessionMemoryStore` 接口对接 Redis / 文件 / SQL 即可。

---

## 完整示例

- [`examples/eino_demo/`](examples/eino_demo/) —— eino Middleware 端到端集成（Mock 模型 + 真实 API）
- [`examples/failure_scenarios/`](examples/failure_scenarios/) —— 异常路径演示（PTL、断路器、PreCompact 中止、自定义 detector）

```bash
cd examples/eino_demo
go mod tidy
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # OpenAI 官方端点
go run .
```

Mock 测试不需要 API Key，自动验证压缩逻辑。

```bash
go run ./examples/failure_scenarios   # 演示降级、PTL、断路器、Hook 中止
```

---

## 一行接入（最短路径）

```go
mw := einocompact.NewEinoMiddleware(chatModel, "claude-opus-4-7")
// 默认配置 + 自动选择 ModelPreset。同时复用 chatModel 作为摘要器，省一次模型创建。
```

需要覆盖配置时：

```go
cfg := compact.PresetForScenario(compact.ScenarioCodingAgent).
    WithLogLevel(compact.LogLevelWarn)
mw := einocompact.NewEinoMiddlewareWithConfig(cfg, chatModel, "claude-sonnet-4-6")
```

可用场景预设：`ScenarioCodingAgent` / `ScenarioCustomerSupport` / `ScenarioResearchAnalysis` / `ScenarioLightweight`。

---

## 日志与可观测性

KnowCompact 内置基于接口的 Logger，可对接 slog / zap / logrus / 自家系统：

```go
cfg := compact.DefaultCompactionConfig().
    WithLogger(myLogger).        // 实现 compact.Logger 接口
    WithLogLevel(compact.LogLevelInfo)
```

实现 slog 适配器只需 4 行：

```go
type slogAdapter struct{ L *slog.Logger }
func (a slogAdapter) Debug(m string, args ...any) { a.L.Debug(m, args...) }
func (a slogAdapter) Info(m string, args ...any)  { a.L.Info(m, args...) }
func (a slogAdapter) Warn(m string, args ...any)  { a.L.Warn(m, args...) }
func (a slogAdapter) Error(m string, args ...any) { a.L.Error(m, args...) }
```

未传入 Logger 时使用基于 stdlib `log` 的默认实现，受 `LogLevel` 控制（零值=Info）。

---

## 生命周期 Hook

```go
cfg := compact.DefaultCompactionConfig().
    WithPreCompact(func(ctx context.Context, msgs []compact.Message) ([]compact.Message, error) {
        // 敏感信息脱敏 / pinned 注入 / 拒绝压缩
        return scrub(msgs), nil
    }).
    WithPostCompact(func(ctx context.Context, r *compact.CompactionResult) (*compact.CompactionResult, error) {
        metrics.Record(r.Trigger, r.TokensBefore, r.TokensAfter)
        return r, nil
    })
```

PreCompact 返回 error 时整次压缩中止；PostCompact 返回 error 会被视为压缩失败（计入断路器）。

---

## 自定义 Prompt-Too-Long 判定

不同 SDK 的错误结构千差万别。推荐用法是用哨兵错误包裹：

```go
return fmt.Errorf("openai: %w", compact.ErrPromptTooLong)
```

或者注入识别函数：

```go
cfg := compact.DefaultCompactionConfig().
    WithPromptTooLongDetector(func(err error) bool {
        var apiErr *openai.APIError
        return errors.As(err, &apiErr) && apiErr.Code == "context_length_exceeded"
    })
```

未提供 detector 时使用 `DefaultPromptTooLongDetector`，匹配 Anthropic / OpenAI / Azure / Google 的常见短语.

---

## 关键消息保留（Pinned Messages）

降级截断 / fallback truncate 在丢弃旧消息时会保留 pinned 消息：

```go
cfg := compact.DefaultCompactionConfig().
    WithPinnedMessageFilter(func(idx, total int, m compact.Message) bool {
        return m.Extra["category"] == "system_directive"
    })
```

默认规则：首条 user 消息 + 任何 `Extra["pinned"] == "true"` / `Extra["role"] == "system_prompt"` 的消息。

---

## 架构图

```
┌─────────────────────────────────────────────────────────────┐
│  CompactMiddleware (eino ADK BeforeModelRewriteState)       │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  DefaultCompactor.Compact()                           │  │
│  │  ┌─────────────┐                                      │  │
│  │  │ PreCompact  │ ← 用户钩子，可改写/拒绝              │  │
│  │  └──────┬──────┘                                      │  │
│  │         ▼                                             │  │
│  │  ┌─────────────┐                                      │  │
│  │  │   Layer 1   │ MicroCompact: 清理旧 tool_result     │  │
│  │  └──────┬──────┘ (<1ms, 不丢语义)                     │  │
│  │         ▼                                             │  │
│  │  ┌─────────────┐                                      │  │
│  │  │   Layer 2   │ AutoCompact: 阈值判断 + 断路器       │  │
│  │  └──────┬──────┘                                      │  │
│  │         │  超过阈值时:                                │  │
│  │         ├──→ Layer 4: SessionMemory (复用已有摘要)    │  │
│  │         └──→ Layer 3: FullCompact (LLM 生成摘要)      │  │
│  │              │                                        │  │
│  │              └─失败─→ fallbackTruncate (保留 pinned)  │  │
│  │         ▼                                             │  │
│  │  ┌─────────────┐                                      │  │
│  │  │ PostCompact │ ← 用户钩子，记录指标                 │  │
│  │  └─────────────┘                                      │  │
│  └───────────────────────────────────────────────────────┘  │
│                       ▼                                     │
│  ReactiveCompact: 压缩后仍超危险阈值时的紧急截断            │
└─────────────────────────────────────────────────────────────┘
```

---

## 术语表

| 术语 | 说明 |
|------|------|
| **MicroCompact** | Layer 1。规则清理旧工具结果，不调 LLM，<1ms |
| **AutoCompact** | Layer 2。token 超阈值时自动触发摘要 |
| **FullCompact** | Layer 3。让 LLM 生成结构化摘要（`<分析>` + `<摘要>` 9 章节） |
| **Session Memory** | Layer 4。复用后台累积的摘要，避免重新调用 LLM |
| **PTL (Prompt-Too-Long)** | LLM 返回的 "请求过长" 错误，需要进一步裁剪后重试 |
| **Fallback Truncate** | LLM 摘要彻底失败时的兜底——按比例保留最近消息 + pinned |
| **Reactive Compact** | API 调用返回 PTL 后的紧急截断，比 Fallback 更激进（保留 20%） |
| **Pinned Messages** | 在截断时必须保留的关键消息（首条 user / 显式打标） |
| **Circuit Breaker** | 连续 N 次压缩失败后停止尝试，避免抖动 |
| **CompactBoundary** | 压缩边界标记消息（system role），用于分组与去重 |

---

## 故障排查

**Q: 自动压缩没触发？**
- 检查 `modelMaxTokens` 是否过大（默认 256K，可能与实际窗口不符）
- 用 `compact.PresetForModel("your-model").AutoCompactThreshold()` 查看实际阈值
- 检查日志（`WithLogLevel(LogLevelDebug)`）：是否提示 "unknown model preset"

**Q: 压缩后 token 数变化不大？**
- 看 `result.Trigger`：若是 `micro_compact`，说明只清理了旧工具，未触发 LLM 摘要
- 调小 `WithAutoCompactBufferTokens` 让阈值更敏感
- 检查 `MicroCompactWhitelist` 是否过窄

**Q: LLM 摘要总是失败 / 降级？**
- 看日志中是否出现 "circuit breaker triggered"——表示连续 fallback
- 检查 PTL 错误是否被正确识别：试一下自定义 `WithPromptTooLongDetector`
- 跑 `examples/failure_scenarios` 复现并对比行为

**Q: 想用 slog / zap？**
- 实现 `compact.Logger` 接口（4 个方法），通过 `WithLogger(...)` 注入
- 不要用 `Silent` 字段（已废弃），用 `WithLogLevel(LogLevelSilent)` 或 `NewNopLogger()`

**Q: 重要的 system prompt 在降级截断时丢失？**
- 用 `Extra["pinned"] = "true"` 标记必须保留的消息
- 或自定义 `WithPinnedMessageFilter` 实现你自己的规则

**Q: 怎么验证压缩链路在生产环境工作正常？**
- 注册 `WithPostCompact` 上报指标到 Prometheus / 自家系统
- 监控 `result.Trigger` 分布：fallback_truncate 占比过高说明 Summarizer 不稳定

---

## License

[MIT](LICENSE)
