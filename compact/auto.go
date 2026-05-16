package compact

import (
	"context"
	"log"
)

// QuerySource 查询来源类型.
type QuerySource string

const (
	QuerySourceNormal        QuerySource = "normal"
	QuerySourceCompact       QuerySource = "compact"
	QuerySourceSessionMemory QuerySource = "session_memory"
)

// AutoCompactor 第2层压缩：自动压缩（阈值判断 + 断路器）.
//
// 核心职责:
//  - shouldAutoCompact 决策树：判断是否应该触发压缩
//  - autoCompactIfNeeded 执行流程：SM 优先，回退到传统压缩
//  - 断路器：连续失败 N 次后停止尝试
type AutoCompactor struct {
	microCompactor    *MicroCompactResult // 最近微压缩结果
	fullCompactor     *FullCompactor
	sessionCompactor  *SessionMemoryCompactor
	config            CompactionConfig
	circuitBreaker    *compactCircuitBreaker
	consecutiveFailures int
}

// NewAutoCompactor 创建自动压缩器.
func NewAutoCompactor(
	fullCompactor *FullCompactor,
	sessionCompactor *SessionMemoryCompactor,
	config CompactionConfig,
) *AutoCompactor {
	return &AutoCompactor{
		fullCompactor:    fullCompactor,
		sessionCompactor: sessionCompactor,
		config:           config,
		circuitBreaker:   newCircuitBreaker(config.MaxConsecutiveFailures),
	}
}

// ShouldAutoCompact 决策树：判断是否应该触发自动压缩.
//
// 决策树:
//  1. querySource 是 compact 或 session_memory？→ false（防止递归死锁）
//  2. 自动压缩未启用？→ false
//  3. 断路器打开？→ false
//  4. token 数超过阈值？→ true
func (a *AutoCompactor) ShouldAutoCompact(
	messages []Message,
	modelMaxTokens int,
	querySource QuerySource,
) bool {
	// 防止递归死锁：压缩代理自身不触发压缩
	if querySource == QuerySourceCompact || querySource == QuerySourceSessionMemory {
		return false
	}

	// 自动压缩未启用
	if !a.config.AutoCompactEnabled {
		return false
	}

	// 断路器打开
	if a.circuitBreaker.isOpen() {
		return false
	}

	// 计算阈值并检查
	threshold := GetAutoCompactThreshold(
		"", // model 参数用于上下文窗口查询
		a.config.AutoCompactBufferTokens,
		a.config.MaxOutputTokensForSummary,
	)
	// 使用传入的模型最大 token 数计算实际阈值
	if modelMaxTokens > 0 {
		effectiveWindow := modelMaxTokens - a.config.MaxOutputTokensForSummary
		threshold = effectiveWindow - a.config.AutoCompactBufferTokens
	}

	currentTokens := EstimateMessageTokensPrecise(messages)
	return currentTokens >= threshold
}

// AutoCompactIfNeeded 自动压缩执行流程.
//
// 流程:
//  1. 断路器检查
//  2. ShouldAutoCompact 判断
//  3. 优先尝试 Session Memory 压缩
//  4. 回退到传统压缩
//  5. 记录失败，触发断路器
func (a *AutoCompactor) AutoCompactIfNeeded(
	ctx context.Context,
	messages []Message,
	modelMaxTokens int,
	querySource QuerySource,
) (*CompactionResult, error) {
	// 断路器检查
	if a.circuitBreaker.isOpen() {
		return &CompactionResult{WasCompacted: false}, nil
	}

	// 阈值判断
	if !a.ShouldAutoCompact(messages, modelMaxTokens, querySource) {
		return &CompactionResult{WasCompacted: false}, nil
	}

	tokensBefore := EstimateMessageTokens(messages)
	threshold := GetAutoCompactThreshold("", a.config.AutoCompactBufferTokens, a.config.MaxOutputTokensForSummary)
	if modelMaxTokens > 0 {
		effectiveWindow := modelMaxTokens - a.config.MaxOutputTokensForSummary
		threshold = effectiveWindow - a.config.AutoCompactBufferTokens
	}

	// 触发回调
	if a.config.OnCompactionStart != nil {
		a.config.OnCompactionStart(CompactionInfo{
			Trigger:      "auto_compact",
			TokensBefore: tokensBefore,
			Threshold:    threshold,
			Layer:        2,
		})
	}

	// 优先尝试 Session Memory 压缩
	if a.sessionCompactor != nil {
		result, err := a.sessionCompactor.TryCompact(ctx, messages, threshold)
		if err == nil && result != nil && result.WasCompacted {
			a.circuitBreaker.recordSuccess()
			if a.config.OnCompactionEnd != nil {
				a.config.OnCompactionEnd(*result)
			}
			return result, nil
		}
	}

	// 回退到传统压缩
	if a.fullCompactor != nil {
		result, err := a.fullCompactor.Compact(ctx, messages, BaseCompactPrompt, "from")
		if err == nil && result != nil && result.WasCompacted {
			a.circuitBreaker.recordSuccess()
			if a.config.OnCompactionEnd != nil {
				a.config.OnCompactionEnd(*result)
			}
			return result, nil
		}
	}

	// 记录失败
	a.circuitBreaker.recordFailure()
	a.consecutiveFailures++

	if a.consecutiveFailures >= a.config.MaxConsecutiveFailures {
		log.Printf("[compact] circuit breaker triggered after %d consecutive failures, "+
			"auto-compact disabled for this session", a.consecutiveFailures)
	}

	return &CompactionResult{WasCompacted: false}, nil
}

// GetWarningThreshold 获取警告阈值（token 超过此值发出警告）.
func (a *AutoCompactor) GetWarningThreshold(modelMaxTokens int) int {
	if modelMaxTokens > 0 {
		return modelMaxTokens - a.config.WarningThresholdBuffer
	}
	return GetContextWindow("") - a.config.WarningThresholdBuffer
}

// IsNearLimit 判断是否接近上下文限制.
func (a *AutoCompactor) IsNearLimit(messages []Message, modelMaxTokens int) bool {
	return EstimateMessageTokensPrecise(messages) >= a.GetWarningThreshold(modelMaxTokens)
}

// Reset 重置自动压缩器状态.
func (a *AutoCompactor) Reset() {
	a.circuitBreaker.reset()
	a.consecutiveFailures = 0
}
