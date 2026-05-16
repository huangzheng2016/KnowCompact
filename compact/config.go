package compact

import (
	"context"
	"sync"
	"time"
)

// CompactionConfig 压缩全局配置.
type CompactionConfig struct {
	// 自动压缩阈值相关
	AutoCompactEnabled          bool `json:"auto_compact_enabled"`
	AutoCompactBufferTokens     int  `json:"auto_compact_buffer_tokens"`      // 默认 13K
	MaxOutputTokensForSummary   int  `json:"max_output_tokens_for_summary"`   // 默认 20K
	WarningThresholdBuffer      int  `json:"warning_threshold_buffer"`        // 默认 20K

	// 断路器
	MaxConsecutiveFailures int `json:"max_consecutive_failures"` // 默认 3

	// Full Compact 重试与降级
	FullCompactMaxRetries   int     `json:"full_compact_max_retries"`   // 默认 3
	FallbackTruncateRatio   float64 `json:"fallback_truncate_ratio"`    // 默认 0.3
	FallbackTruncateMinKeep int     `json:"fallback_truncate_min_keep"` // 默认 4

	// Session Memory 配置
	SMMinTokens            int `json:"sm_min_tokens"`          // 默认 10K
	SMMaxTokens            int `json:"sm_max_tokens"`          // 默认 40K
	SMMinTextBlockMessages int `json:"sm_min_text_block_msgs"` // 默认 5

	// 微压缩
	MicroCompactEnabled bool `json:"micro_compact_enabled"`
	RecentToolsKeep     int  `json:"recent_tools_keep"` // 保留最近 N 个工具结果

	// MicroCompactWhitelist 微压缩白名单。
	// nil 或空 map 表示所有工具结果均可压缩（默认行为）。
	// 传入非空 map 时，仅压缩白名单内的工具。
	MicroCompactWhitelist map[string]bool `json:"-"`

	// MicroCompactCaseInsensitive 白名单是否忽略大小写（默认 true）。
	// 设为 true 时，"Read" 可匹配 "read"/"READ"/"Read"。
	MicroCompactCaseInsensitive bool `json:"micro_compact_case_insensitive"`

	// 模型
	ModelName string `json:"model_name"` // 模型名（用于上下文窗口检测）

	// 文件重注入
	RecentFilesMax      int `json:"recent_files_max"`       // 默认 5
	RecentFileMaxTokens int `json:"recent_file_max_tokens"` // 默认 5000
	FileReinjectBudget  int `json:"file_reinject_budget"`   // 默认 50000

	// FileReadToolNames 视为"文件读取"的工具名列表。
	FileReadToolNames []string `json:"file_read_tool_names"`

	// 回调
	OnCompactionStart func(info CompactionInfo)     `json:"-"`
	OnCompactionEnd   func(result CompactionResult) `json:"-"`

	// PreCompact 在压缩开始前回调，允许用户改写消息列表。
	// 典型用途：敏感信息脱敏、特定消息打标、Pinned 标记的注入。
	// 返回的消息列表会替代原列表参与后续压缩。返回 error 时压缩中止。
	// nil 表示不处理。
	PreCompact func(ctx context.Context, messages []Message) ([]Message, error) `json:"-"`

	// PostCompact 在压缩完成后回调，允许用户改写压缩结果。
	// 典型用途：附加自定义元数据、二次过滤、上报。
	// 返回的结果会替代原结果。返回 error 时整次压缩视为失败。
	// nil 表示不处理。
	PostCompact func(ctx context.Context, result *CompactionResult) (*CompactionResult, error) `json:"-"`

	// PromptTooLongDetector 用户自定义的 PTL 错误判定函数。
	// nil 时使用 DefaultPromptTooLongDetector。
	// 详见 errors.go。
	PromptTooLongDetector PromptTooLongDetector `json:"-"`

	// Logger 用户传入的日志实现。
	// nil 时使用基于 stdlib log 的默认实现（NewStdLogger）。
	// 适配 slog/zap/logrus 时，实现 Logger 接口即可。
	Logger Logger `json:"-"`

	// LogLevel 日志级别。
	// 零值（LogLevelUnset）等同于 LogLevelInfo。
	// 仅在 Logger 为 nil 时生效（用户传入的 Logger 自己管理 level）。
	LogLevel LogLevel `json:"-"`

	// PinnedMessageFilter 判断一条消息是否"必须保留"。
	// 在降级截断（fallbackTruncate）等会丢弃消息的路径中，
	// 标记为 pinned 的消息会被强制保留在结果中。
	// 默认实现：保留首条 user 消息 + 任何 Extra["pinned"]=="true" 的消息。
	// nil 时使用 DefaultPinnedMessageFilter。
	PinnedMessageFilter PinnedMessageFilter `json:"-"`
}

// DefaultCompactionConfig 返回默认配置.
//
// 参数值来源于 Claude Code 源码逆向分析（见项目根目录 Claude_Code上下文压缩算法深度分析.md）。
// AutoCompact 触发阈值在运行时动态计算：
//   threshold = modelMaxTokens - MaxOutputTokensForSummary(20K) - AutoCompactBufferTokens(13K)
//
// 举例：256K 上下文 → 阈值 = 256K - 20K - 13K = 223K
//
// 如需覆盖默认参数，用 Builder 方法链式调整：
//   config := compact.DefaultCompactionConfig().
//       WithAutoCompactBufferTokens(10_000).
//       WithRecentToolsKeep(2)
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		AutoCompactEnabled:        true,
		AutoCompactBufferTokens:   13_000, // Claude Code 固定常量
		MaxOutputTokensForSummary: 20_000,
		WarningThresholdBuffer:    20_000, // 预警阈值
		MaxConsecutiveFailures:    3,
		SMMinTokens:               10_000, // Session Memory 最小触发 tokens
		SMMaxTokens:               40_000, // Session Memory 最大存储 tokens
		SMMinTextBlockMessages:    5,
		MicroCompactEnabled:           true,
		MicroCompactCaseInsensitive:   true,
		RecentToolsKeep:               3,
		FullCompactMaxRetries:         3,
		FallbackTruncateRatio:         0.3,
		FallbackTruncateMinKeep:       4,
		RecentFilesMax:            5,      // 最大跟踪文件数
		RecentFileMaxTokens:       5_000,  // 单个文件最大 tokens
		FileReinjectBudget:        50_000, // 附件重注入总预算 tokens
		FileReadToolNames:         append([]string(nil), DefaultFileReadToolNames...), // 深拷贝默认值
	}
}

// ============================================================
// CompactionConfig Builder 方法（值类型，链式调用）
// ============================================================

// WithFileReadToolNames 直接替换文件读取工具名列表.
func (c CompactionConfig) WithFileReadToolNames(names ...string) CompactionConfig {
	c.FileReadToolNames = names
	return c
}

// AppendFileReadToolNames 追加文件读取工具名.
func (c CompactionConfig) AppendFileReadToolNames(names ...string) CompactionConfig {
	c.FileReadToolNames = append(c.FileReadToolNames, names...)
	return c
}

// ResetFileReadToolNames 恢复为默认文件读取工具名列表.
func (c CompactionConfig) ResetFileReadToolNames() CompactionConfig {
	c.FileReadToolNames = append([]string(nil), DefaultFileReadToolNames...)
	return c
}

// WithRecentToolsKeep 设置保留最近工具结果的数量.
func (c CompactionConfig) WithRecentToolsKeep(n int) CompactionConfig {
	c.RecentToolsKeep = n
	return c
}

// WithAutoCompactBufferTokens 设置自动压缩缓冲 tokens.
func (c CompactionConfig) WithAutoCompactBufferTokens(n int) CompactionConfig {
	c.AutoCompactBufferTokens = n
	return c
}

// WithMicroCompactWhitelist 设置微压缩白名单.
// nil 或空 map 表示所有工具结果均可压缩。
func (c CompactionConfig) WithMicroCompactWhitelist(whitelist map[string]bool) CompactionConfig {
	c.MicroCompactWhitelist = whitelist
	return c
}

// WithMicroCompactCaseInsensitive 设置白名单是否忽略大小写（默认 true）。
func (c CompactionConfig) WithMicroCompactCaseInsensitive(v bool) CompactionConfig {
	c.MicroCompactCaseInsensitive = v
	return c
}

// WithMaxOutputTokensForSummary 设置摘要最大输出 tokens.
func (c CompactionConfig) WithMaxOutputTokensForSummary(n int) CompactionConfig {
	c.MaxOutputTokensForSummary = n
	return c
}

// WithFullCompactMaxRetries 设置 Full Compact 最大重试次数.
func (c CompactionConfig) WithFullCompactMaxRetries(n int) CompactionConfig {
	c.FullCompactMaxRetries = n
	return c
}

// WithFallbackTruncateRatio 设置降级截断保留比例（0~1）.
func (c CompactionConfig) WithFallbackTruncateRatio(ratio float64) CompactionConfig {
	c.FallbackTruncateRatio = ratio
	return c
}

// WithFallbackTruncateMinKeep 设置降级截断最少保留消息数.
func (c CompactionConfig) WithFallbackTruncateMinKeep(n int) CompactionConfig {
	c.FallbackTruncateMinKeep = n
	return c
}

// WithRecentFilesMax 设置最大跟踪文件数.
func (c CompactionConfig) WithRecentFilesMax(n int) CompactionConfig {
	c.RecentFilesMax = n
	return c
}

// WithRecentFileMaxTokens 设置单个文件最大 tokens.
func (c CompactionConfig) WithRecentFileMaxTokens(n int) CompactionConfig {
	c.RecentFileMaxTokens = n
	return c
}

// WithFileReinjectBudget 设置附件重注入总预算 tokens.
func (c CompactionConfig) WithFileReinjectBudget(n int) CompactionConfig {
	c.FileReinjectBudget = n
	return c
}

// WithSMMinTokens 设置 Session Memory 最小触发 tokens.
func (c CompactionConfig) WithSMMinTokens(n int) CompactionConfig {
	c.SMMinTokens = n
	return c
}

// WithSMMaxTokens 设置 Session Memory 最大存储 tokens.
func (c CompactionConfig) WithSMMaxTokens(n int) CompactionConfig {
	c.SMMaxTokens = n
	return c
}

// WithLogger 设置自定义 Logger。
//
// 用户可适配 slog/zap/logrus —— 实现 Logger 接口即可。
// nil 时回退到内置默认 logger（受 LogLevel 控制）。
func (c CompactionConfig) WithLogger(logger Logger) CompactionConfig {
	c.Logger = logger
	return c
}

// WithLogLevel 设置默认 logger 的日志级别（仅当未提供自定义 Logger 时生效）.
func (c CompactionConfig) WithLogLevel(level LogLevel) CompactionConfig {
	c.LogLevel = level
	return c
}

// WithPromptTooLongDetector 设置用户自定义的 PTL 错误判定函数.
func (c CompactionConfig) WithPromptTooLongDetector(d PromptTooLongDetector) CompactionConfig {
	c.PromptTooLongDetector = d
	return c
}

// WithPreCompact 注册压缩前回调。
//
// 钩子返回的消息列表会替代原列表参与后续压缩。
// 返回 error 时整次压缩中止（不会触发 PostCompact）。
func (c CompactionConfig) WithPreCompact(
	fn func(ctx context.Context, messages []Message) ([]Message, error),
) CompactionConfig {
	c.PreCompact = fn
	return c
}

// WithPostCompact 注册压缩后回调。
//
// 钩子返回的结果会替代原结果。
// 返回 error 时本次压缩视为失败（会影响断路器计数）。
func (c CompactionConfig) WithPostCompact(
	fn func(ctx context.Context, result *CompactionResult) (*CompactionResult, error),
) CompactionConfig {
	c.PostCompact = fn
	return c
}

// WithPinnedMessageFilter 设置 pinned 消息判定函数（用于降级截断保留关键消息）.
func (c CompactionConfig) WithPinnedMessageFilter(f PinnedMessageFilter) CompactionConfig {
	c.PinnedMessageFilter = f
	return c
}

// Compactor 上下文压缩器接口.
type Compactor interface {
	Compact(ctx context.Context, messages []Message, modelMaxTokens int) (*CompactionResult, error)
	ShouldCompact(messages []Message, modelMaxTokens int) bool
	Reset()
}

// Summarizer LLM 摘要生成接口，用于 FullCompact.
type Summarizer interface {
	GenerateSummary(ctx context.Context, messages []Message) (string, error)
}

// SessionMemoryStore Session Memory 存储接口.
type SessionMemoryStore interface {
	GetMemory(ctx context.Context) (string, error)
	GetLastSummarizedMessageID(ctx context.Context) string
	IsEmpty(ctx context.Context) bool
}

// compactCircuitBreaker 压缩断路器.
type compactCircuitBreaker struct {
	mu                  sync.Mutex
	consecutiveFailures int
	maxFailures         int
	disabled            bool
	lastFailureTime     time.Time
}

func newCircuitBreaker(maxFailures int) *compactCircuitBreaker {
	return &compactCircuitBreaker{maxFailures: maxFailures}
}

func (b *compactCircuitBreaker) isOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disabled {
		return true
	}
	if b.consecutiveFailures >= b.maxFailures {
		return true
	}
	return false
}

func (b *compactCircuitBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	b.disabled = false
}

// recordFailure 记录一次失败，返回累计连续失败次数以及是否刚好触发断路器。
//
// 返回值用于外部决策（如日志输出），避免调用方再维护重复的失败计数。
func (b *compactCircuitBreaker) recordFailure() (failures int, justTripped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	prev := b.consecutiveFailures
	b.consecutiveFailures++
	b.lastFailureTime = time.Now()
	return b.consecutiveFailures, prev < b.maxFailures && b.consecutiveFailures >= b.maxFailures
}

// failureCount 返回当前连续失败次数（仅用于测试与可观测性）.
func (b *compactCircuitBreaker) failureCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consecutiveFailures
}

func (b *compactCircuitBreaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	b.disabled = false
}
