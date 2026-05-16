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

func (b *compactCircuitBreaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures++
	b.lastFailureTime = time.Now()
}

func (b *compactCircuitBreaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	b.disabled = false
}
