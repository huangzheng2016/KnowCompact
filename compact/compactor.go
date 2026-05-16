package compact

import (
	"context"
	"fmt"
)

// DefaultCompactor 默认压缩器——编排 4 层压缩体系.
//
// 每轮查询前执行:
//
//	用户消息 → [Layer 1: MicroCompact] → [Layer 2: AutoCompact] → API 调用
//	              ↓                          ↓
//	         细粒度清理旧工具输出        阈值触发时:
//	         (不丢语义, <1ms)          → Layer 4: Session Memory (<10ms)
//	                                   → Layer 3: Full Compact (5-30s)
//
// 实现 Compactor 接口，可作为 eino 组件使用.
type DefaultCompactor struct {
	config           CompactionConfig
	fullCompactor    *FullCompactor
	sessionCompactor *SessionMemoryCompactor
	autoCompactor    *AutoCompactor
}

// NewDefaultCompactor 创建默认压缩器.
//
// 参数:
//   - config: 压缩配置
//   - summarizer: LLM 摘要生成器（用于 FullCompact，可为 nil 禁用第3层）
//   - memoryStore: Session Memory 存储（用于第4层，可为 nil 禁用）
func NewDefaultCompactor(
	config CompactionConfig,
	summarizer Summarizer,
	memoryStore SessionMemoryStore,
) *DefaultCompactor {
	c := &DefaultCompactor{config: config}

	if summarizer != nil {
		c.fullCompactor = NewFullCompactor(summarizer, config)
	}
	if memoryStore != nil {
		c.sessionCompactor = NewSessionMemoryCompactor(memoryStore, config)
	}

	c.autoCompactor = NewAutoCompactor(c.fullCompactor, c.sessionCompactor, config)
	return c
}

// Compact 对消息列表执行完整的压缩管线.
//
// 管线:
//  1. PreCompact 钩子（用户可改写消息列表）
//  2. MicroCompact: 清理旧的工具输出
//  3. AutoCompact: 如果超过阈值，触发 SM 或 Full Compact
//  4. PostCompact 钩子（用户可改写压缩结果）
func (c *DefaultCompactor) Compact(
	ctx context.Context,
	messages []Message,
	modelMaxTokens int,
) (*CompactionResult, error) {
	// PreCompact 钩子
	if c.config.PreCompact != nil {
		rewritten, err := c.config.PreCompact(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("compact: pre-compact hook failed: %w", err)
		}
		if rewritten != nil {
			messages = rewritten
		}
	}

	// Layer 1: MicroCompact
	microCompacted := false
	microTokensFreed := 0
	if c.config.MicroCompactEnabled {
		mcResult := MicroCompact(messages, c.config.RecentToolsKeep, c.config.MicroCompactCaseInsensitive, c.config.MicroCompactWhitelist)
		messages = mcResult.Messages
		microCompacted = mcResult.WasCompacted
		microTokensFreed = mcResult.TokensFreed
	}

	// Layer 2-4: AutoCompact
	result, err := c.autoCompactor.AutoCompactIfNeeded(
		ctx, messages, modelMaxTokens, QuerySourceNormal,
	)
	if err != nil {
		return nil, err
	}
	if result.WasCompacted {
		// AutoCompact 已触发，累加 MicroCompact 的节省
		if microCompacted {
			result.TokensBefore += microTokensFreed
			result.Trigger = "micro+" + result.Trigger
		}
		return c.applyPostCompact(ctx, result)
	}

	// 不需要 AutoCompact，返回当前状态
	tokens := EstimateMessageTokens(messages)
	if microCompacted {
		// MicroCompact 修改了消息但 AutoCompact 未触发
		return c.applyPostCompact(ctx, &CompactionResult{
			WasCompacted: true,
			Trigger:      "micro_compact",
			Messages:     messages,
			TokensBefore: tokens + microTokensFreed,
			TokensAfter:  tokens,
		})
	}

	return &CompactionResult{
		WasCompacted: false,
		Messages:     messages,
		TokensBefore: tokens,
		TokensAfter:  tokens,
	}, nil
}

// applyPostCompact 在压缩结果上应用 PostCompact 钩子.
//
// 钩子返回的结果替代原结果；钩子返回 error 时整次压缩视为失败，
// 调用方应将其视为 Compact 失败并触发对应错误处理。
func (c *DefaultCompactor) applyPostCompact(
	ctx context.Context,
	result *CompactionResult,
) (*CompactionResult, error) {
	if c.config.PostCompact == nil || result == nil {
		return result, nil
	}
	rewritten, err := c.config.PostCompact(ctx, result)
	if err != nil {
		return nil, fmt.Errorf("compact: post-compact hook failed: %w", err)
	}
	if rewritten == nil {
		return result, nil
	}
	return rewritten, nil
}

// ShouldCompact 判断是否应该触发压缩.
func (c *DefaultCompactor) ShouldCompact(messages []Message, modelMaxTokens int) bool {
	return c.autoCompactor.ShouldAutoCompact(messages, modelMaxTokens, QuerySourceNormal)
}

// Reset 重置内部状态.
func (c *DefaultCompactor) Reset() {
	c.autoCompactor.Reset()
}

// CompactOnly 仅执行 MicroCompact（不触发 AutoCompact）.
func (c *DefaultCompactor) CompactOnly(messages []Message) []Message {
	if c.config.MicroCompactEnabled {
		return MicroCompact(messages, c.config.RecentToolsKeep, c.config.MicroCompactCaseInsensitive, c.config.MicroCompactWhitelist).Messages
	}
	return messages
}

// ManualCompact 手动触发压缩（绕过阈值检查）.
func (c *DefaultCompactor) ManualCompact(
	ctx context.Context,
	messages []Message,
	modelMaxTokens int,
) (*CompactionResult, error) {
	tokensBefore := EstimateMessageTokens(messages)

	if c.config.OnCompactionStart != nil {
		c.config.OnCompactionStart(CompactionInfo{
			Trigger:      "manual_compact",
			TokensBefore: tokensBefore,
			Layer:        3,
		})
	}

	// 手动压缩直接使用传统压缩
	if c.fullCompactor == nil {
		return &CompactionResult{
			WasCompacted: false,
			Messages:     messages,
			TokensBefore: tokensBefore,
			TokensAfter:  tokensBefore,
		}, nil
	}

	result, err := c.fullCompactor.Compact(ctx, messages, BaseCompactPrompt, "from")
	if err != nil {
		return nil, err
	}

	if c.config.OnCompactionEnd != nil && result != nil {
		c.config.OnCompactionEnd(*result)
	}

	return c.applyPostCompact(ctx, result)
}

// GetAutoCompactor 获取内部 AutoCompactor（用于高级定制）.
func (c *DefaultCompactor) GetAutoCompactor() *AutoCompactor {
	return c.autoCompactor
}

// GetFullCompactor 获取内部 FullCompactor（用于高级定制）.
func (c *DefaultCompactor) GetFullCompactor() *FullCompactor {
	return c.fullCompactor
}

// GetSessionMemoryCompactor 获取内部 SessionMemoryCompactor（用于高级定制）.
func (c *DefaultCompactor) GetSessionMemoryCompactor() *SessionMemoryCompactor {
	return c.sessionCompactor
}

// Config 获取压缩配置.
func (c *DefaultCompactor) Config() CompactionConfig {
	return c.config
}

// CompactWithDirection 支持部分压缩方向的完整压缩.
func (c *DefaultCompactor) CompactWithDirection(
	ctx context.Context,
	messages []Message,
	modelMaxTokens int,
	direction string,
	splitIndex int,
) (*CompactionResult, error) {
	// MicroCompact 预处理
	if c.config.MicroCompactEnabled {
		messages = MicroCompact(messages, c.config.RecentToolsKeep, c.config.MicroCompactCaseInsensitive, c.config.MicroCompactWhitelist).Messages
	}

	if c.fullCompactor == nil {
		tokens := EstimateMessageTokens(messages)
		return &CompactionResult{
			WasCompacted: false,
			Messages:     messages,
			TokensBefore: tokens,
			TokensAfter:  tokens,
		}, nil
	}

	return PartialCompact(ctx, c.fullCompactor, messages, splitIndex, direction)
}

// ReactiveCompact 响应式压缩 —— API 调用失败后调用.
//
// 场景：API 返回 prompt-too-long 错误，需要紧急截断消息以继续对话。
// 与主动压缩不同，响应式压缩：
//   - 不调用 LLM 生成摘要（避免再次触发 PTL）
//   - 直接执行降级截断，保留最近消息
//   - 使用更激进的保留比例（默认 20%）
//
// 这是 Claude Code 5 层错误恢复中的第 2 层。
func (c *DefaultCompactor) ReactiveCompact(
	ctx context.Context,
	messages []Message,
	modelMaxTokens int,
) (*CompactionResult, error) {
	// 先执行 MicroCompact
	if c.config.MicroCompactEnabled {
		messages = MicroCompact(messages, c.config.RecentToolsKeep, c.config.MicroCompactCaseInsensitive, c.config.MicroCompactWhitelist).Messages
	}

	tokensBefore := EstimateMessageTokens(messages)

	// 使用 FullCompactor 的降级截断（更激进的保留比例）
	if c.fullCompactor != nil {
		return c.fullCompactor.reactiveTruncate(messages, tokensBefore)
	}

	// 无 FullCompactor：直接截断保留最近 20%
	return fallbackTruncateDirect(messages, tokensBefore, c.config)
}
