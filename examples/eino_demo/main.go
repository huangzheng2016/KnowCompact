package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	fspkg "github.com/cloudwego/eino/adk/filesystem"
	fsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"

	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/huangzheng2016/KnowCompact/compact"
	einocompact "github.com/huangzheng2016/KnowCompact/eino"
)

// ============================================================
// KnowCompact + eino v0.8 ADK Middleware 完整演示
// ============================================================
//
// 本 demo 展示了如何在 eino ChatModelAgent 中集成：
//   1. KnowCompact 上下文压缩中间件
//   2. eino v0.8 内置 ADK 中间件（默认全部启用）
//   3. 中文语言设置
//
// 包含的 ADK 中间件（按执行顺序）：
//   - PatchToolCalls:  修补悬空的 ToolCall

//   - Filesystem:      文件系统工具（ls/read/write/edit/glob/grep）
//   - Skill:           Skill.md 技能加载
//   - PlanTask:        任务管理（TaskCreate/TaskGet/TaskUpdate/TaskList）
//   - ToolReduction:   工具结果截断与清理
//   - KnowCompact:     上下文压缩（Micro→Auto→SessionMemory→Full）
//
// 运行:
//   cd examples/eino_demo
//   go mod tidy
//   export OPENAI_API_KEY="your-api-key"
//   export OPENAI_BASE_URL="https://api.openai.com/v1"  # 可选，默认即 OpenAI 官方端点
//   go run .

const (
	modelName = "kimi-k2.6"
)

func getBaseURL() string {
	if url := os.Getenv("OPENAI_BASE_URL"); url != "" {
		return url
	}
	return "https://api.openai.com/v1"
}

func main() {
	ctx := context.Background()

	// ========== 1. 设置全局中文语言 ==========
	if err := adk.SetLanguage(adk.LanguageChinese); err != nil {
		fmt.Printf("设置语言失败: %v\n", err)
	}
	fmt.Println("=== KnowCompact + eino v0.8 ADK Middleware 完整演示 ===")
	fmt.Printf("语言: 中文 | 模型: %s\n\n", modelName)

	// ========== 2. 初始化共享 Backend ==========
	fsBackend := fspkg.NewInMemoryBackend()

	// ========== 3. 创建所有 ADK 中间件 ==========
	handlers := createMiddlewares(ctx, fsBackend)

	// ========== 4. 阶段 1: Mock 模型验证 ==========
	fmt.Println("--- 阶段 1: Mock 模型验证压缩中间件 ---")
	testWithMockModel(ctx, handlers)

	fmt.Println()
	fmt.Println()

	// ========== 5. 阶段 2: 真实 API 测试 ==========
	fmt.Println("--- 阶段 2: 真实 API 测试 ---")
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("警告：未设置 OPENAI_API_KEY，跳过真实 API 测试")
		fmt.Println("   export OPENAI_API_KEY='your-api-key'")
		fmt.Println("   export OPENAI_BASE_URL='https://api.openai.com/v1'  # 可选")
		return
	}
	baseURL := getBaseURL()
	testWithRealAPI(ctx, handlers, apiKey, baseURL)
}

// createMiddlewares 创建完整的 ADK 中间件链.
func createMiddlewares(ctx context.Context, fsBackend fspkg.Backend) []adk.ChatModelAgentMiddleware {
	var handlers []adk.ChatModelAgentMiddleware

	// 1. PatchToolCalls — 修补悬空 ToolCall（无需配置）
	if mw, err := patchtoolcalls.New(ctx, nil); err == nil {
		handlers = append(handlers, mw)
		fmt.Println("  [中间件] PatchToolCalls 已启用")
	}

	// 2. Filesystem — 文件系统工具
	if mw, err := fsmw.New(ctx, &fsmw.MiddlewareConfig{
		Backend: fsBackend,
	}); err == nil {
		handlers = append(handlers, mw)
		fmt.Println("  [中间件] Filesystem 已启用")
	}

	// 3. Skill — Skill.md 技能加载
	if skillBackend, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
		Backend: fsBackend,
	}); err == nil {
		if mw, err := skill.NewMiddleware(ctx, &skill.Config{
			Backend: skillBackend,
		}); err == nil {
			handlers = append(handlers, mw)
			fmt.Println("  [中间件] Skill 已启用")
		}
	}

	// 4. PlanTask — 任务管理
	ptBackend := &plantaskBackendWrapper{InMemoryBackend: fsBackend.(*fspkg.InMemoryBackend)}
	if mw, err := plantask.New(ctx, &plantask.Config{
		Backend: ptBackend,
		BaseDir: "/tmp/tasks",
	}); err == nil {
		handlers = append(handlers, mw)
		fmt.Println("  [中间件] PlanTask 已启用")
	}

	// 5. ToolReduction — 工具结果截断与清理
	if mw, err := reduction.New(ctx, &reduction.Config{
		Backend:         fsBackend,
		SkipTruncation:  false,
		SkipClear:       false,
		TokenCounter:    tokenCounter,
		MaxTokensForClear: 100_000,
	}); err == nil {
		handlers = append(handlers, mw)
		fmt.Println("  [中间件] ToolReduction 已启用")
	}

	// 6. KnowCompact — 上下文压缩（放在最后，确保在模型调用前执行）
	config := compact.DefaultCompactionConfig()
	config.MicroCompactEnabled = true
	config.RecentToolsKeep = 2
	config.AutoCompactEnabled = true
	config.AutoCompactBufferTokens = 5_000
	compactor := compact.NewDefaultCompactor(config, nil, nil)
	compactMW := einocompact.NewCompactMiddleware(compactor, 0) // 0 = 使用默认值 256K
	handlers = append(handlers, compactMW)
	fmt.Println("  [中间件] KnowCompact 已启用")

	return handlers
}

// tokenCounter 为 ToolReduction 提供 token 计数.
func tokenCounter(ctx context.Context, msgs []adk.Message, tools []*schema.ToolInfo) (int64, error) {
	var total int
	for _, m := range msgs {
		if m != nil && m.Content != "" {
			total += len(m.Content) / 4
		}
	}
	return int64(total), nil
}

// plantaskBackendWrapper 包装 filesystem.InMemoryBackend，添加 Delete 方法.
type plantaskBackendWrapper struct {
	*fspkg.InMemoryBackend
}

func (b *plantaskBackendWrapper) Delete(ctx context.Context, req *plantask.DeleteRequest) error {
	// 演示用：plantask 的 Delete 不做实际删除
	return nil
}

// ============================================================
// Mock 模型测试
// ============================================================

func testWithMockModel(ctx context.Context, handlers []adk.ChatModelAgentMiddleware) {
	chatModel := newMockModel("mock")

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "compact-test-agent",
		Description: "测试 KnowCompact 上下文压缩",
		Model:       chatModel,
		Instruction: "你是一个有帮助的助手。请简洁回答问题。",
		Handlers:    handlers,
	})
	if err != nil {
		fmt.Printf("创建 Agent 失败: %v\n", err)
		return
	}

	fmt.Println("\n[测试 1] 短对话（预期: 不触发压缩）")
	runAgent(ctx, agent, []*schema.Message{
		{Role: schema.User, Content: "你好，请介绍一下 Go 语言"},
	})

	fmt.Println("\n[测试 2] 长对话（预期: 触发 MicroCompact，清理旧工具结果）")
	longMsgs := buildLongConversation()
	fmt.Printf("  构造长对话: %d 条消息, 估算 tokens: %d\n",
		len(longMsgs), compact.EstimateMessageTokens(einocompact.FromSchemaMessages(longMsgs)))
	runAgent(ctx, agent, longMsgs)
}

// ============================================================
// 真实 API 测试
// ============================================================

func testWithRealAPI(ctx context.Context, handlers []adk.ChatModelAgentMiddleware, apiKey, baseURL string) {
	chatModel := newOpenAIClient(apiKey, baseURL, modelName)

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "compact-test-agent",
		Description: "测试 KnowCompact 上下文压缩",
		Model:       chatModel,
		Instruction: "你是一个有帮助的助手。请简洁回答问题。",
		Handlers:    handlers,
	})
	if err != nil {
		fmt.Printf("创建 Agent 失败: %v\n", err)
		return
	}

	fmt.Println("[测试 3] 短对话（真实 API）")
	runAgent(ctx, agent, []*schema.Message{
		{Role: schema.User, Content: "你好，请介绍一下 Go 语言"},
	})

	fmt.Println("\n[测试 4] 长对话（真实 API，预期触发 MicroCompact）")
	longMsgs := buildLongConversation()
	fmt.Printf("  构造长对话: %d 条消息, 估算 tokens: %d\n",
		len(longMsgs), compact.EstimateMessageTokens(einocompact.FromSchemaMessages(longMsgs)))
	runAgent(ctx, agent, longMsgs)
}

// runAgent 运行 Agent 并打印输出.
func runAgent(ctx context.Context, agent *adk.ChatModelAgent, msgs []*schema.Message) {
	input := &adk.AgentInput{
		Messages:        msgs,
		EnableStreaming: false,
	}

	events := agent.Run(ctx, input)
	foundOutput := false

	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			fmt.Printf("  [Event] Error: %v\n", event.Err)
			continue
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			if msg := event.Output.MessageOutput.Message; msg != nil {
				foundOutput = true
				fmt.Printf("  Agent 回答: %s\n", truncate(msg.Content, 200))
			}
		}
		if event.Action != nil {
			if event.Action.Exit {
				fmt.Printf("  [Event] Action: Exit\n")
			}
			if event.Action.BreakLoop != nil {
				fmt.Printf("  [Event] Action: BreakLoop\n")
			}
		}
	}

	if !foundOutput {
		fmt.Println("  [Warning] 未从事件流中获取到模型输出")
	}
}

// buildLongConversation 构造一个模拟的长对话（多轮工具调用后的上下文膨胀）.
func buildLongConversation() []*schema.Message {
	var msgs []*schema.Message

	msgs = append(msgs, &schema.Message{
		Role:    schema.User,
		Content: "帮我分析项目里所有 Go 文件的依赖关系",
	})

	// Round 1: Glob 找 Go 文件
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "先列出项目里的 Go 文件",
		ToolCalls: []schema.ToolCall{
			{ID: "t-glob-001", Type: "function", Function: schema.FunctionCall{
				Name: "Glob", Arguments: `{"pattern":"**/*.go"}`,
			}},
		},
	})
	msgs = append(msgs, &schema.Message{
		Role:       schema.Tool,
		ToolCallID: "t-glob-001",
		ToolName:   "Glob",
		Content:    strings.Repeat("file: pkg/handler/user.go\nfile: pkg/handler/order.go\nfile: pkg/service/auth.go\nfile: pkg/db/mysql.go\nfile: pkg/db/redis.go\nfile: pkg/util/string.go\nfile: main.go\n", 200),
	})
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "找到 7 个 Go 文件，开始逐个读取分析。",
	})

	// Round 2: Read main.go
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "读取主入口文件",
		ToolCalls: []schema.ToolCall{
			{ID: "t-read-001", Type: "function", Function: schema.FunctionCall{
				Name: "Read", Arguments: `{"file_path":"main.go"}`,
			}},
		},
	})
	msgs = append(msgs, &schema.Message{
		Role:       schema.Tool,
		ToolCallID: "t-read-001",
		ToolName:   "Read",
		Content:    strings.Repeat("package main\nimport (...)\nfunc main() { ... }\n", 300),
	})
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "main.go 导入了 handler、service、db 三个内部包。",
	})

	// Round 3: Read handler/user.go
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "读取用户 handler",
		ToolCalls: []schema.ToolCall{
			{ID: "t-read-002", Type: "function", Function: schema.FunctionCall{
				Name: "Read", Arguments: `{"file_path":"pkg/handler/user.go"}`,
			}},
		},
	})
	msgs = append(msgs, &schema.Message{
		Role:       schema.Tool,
		ToolCallID: "t-read-002",
		ToolName:   "Read",
		Content:    strings.Repeat("func GetUser(w http.ResponseWriter, r *http.Request) { ... }\nfunc CreateUser(w http.ResponseWriter, r *http.Request) { ... }\n", 350),
	})
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "user.go 调用了 service.Auth、db.MySQL。",
	})

	// Round 4: Bash 跑依赖分析
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "用 go list 分析依赖",
		ToolCalls: []schema.ToolCall{
			{ID: "t-bash-001", Type: "function", Function: schema.FunctionCall{
				Name: "Bash", Arguments: `{"cmd":"go list -json ./..."}`,
			}},
		},
	})
	msgs = append(msgs, &schema.Message{
		Role:       schema.Tool,
		ToolCallID: "t-bash-001",
		ToolName:   "Bash",
		Content:    strings.Repeat(`{"ImportPath":"github.com/gin-gonic/gin","Imports":["fmt","net/http","sync","time"]}`, 400) + "\n",
	})
	msgs = append(msgs, &schema.Message{
		Role:    schema.Assistant,
		Content: "依赖分析完成，主要外部依赖: gin, gorm, redis client。",
	})

	return msgs
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
