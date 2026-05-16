// failure_scenarios 演示 KnowCompact 在各类异常路径下的兜底行为.
//
// 涵盖以下场景:
//  1. Summarizer 网络全部失败 → 触发 fallback truncate
//  2. Prompt-Too-Long 错误 → PTL 重试 + 渐进裁剪
//  3. 断路器：连续 3 次失败后自动停止尝试
//  4. PreCompact 钩子返回 error → 整次压缩被中止
//  5. 自定义 PromptTooLongDetector → SDK 私有错误也能被识别
//
// 运行: go run ./examples/failure_scenarios
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/huangzheng2016/KnowCompact/compact"
)

// ============================================================
// flakySummarizer 模拟 LLM 摘要器的不同失败模式
// ============================================================

type summarizerMode int

const (
	modeAlwaysFail summarizerMode = iota
	modeAlwaysPTL
	modeFailThenOK
	modeCustomSDKError
)

type flakySummarizer struct {
	mode  summarizerMode
	calls int
}

var errCustomSDK = errors.New("MY_SDK_PROMPT_LIMIT_REACHED")

func (f *flakySummarizer) GenerateSummary(ctx context.Context, msgs []compact.Message) (string, error) {
	f.calls++
	switch f.mode {
	case modeAlwaysFail:
		return "", fmt.Errorf("call #%d: simulated network error", f.calls)
	case modeAlwaysPTL:
		// 用哨兵错误包裹，演示推荐做法
		return "", fmt.Errorf("call #%d: %w", f.calls, compact.ErrPromptTooLong)
	case modeFailThenOK:
		if f.calls < 3 {
			return "", fmt.Errorf("call #%d: transient error", f.calls)
		}
		return "<分析>recovered</分析>\n<摘要>1. 摘要生成成功</摘要>", nil
	case modeCustomSDKError:
		return "", errCustomSDK
	}
	return "", nil
}

// ============================================================
// 场景执行
// ============================================================

func main() {
	fmt.Println("=== KnowCompact 失败路径演示 ===")
	fmt.Println()

	scenarioFallbackTruncate()
	fmt.Println()
	scenarioPTLRetry()
	fmt.Println()
	scenarioCircuitBreaker()
	fmt.Println()
	scenarioPreCompactAbort()
	fmt.Println()
	scenarioCustomDetector()
}

// 构造一段足够长的消息，确保超过自动压缩阈值.
func longMessages(n int) []compact.Message {
	var msgs []compact.Message
	// 第一条 user 永远 pinned
	msgs = append(msgs, compact.Message{
		Role: compact.RoleUser,
		Content: []compact.ContentBlock{{
			Type: compact.ContentTypeText,
			Text: "[ORIGINAL TASK] Investigate why the build is failing.",
		}},
	})
	for i := 0; i < n; i++ {
		role := compact.RoleUser
		if i%2 == 0 {
			role = compact.RoleAssistant
		}
		msgs = append(msgs, compact.Message{
			Role: role,
			Content: []compact.ContentBlock{{
				Type: compact.ContentTypeText,
				Text: strings.Repeat(fmt.Sprintf("turn-%03d ", i), 200),
			}},
		})
	}
	return msgs
}

func scenarioFallbackTruncate() {
	fmt.Println("--- 场景 1: Summarizer 始终失败 → fallback truncate ---")
	cfg := compact.DefaultCompactionConfig().
		WithLogLevel(compact.LogLevelInfo)
	compactor := compact.NewDefaultCompactor(cfg, &flakySummarizer{mode: modeAlwaysFail}, nil)

	// 用很小的 modelMaxTokens 强制触发自动压缩
	result, err := compactor.Compact(context.Background(), longMessages(40), 8_000)
	if err != nil {
		fmt.Printf("  压缩错误: %v\n", err)
		return
	}
	fmt.Printf("  trigger=%s tokens %d→%d messages=%d\n",
		result.Trigger, result.TokensBefore, result.TokensAfter, len(result.Messages))

	// 验证首条 user 仍然存在（pinned）
	found := false
	for _, m := range result.Messages {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "[ORIGINAL TASK]") {
				found = true
			}
		}
	}
	fmt.Printf("  pinned 首条 user 是否保留: %v\n", found)
}

func scenarioPTLRetry() {
	fmt.Println("--- 场景 2: 持续 PTL 错误 → PTL 重试 + 渐进裁剪 → 最终 fallback ---")
	cfg := compact.DefaultCompactionConfig()
	summarizer := &flakySummarizer{mode: modeAlwaysPTL}
	compactor := compact.NewDefaultCompactor(cfg, summarizer, nil)

	result, _ := compactor.Compact(context.Background(), longMessages(40), 8_000)
	fmt.Printf("  summarizer 被调用 %d 次（含 PTL 重试 + 通用重试）\n", summarizer.calls)
	fmt.Printf("  trigger=%s tokens %d→%d\n",
		result.Trigger, result.TokensBefore, result.TokensAfter)
}

func scenarioCircuitBreaker() {
	fmt.Println("--- 场景 3: 断路器 —— 连续失败后停止尝试 ---")
	cfg := compact.DefaultCompactionConfig().
		WithFullCompactMaxRetries(0) // 每次失败立即记一票
	compactor := compact.NewDefaultCompactor(cfg, &flakySummarizer{mode: modeAlwaysFail}, nil)
	auto := compactor.GetAutoCompactor()

	for i := 0; i < 5; i++ {
		_, _ = compactor.Compact(context.Background(), longMessages(40), 8_000)
		fmt.Printf("  第 %d 轮：失败计数=%d, 断路器开=%v\n",
			i+1, auto.ConsecutiveFailures(), auto.IsCircuitOpen())
	}
}

func scenarioPreCompactAbort() {
	fmt.Println("--- 场景 4: PreCompact 钩子返回 error → 压缩中止 ---")
	cfg := compact.DefaultCompactionConfig().
		WithPreCompact(func(ctx context.Context, msgs []compact.Message) ([]compact.Message, error) {
			return nil, errors.New("用户策略拒绝压缩此会话")
		})
	compactor := compact.NewDefaultCompactor(cfg, &flakySummarizer{mode: modeFailThenOK}, nil)
	_, err := compactor.Compact(context.Background(), longMessages(40), 8_000)
	fmt.Printf("  Compact 返回错误: %v\n", err)
}

func scenarioCustomDetector() {
	fmt.Println("--- 场景 5: 自定义 PromptTooLongDetector ---")
	cfg := compact.DefaultCompactionConfig().
		WithPromptTooLongDetector(func(err error) bool {
			// 私有 SDK 错误：仅靠默认短语库无法识别
			return err != nil && strings.Contains(err.Error(), "MY_SDK_PROMPT_LIMIT")
		})
	summarizer := &flakySummarizer{mode: modeCustomSDKError}
	compactor := compact.NewDefaultCompactor(cfg, summarizer, nil)

	_, _ = compactor.Compact(context.Background(), longMessages(40), 8_000)
	fmt.Printf("  summarizer 被调用 %d 次（自定义 detector 命中后走 PTL 重试路径）\n", summarizer.calls)
}
