package compact

import (
	"context"
)

// CompactionHook 上下文压缩钩子 —— 以 callback/hook 方式透明集成.
//
// 提供两种注入方式:
//   方式 A: 作为 Graph 节点选项 (WithStatePreHandler) —— 绑定到特定 ChatModel 节点
//   方式 B: 作为全局回调处理器 —— 对所有 ChatModel 节点生效
//
// 两种方式都无需修改 agent 核心逻辑，纯 Hook 注入.

// CompactionHook 压缩钩子配置.
//
// 零值可用 (字段均为可选):
//
//	hook := &CompactionHook{Compactor: compactor, ModelMaxTokens: 200_000}
type CompactionHook struct {
	// Compactor 压缩器实例 (必填).
	Compactor *DefaultCompactor

	// ModelMaxTokens 模型上下文窗口大小 (默认 200_000).
	ModelMaxTokens int

	// MicroCompactOnly 仅执行微压缩，不触发自动压缩 (默认 false).
	// 设为 true 时，Hook 仅做每轮工具结果清理，不做 LLM 摘要.
	MicroCompactOnly bool

	// Silent 静默模式，关闭日志 (默认 false).
	//
	// Deprecated: 使用 Logger / LogLevel 进行更细粒度控制。
	// 当 Logger 为 nil 时，Silent=true 等价于 LogLevel=LogLevelSilent。
	Silent bool

	// Logger 自定义日志实现。
	// nil 时使用 Compactor.Config().Logger，再 fallback 到默认 logger。
	Logger Logger

	// LogLevel 当未提供 Logger 时控制默认 logger 的级别。
	LogLevel LogLevel

	// OnCompacted 压缩完成回调 (可选).
	// 可用于记录指标、审计日志.
	OnCompacted func(result *CompactionResult)
}

func (h *CompactionHook) modelMaxTokens() int {
	if h.ModelMaxTokens <= 0 {
		return 200_000
	}
	return h.ModelMaxTokens
}

// logger 解析当前 hook 使用的 logger:
//  1. 显式设置的 h.Logger 最优先
//  2. Silent=true 时返回 nopLogger（向后兼容）
//  3. 回退到 hook 自己的 LogLevel 或 compactor config
func (h *CompactionHook) logger() Logger {
	if h.Logger != nil {
		return h.Logger
	}
	if h.Silent {
		return NewNopLogger()
	}
	if h.LogLevel != LogLevelUnset {
		return NewStdLogger(h.LogLevel, nil)
	}
	if h.Compactor != nil {
		return getLogger(h.Compactor.Config())
	}
	return NewDefaultLogger()
}

// ============================================================
// 方式 A: 作为 eino Graph 节点选项
// ============================================================
//
// 使用示例:
//
//	hook := &CompactionHook{Compactor: compactor, ModelMaxTokens: 200_000}
//	graph.AddChatModelNode("chat_model", chatModel,
//	    hook.AsNodeOption(),
//	)
//
// 效果: 每次 ChatModel.Generate 调用前，自动执行微压缩 + 阈值检查.

// PreProcess 在 ChatModel 调用前执行压缩.
//
// 此方法签名兼容 eino 的 StatePreHandler.
// 实际集成时签名需匹配 eino 的 func(ctx, []*schema.Message, state) ([]*schema.Message, error).
func (h *CompactionHook) PreProcess(ctx context.Context, messages []Message, state interface{}) ([]Message, error) {
	if h.Compactor == nil {
		return messages, nil
	}
	log := h.logger()

	originalTokens := EstimateMessageTokens(messages)

	// Layer 1: MicroCompact（每轮自动）
	if h.Compactor.config.MicroCompactEnabled {
		mcResult := MicroCompact(messages, h.Compactor.config.RecentToolsKeep, h.Compactor.config.MicroCompactCaseInsensitive, h.Compactor.config.MicroCompactWhitelist)
		messages = mcResult.Messages
		if mcResult.WasCompacted {
			log.Debug("micro-compact",
				"tools_cleared", mcResult.ToolsCleared,
				"tokens_freed", mcResult.TokensFreed)
		}
	}

	// MicroCompactOnly 模式：跳过自动压缩
	if h.MicroCompactOnly {
		return messages, nil
	}

	// Layer 2-4: 自动压缩阈值检查
	if !h.Compactor.ShouldCompact(messages, h.modelMaxTokens()) {
		return messages, nil
	}

	log.Info("auto-compact triggered",
		"tokens_before", originalTokens)

	result, err := h.Compactor.Compact(ctx, messages, h.modelMaxTokens())
	if err != nil {
		log.Error("auto-compact failed", "error", err)
		return messages, nil
	}

	if result.WasCompacted {
		log.Info("compact completed",
			"tokens_before", result.TokensBefore,
			"tokens_after", result.TokensAfter,
			"trigger", result.Trigger)

		if h.OnCompacted != nil {
			h.OnCompacted(result)
		}
		return result.Messages, nil
	}

	return messages, nil
}

// PostProcess 在 ChatModel 调用后执行（可用于记录 token 使用）.
func (h *CompactionHook) PostProcess(ctx context.Context, messages []Message, state interface{}) ([]Message, error) {
	// 可选：记录模型实际返回的 token 使用量
	tokens := EstimateMessageTokens(messages)
	_ = tokens
	return messages, nil
}

// ============================================================
// CompactionHook 构造器
// ============================================================

// NewCompactionHook 创建压缩钩子.
func NewCompactionHook(compactor *DefaultCompactor, modelMaxTokens int) *CompactionHook {
	return &CompactionHook{
		Compactor:      compactor,
		ModelMaxTokens: modelMaxTokens,
	}
}

// NewMicroCompactHook 创建仅微压缩的钩子（最轻量，无 LLM 调用）.
func NewMicroCompactHook(compactor *DefaultCompactor) *CompactionHook {
	return &CompactionHook{
		Compactor:        compactor,
		MicroCompactOnly: true,
	}
}

// WithCallback 设置压缩完成回调.
func (h *CompactionHook) WithCallback(fn func(result *CompactionResult)) *CompactionHook {
	h.OnCompacted = fn
	return h
}

// WithSilent 设置静默模式.
//
// Deprecated: 使用 WithLogger(compact.NewNopLogger()) 或
// WithLogLevel(compact.LogLevelSilent) 替代。
func (h *CompactionHook) WithSilent(silent bool) *CompactionHook {
	h.Silent = silent
	return h
}

// WithLogger 设置自定义 Logger.
func (h *CompactionHook) WithLogger(logger Logger) *CompactionHook {
	h.Logger = logger
	return h
}

// WithLogLevel 设置默认 logger 的级别.
func (h *CompactionHook) WithLogLevel(level LogLevel) *CompactionHook {
	h.LogLevel = level
	return h
}
