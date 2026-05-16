package main

import (
	"github.com/huangzheng2016/KnowCompact/compact"
	"context"
	"fmt"
	"strings"
)

// ============================================================
// 演示: 在 eino agent 中集成 Compact 上下文压缩插件
// ============================================================
//
// 集成方式：
//   - 方式 1: Lambda 节点（推荐）— 作为图管线中的预处理节点
//   - 方式 2: 回调处理器 — 通过 callbacks.Handler 自动触发
//   - 方式 3: 包装 ChatModel — 在 Generate 中拦截
//
// 以下示例演示完整的 agent 构建和使用流程.

func main() {
	fmt.Println("=== Eino Agent + Compact 上下文压缩插件 演示 ===")
fmt.Println()

	demonstrateWindowSize()
	fmt.Println()

	// Step 1: 创建压缩配置
	config := compact.DefaultCompactionConfig()
	fmt.Printf("[配置] 自动压缩阈值缓冲: %d tokens\n", config.AutoCompactBufferTokens)
	fmt.Printf("[配置] 断路器最大失败次数: %d\n", config.MaxConsecutiveFailures)
	fmt.Printf("[配置] 微压缩保留最近工具结果数: %d\n\n", config.RecentToolsKeep)

	// Step 2: 创建 LLM 摘要器
	summarizer := &demoSummarizer{}

	// Step 3: 创建 Session Memory 存储
	memStore := newDemoMemoryStore()

	// Step 4: 创建压缩器
	compactor := compact.NewDefaultCompactor(config, summarizer, memStore)

	// Step 5: 模拟 agent 对话
	demonstrateAgentLoop(compactor)

	// Step 5.1: 演示 Hook 集成
	demonstrateHookIntegration(compactor)

	// Step 6: 演示转换
	demonstrateSchemaConversion()
}

// demonstrateAgentLoop 模拟 agent ReAct 循环中的压缩触发.
func demonstrateAgentLoop(compactor *compact.DefaultCompactor) {
	fmt.Println("--- Agent ReAct 循环（模拟）---")

	// 构造一个经过多轮工具调用的长对话
	conversation := buildRealisticConversation()
	fmt.Printf("初始消息数: %d, tokens: %d\n",
		len(conversation), compact.EstimateMessageTokens(conversation))

	// Round 1: 微压缩——清理旧工具输出
	r1 := compact.MicroCompact(conversation, 3, true)
	conversation = r1.Messages
	fmt.Printf("MicroCompact: 清理 %d 个旧工具结果, 释放 ~%d tokens, 剩余 %d tokens\n",
		r1.ToolsCleared, r1.TokensFreed, compact.EstimateMessageTokens(conversation))

	// 验证：最旧的工具结果被清除
	for i, msg := range conversation {
		for _, b := range msg.Content {
			if b.ToolOutput == "[Old tool result content cleared]" {
				fmt.Printf("  [已清除] msg[%d] %s/%s\n", i, b.ToolName, b.ToolUseID)
			}
		}
	}

	// Round 2: 再追加一轮工具调用
	conversation = append(conversation, compact.Message{
		Role: compact.RoleAssistant,
		Content: []compact.ContentBlock{
			{Type: compact.ContentTypeToolUse, ToolName: "Read", ToolUseID: "t-new",
				ToolInput: `{"file_path":"result.json"}`},
		},
		MessageID: "msg-020",
	})
	conversation = append(conversation, compact.Message{
		Role: compact.RoleTool,
		Content: []compact.ContentBlock{
			{Type: compact.ContentTypeToolResult, ToolName: "Read", ToolUseID: "t-new",
				ToolOutput: strings.Repeat("json result data\n", 500)},
		},
	})
	r2 := compact.MicroCompact(conversation, 3, true)
	conversation = r2.Messages
	fmt.Printf("\n第2轮微压缩: 再清理 %d 个, 当前 %d tokens\n",
		r2.ToolsCleared, compact.EstimateMessageTokens(conversation))

	// 演示 Full Compact：手动触发（绕过阈值），生成结构化摘要
	fmt.Println()
	result, err := compactor.ManualCompact(context.Background(), conversation, 200_000)
	if err != nil {
		fmt.Printf("Full Compact 失败: %v\n", err)
	} else {
		fmt.Printf("Full Compact: %d → %d tokens, trigger=%s, 消息数 %d → %d\n",
			result.TokensBefore, result.TokensAfter, result.Trigger,
			len(conversation), len(result.Messages))
	}

	// 重置
	compactor.Reset()
}

// buildRealisticConversation 构造一个接近真实的 agent 多轮对话.
// 包含 6 次工具调用，模拟典型的数据分析场景.
func buildRealisticConversation() []compact.Message {
	var msgs []compact.Message

	// 用户初始请求
	msgs = append(msgs, compact.Message{
		Role: compact.RoleUser,
		Content: []compact.ContentBlock{
			{Type: compact.ContentTypeText, Text: "帮我分析项目里所有 Go 文件的依赖关系"},
		},
		MessageID: "msg-001",
	})

	// 工具调用辅助函数
	makeToolRound := func(
		assistantMsgID string,
		thinking string,
		toolName, toolID, toolInput string,
		toolOutput string,
		assistantText string,
		textMsgID string,
		usage *compact.TokenUsage,
	) []compact.Message {
		return []compact.Message{
			{
				Role: compact.RoleAssistant,
				Content: []compact.ContentBlock{
					{Type: compact.ContentTypeThinking, Thinking: thinking},
					{Type: compact.ContentTypeToolUse, ToolName: toolName, ToolUseID: toolID, ToolInput: toolInput},
				},
				MessageID: assistantMsgID,
			},
			{
				Role: compact.RoleTool,
				Content: []compact.ContentBlock{
					{Type: compact.ContentTypeToolResult, ToolName: toolName, ToolUseID: toolID, ToolOutput: toolOutput},
				},
			},
			{
				Role:    compact.RoleAssistant,
				Content: []compact.ContentBlock{{Type: compact.ContentTypeText, Text: assistantText}},
				MessageID: textMsgID,
				Usage:     usage,
			},
		}
	}

	// Round 1: Glob 找 Go 文件
	msgs = append(msgs, makeToolRound(
		"msg-002", "先列出项目里的 Go 文件",
		"Glob", "t-glob-001", `{"pattern":"**/*.go"}`,
		strings.Repeat("file: pkg/handler/user.go\nfile: pkg/handler/order.go\nfile: pkg/service/auth.go\nfile: pkg/db/mysql.go\nfile: pkg/db/redis.go\nfile: pkg/util/string.go\nfile: main.go\n", 400),
		"找到 7 个 Go 文件，开始逐个读取分析。",
		"msg-003",
		&compact.TokenUsage{InputTokens: 12000, OutputTokens: 25},
	)...)

	// Round 2: Read main.go
	msgs = append(msgs, makeToolRound(
		"msg-004", "读取主入口文件",
		"Read", "t-read-001", `{"file_path":"main.go"}`,
		strings.Repeat("package main\nimport (...)\nfunc main() { ... }\n", 300),
		"main.go 导入了 handler、service、db 三个内部包。",
		"msg-005",
		&compact.TokenUsage{InputTokens: 16000, OutputTokens: 30},
	)...)

	// Round 3: Read handler/user.go
	msgs = append(msgs, makeToolRound(
		"msg-006", "读取用户 handler",
		"Read", "t-read-002", `{"file_path":"pkg/handler/user.go"}`,
		strings.Repeat("func GetUser(w http.ResponseWriter, r *http.Request) { ... }\nfunc CreateUser(w http.ResponseWriter, r *http.Request) { ... }\n", 350),
		"user.go 调用了 service.Auth、db.MySQL。",
		"msg-007",
		&compact.TokenUsage{InputTokens: 21000, OutputTokens: 28},
	)...)

	// Round 4: Read service/auth.go
	msgs = append(msgs, makeToolRound(
		"msg-008", "读取认证服务",
		"Read", "t-read-003", `{"file_path":"pkg/service/auth.go"}`,
		strings.Repeat("func Authenticate(token string) (*User, error) { ... }\nfunc ValidateSession(sid string) bool { ... }\n", 400),
		"auth.go 依赖 db.MySQL 和 db.Redis。",
		"msg-009",
		&compact.TokenUsage{InputTokens: 27000, OutputTokens: 25},
	)...)

	// Round 5: Bash 跑依赖分析
	msgs = append(msgs, makeToolRound(
		"msg-010", "用 go list 分析依赖",
		"Bash", "t-bash-001", `{"cmd":"go list -json ./..."}`,
		strings.Repeat(`{"ImportPath":"github.com/gin-gonic/gin","Imports":["fmt","net/http","sync","time",...]}`, 500)+"\n",
		"依赖分析完成，主要外部依赖: gin, gorm, redis client。",
		"msg-011",
		&compact.TokenUsage{InputTokens: 33000, OutputTokens: 40},
	)...)

	// Round 6: Grep 查特定的 import
	msgs = append(msgs, makeToolRound(
		"msg-012", "搜索数据库相关引用",
		"Grep", "t-grep-001", `{"pattern":"gorm|mysql|redis","path":"."}`,
		strings.Repeat("pkg/db/mysql.go:5: import \"gorm.io/gorm\"\npkg/db/redis.go:3: import \"github.com/go-redis/redis\"\npkg/service/auth.go:7: import \"github.com/huangzheng2016/KnowCompact/pkg/db\"\n", 300),
		"数据库引用集中在 pkg/db 和 pkg/service 层。",
		"msg-013",
		&compact.TokenUsage{InputTokens: 38000, OutputTokens: 35},
	)...)

	return msgs
}

// demonstrateSchemaConversion 演示 compact 消息结构.
func demonstrateSchemaConversion() {
	fmt.Println("\n--- 消息格式演示 ---")

	messages := []compact.Message{
		{
			Role: compact.RoleUser,
			Content: []compact.ContentBlock{
				{Type: compact.ContentTypeText, Text: "What is the meaning of life?"},
			},
			MessageID: "msg-001",
		},
		{
			Role: compact.RoleAssistant,
			Content: []compact.ContentBlock{
				{Type: compact.ContentTypeThinking, Thinking: "This is a philosophical question..."},
				{Type: compact.ContentTypeToolUse, ToolName: "WebSearch", ToolUseID: "search-001",
					ToolInput: `{"query":"meaning of life"}`},
			},
			MessageID: "msg-002",
			Usage:     &compact.TokenUsage{InputTokens: 100, OutputTokens: 50},
		},
		{
			Role: compact.RoleTool,
			Content: []compact.ContentBlock{
				{Type: compact.ContentTypeToolResult, ToolName: "WebSearch", ToolUseID: "search-001",
					ToolOutput: "42 is the answer to life, the universe, and everything."},
			},
		},
	}

	fmt.Printf("消息数: %d\n", len(messages))
	for _, m := range messages {
		textCount, toolCount := 0, 0
		for _, b := range m.Content {
			switch b.Type {
			case compact.ContentTypeText:
				textCount++
			case compact.ContentTypeToolUse, compact.ContentTypeToolResult:
				toolCount++
			}
		}
		fmt.Printf("  Role=%s textBlocks=%d toolBlocks=%d\n", m.Role, textCount, toolCount)
	}
}

// demonstrateHookIntegration 演示使用 Hook 方式透明集成压缩.
func demonstrateHookIntegration(compactor *compact.DefaultCompactor) {
	fmt.Println("\n--- Hook 方式集成演示 ---")

	// 方式 A: 微压缩专用 Hook（最轻量，纯规则，无 LLM 调用）
	microHook := compact.NewMicroCompactHook(compactor).WithSilent(true)
	batch := buildRealisticConversation()
	fmt.Printf("Hook 预处理前: %d 条消息, %d tokens\n",
		len(batch), compact.EstimateMessageTokens(batch))
	result, _ := microHook.PreProcess(context.Background(), batch, nil)
	fmt.Printf("微压缩 Hook 后: %d 条消息, %d tokens\n",
		len(result), compact.EstimateMessageTokens(result))

	// 方式 B: 带压缩回调的 Hook
	hook := compact.NewCompactionHook(compactor, 200_000).
		WithCallback(func(r *compact.CompactionResult) {
			fmt.Printf("  [回调] 压缩触发=%s, %d→%d tokens\n",
				r.Trigger, r.TokensBefore, r.TokensAfter)
		}).
		WithSilent(true)

	batch = buildRealisticConversation()
	_, _ = hook.PreProcess(context.Background(), batch, nil)
	fmt.Println("Hook 处理完成（未达阈值仅微压缩）")

	// 方式 C: 集成到 eino graph（伪代码，实际需 import eino）
	fmt.Println("\n=== 实际 eino 集成代码示例 ===")
	fmt.Print(`
// 1. 创建 Hook
hook := compact.NewCompactionHook(compactor, 200_000)

// 2. 注入到 ChatModel 节点
graph.AddChatModelNode("chat_model", chatModel,
    hook.AsNodeOption(),   // ← 一行注入
)

// 3. 或者注册全局回调
handler := hook.AsCallbackHandler()
callbacks.AppendGlobalHandlers(handler)

// 4. 正常使用 agent
runnable.Invoke(ctx, messages)
// ↑ 每轮 LLM 调用前自动执行压缩，无需其他代码
`)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}


// demonstrateWindowSize 演示窗口大小配置的几种方式.
func demonstrateWindowSize() {
	fmt.Println("--- 上下文窗口大小配置 ---")

	// 方式 1: 按模型名自动匹配（推荐）
	preset := compact.PresetForModel("claude-sonnet-4-6")
	fmt.Printf("  Sonnet   : 窗口=%dK  有效窗口=%dK  压缩阈值=%dK\n",
		preset.ContextWindow/1000, preset.EffectiveWindow()/1000,
		preset.AutoCompactThreshold()/1000)

	preset = compact.PresetForModel("gpt-4o")
	fmt.Printf("  GPT-4o   : 窗口=%dK  有效窗口=%dK  压缩阈值=%dK\n",
		preset.ContextWindow/1000, preset.EffectiveWindow()/1000,
		preset.AutoCompactThreshold()/1000)

	preset = compact.PresetForModel("deepseek-v3")
	fmt.Printf("  DeepSeek : 窗口=%dK  有效窗口=%dK  压缩阈值=%dK\n",
		preset.ContextWindow/1000, preset.EffectiveWindow()/1000,
		preset.AutoCompactThreshold()/1000)

	// 方式 2: 直接指定窗口大小
	custom := compact.CustomPreset(100_000, 8_000).WithBuffer(5_000)
	fmt.Printf("  自定义   : 窗口=%dK  有效窗口=%dK  压缩阈值=%dK\n",
		custom.ContextWindow/1000, custom.EffectiveWindow()/1000,
		custom.AutoCompactThreshold()/1000)

	// 方式 3: 在 Hook 中设置
	fmt.Print("  Hook 中  : NewCompactionHook(compactor, 200_000)\n" +
		"              ↑ ModelMaxTokens 直接指定窗口大小\n\n")
}

// demoSummarizer 演示用 LLM 摘要生成器.
type demoSummarizer struct{}

func (d *demoSummarizer) GenerateSummary(ctx context.Context, messages []compact.Message) (string, error) {
	// 实际集成时，这里调用真实的 LLM API
	return `<analysis>
用户请求分析 data.csv 文件并统计列分布。agent 先后使用了 Read 和 Bash 工具。
</analysis>
<summary>
1. Primary Request and Intent:
   分析 data.csv 文件，统计每列数据分布

2. Key Technical Concepts:
   - CSV 数据分析
   - awk 命令行统计

3. Files and Code Sections:
   - data.csv: 包含 name/age/city 三列，约 1003 行数据

4. Errors and fixes:
   - 无

5. Problem Solving:
   通过 Read 读取文件内容，使用 Bash + awk 统计年龄分布

6. All user messages:
   - 请帮我分析这个数据文件 data.csv
   - 统计每列的数据分布

7. Pending Tasks:
   - 无

8. Current Work:
   完成年龄分布统计，数据包含年龄范围 25-35

9. Optional Next Step:
   可进一步分析城市分布
</summary>`, nil
}

// demoMemoryStore 演示用 Session Memory 存储.
type demoMemoryStore struct {
	memory              string
	lastSummarizedMsgID string
}

func newDemoMemoryStore() *demoMemoryStore {
	return &demoMemoryStore{
		memory: `## Session Memory

### User Intent
分析 data.csv 文件

### Key Decisions
- 使用 Read 工具读取 CSV 文件
- 使用 Bash + awk 进行列分布统计

### Current State
已完成文件读取，正在统计年龄分布`,
		lastSummarizedMsgID: "msg-003",
	}
}

func (s *demoMemoryStore) GetMemory(ctx context.Context) (string, error) {
	return s.memory, nil
}

func (s *demoMemoryStore) GetLastSummarizedMessageID(ctx context.Context) string {
	return s.lastSummarizedMsgID
}

func (s *demoMemoryStore) IsEmpty(ctx context.Context) bool {
	return s.memory == ""
}
