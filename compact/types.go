// Package compact 提供 eino 框架通用的上下文压缩插件.
// 实现基于 Claude Code 的 4 层递进压缩体系:
//   Layer 1: MicroCompact  - 规则清理旧工具输出 (<1ms)
//   Layer 2: AutoCompact   - 阈值触发 + 断路器
//   Layer 3: FullCompact   - LLM 摘要生成 (5-30s)
//   Layer 4: SessionMemory - 复用已有摘要 (<10ms)
package compact

// ============================================================
// 默认值常量
// ============================================================

// DefaultFileReadToolNames 是默认的"文件读取"工具名列表。
// 附件重注入器会识别这些工具的 tool_result，将其内容在压缩后重新注入上下文。
var DefaultFileReadToolNames = []string{
	"Read",      // Claude Code 风格
	"read_file", // eino Filesystem 中间件
	"ReadFile",
	"file_read",
	"cat",       // Unix 风格
	"view",      // 其他变体
	"show",      // 展示文件内容
}

// Role 消息角色.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// ContentType 内容块类型.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeImage      ContentType = "image"
	ContentTypeThinking   ContentType = "thinking"
)

// ContentBlock 消息内容块.
type ContentBlock struct {
	Type       ContentType `json:"type"`
	Text       string      `json:"text,omitempty"`
	ToolName   string      `json:"tool_name,omitempty"`
	ToolInput  string      `json:"tool_input,omitempty"`
	ToolUseID  string      `json:"tool_use_id,omitempty"`
	ToolOutput string      `json:"tool_output,omitempty"`
	Thinking   string      `json:"thinking,omitempty"`
}

// Message 通用消息类型，兼容 eino schema.Message.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
	// Meta 元数据字段
	MessageID string            `json:"message_id,omitempty"`
	Usage     *TokenUsage       `json:"usage,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// TokenUsage API 返回的精确 token 统计.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// CompactableTools 保留向后兼容的可被微压缩工具集合.
// 若需要自定义白名单，请使用 CompactionConfig.MicroCompactWhitelist.
var CompactableTools = map[string]bool{
	"Read":      true,
	"Bash":      true,
	"Grep":      true,
	"Glob":      true,
	"WebSearch": true,
	"WebFetch":  true,
	"Edit":      true,
	"Write":     true,
}
