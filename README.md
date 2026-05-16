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

## 完整示例

见 [`examples/eino_demo/`](examples/eino_demo/)。运行方式：

```bash
cd examples/eino_demo
go mod tidy
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # OpenAI 官方端点
go run .
```

Mock 测试不需要 API Key，自动验证压缩逻辑。

---

## License

[MIT](LICENSE)
