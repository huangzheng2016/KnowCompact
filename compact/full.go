package compact

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// FullCompactor 第3层压缩：LLM 摘要生成（传统压缩）.
//
// 使用 Fork Agent 模式：传入全部消息，生成结构化摘要。
// 核心机制:
//  - 预处理管线: stripImages → stripReinjected → normalize
//  - 结构化输出: <analysis> 思考草稿 + <summary> 9 章节摘要
//  - 重试: 当 LLM 调用失败时最多重试 N 次
//  - PTL 重试: 当压缩请求自身超限时裁剪最旧消息重试
//  - 降级截断: 重试全部失败后，直接截断旧消息并保留最近消息
type FullCompactor struct {
	summarizer    Summarizer
	config        CompactionConfig
	maxPTLRetries int
}

// NewFullCompactor 创建传统压缩器.
func NewFullCompactor(summarizer Summarizer, config CompactionConfig) *FullCompactor {
	return &FullCompactor{
		summarizer:    summarizer,
		config:        config,
		maxPTLRetries: 3,
	}
}

// Compact 执行传统压缩.
//
// 执行流程:
//  1. 预处理消息（替换图片等）
//  2. 调用 LLM 生成摘要（带重试 + PTL 重试）
//  3. 重试全部失败 → 执行降级截断（fallback truncate）
func (f *FullCompactor) Compact(
	ctx context.Context,
	messages []Message,
	promptTemplate string,
	partialDirection string,
) (*CompactionResult, error) {
	if f.summarizer == nil {
		return nil, nil
	}

	tokensBefore := EstimateMessageTokens(messages)

	// 预处理管线
	processed := preprocessMessages(messages)

	// 选择提示词
	prompt := promptTemplate
	if prompt == "" {
		prompt = BaseCompactPrompt
	}

	// 包装消息（添加压缩指令）
	var compactMessages []Message
	compactMessages = append(compactMessages, Message{
		Role:    RoleSystem,
		Content: []ContentBlock{{Type: ContentTypeText, Text: prompt}},
	})
	compactMessages = append(compactMessages, processed...)

	// 调用 LLM 生成摘要（带重试 + PTL 重试）
	summary, err := f.summarizeWithRetry(ctx, compactMessages)
	if err != nil {
		// 重试全部失败 → 执行降级截断
		return f.fallbackTruncate(messages, tokensBefore, partialDirection)
	}

	// 后处理
	formattedSummary := FormatCompactSummary(summary)

	// 组装压缩结果
	result := f.buildCompactionResult(messages, formattedSummary, tokensBefore, partialDirection)
	return result, nil
}

// summarizeWithRetry 带重试的摘要生成.
//
// 重试策略:
//  - 通用错误（网络/超时等）: 最多重试 FullCompactMaxRetries 次
//  - Prompt-Too-Long 错误: 裁剪最旧消息后重试，最多 maxPTLRetries 次
//  - 所有重试都失败后返回 error，触发降级截断
func (f *FullCompactor) summarizeWithRetry(ctx context.Context, messages []Message) (string, error) {
	maxRetries := f.config.FullCompactMaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	currentMessages := messages

	for attempt := 0; attempt <= maxRetries; attempt++ {
		summary, err := f.summarizer.GenerateSummary(ctx, currentMessages)
		if err == nil {
			return summary, nil
		}

		// 如果是 PTL 错误，尝试裁剪后重试
		if isPromptTooLongError(err) {
			ptlSummary, ptlErr := f.summarizeWithPTLRetry(ctx, currentMessages)
			if ptlErr == nil {
				return ptlSummary, nil
			}
			// PTL 重试也失败了，继续下一轮通用重试
		}

		// 最后一次重试也失败了
		if attempt >= maxRetries {
			return "", fmt.Errorf("compact: summarize failed after %d retries: %w", maxRetries, err)
		}
	}

	return "", fmt.Errorf("compact: summarize exhausted all retries")
}

// summarizeWithPTLRetry 带 Prompt-Too-Long 重试的摘要生成.
func (f *FullCompactor) summarizeWithPTLRetry(ctx context.Context, messages []Message) (string, error) {
	currentMessages := messages

	for attempt := 0; attempt <= f.maxPTLRetries; attempt++ {
		summary, err := f.summarizer.GenerateSummary(ctx, currentMessages)
		if err == nil {
			return summary, nil
		}

		// 检查是否为 prompt-too-long 错误
		if !isPromptTooLongError(err) {
			return "", err
		}

		// PTL 重试：裁剪最旧的消息
		if attempt >= f.maxPTLRetries {
			return "", err
		}

		currentMessages = f.truncateForPTLRetry(currentMessages)
		if currentMessages == nil {
			return "", err
		}
	}

	return "", nil
}

// truncateForPTLRetry 裁剪消息以应对 Prompt-Too-Long 错误.
//
// 策略:
//  1. 按 API 轮次分组
//  2. 删除 20% 最旧的组（至少保留 1 组）
//  3. 保证第一条是 user 消息
func (f *FullCompactor) truncateForPTLRetry(messages []Message) []Message {
	groups := groupMessagesByAPIRound(messages)
	if len(groups) < 2 {
		return nil // 无法再裁剪
	}

	// 删除 20% 最旧的组
	dropCount := int(math.Max(1, math.Floor(float64(len(groups))*0.2)))
	dropCount = int(math.Min(float64(dropCount), float64(len(groups)-1)))

	var truncated []Message
	for i := dropCount; i < len(groups); i++ {
		truncated = append(truncated, groups[i]...)
	}

	// 保证第一条是 user 消息
	if len(truncated) > 0 && truncated[0].Role != RoleUser {
		truncated = ensureFirstMessageIsUser(truncated)
	}

	return truncated
}

// fallbackTruncate 降级截断：LLM 摘要全部失败时的兜底策略.
//
// 策略:
//  1. 按配置比例保留最近的消息（默认 30%）
//  2. 至少保留 FallbackTruncateMinKeep 条消息
//  3. 保留时不切断 tool_use/tool_result 配对
//  4. 生成降级提示消息，说明历史消息被截断
//  5. 返回结果，让对话可以继续
func (f *FullCompactor) fallbackTruncate(
	messages []Message,
	tokensBefore int,
	direction string,
) (*CompactionResult, error) {
	return fallbackTruncateDirect(messages, tokensBefore, f.config)
}

// reactiveTruncate 响应式截断 —— API 调用失败后紧急截断.
//
// 与 fallbackTruncate 不同：
//   - 保留比例更激进（默认 20% vs 30%）
//   - 提示词说明是 PTL 错误触发的响应式压缩
//   - 不调用 LLM，直接截断
func (f *FullCompactor) reactiveTruncate(
	messages []Message,
	tokensBefore int,
) (*CompactionResult, error) {
	config := f.config
	// 响应式压缩使用更激进的保留比例（20%）
	config.FallbackTruncateRatio = 0.2
	if config.FallbackTruncateMinKeep <= 0 {
		config.FallbackTruncateMinKeep = 4
	}
	return fallbackTruncateDirect(messages, tokensBefore, config)
}

// fallbackTruncateDirect 直接降级截断（不依赖 FullCompactor）.
//
// 供 ReactiveCompact 和 fallbackTruncate 复用。
func fallbackTruncateDirect(
	messages []Message,
	tokensBefore int,
	config CompactionConfig,
) (*CompactionResult, error) {
	ratio := config.FallbackTruncateRatio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.3
	}
	minKeep := config.FallbackTruncateMinKeep
	if minKeep <= 0 {
		minKeep = 4
	}

	// 计算保留的消息数
	keepCount := int(math.Max(float64(minKeep), math.Ceil(float64(len(messages))*ratio)))
	keepCount = int(math.Min(float64(keepCount), float64(len(messages))))

	// 从末尾向前保留消息，同时确保不切断 tool 配对
	startIndex := len(messages) - keepCount
	if startIndex < 0 {
		startIndex = 0
	}

	// 调整起始索引以保证 API 不变量
	startIndex = adjustIndexToPreserveAPIInvariants(messages, startIndex)

	// 保留的消息
	keptMessages := messages[startIndex:]

	// 生成降级提示
	trigger := "fallback_truncate"
	hint := "LLM 摘要服务失败"
	if ratio <= 0.25 {
		trigger = "reactive_truncate"
		hint = "API 返回 prompt-too-long 错误"
	}

	truncatedCount := startIndex
	fallbackMsg := Message{
		Role: RoleSystem,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: fmt.Sprintf(
				"[%s] %d 条早期消息因 %s 被丢弃。"+
					"仅保留最近 %d 条消息以继续对话。",
				trigger, truncatedCount, hint, len(keptMessages),
			),
		}},
	}

	result := []Message{fallbackMsg}
	result = append(result, keptMessages...)
	tokensAfter := EstimateMessageTokens(result)

	return &CompactionResult{
		WasCompacted:   true,
		Messages:       result,
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		Trigger:        trigger,
		Summary:        fallbackMsg.Content[0].Text,
		BoundaryMarker: true,
	}, nil
}

// buildCompactionResult 组装压缩结果.
func (f *FullCompactor) buildCompactionResult(
	originalMessages []Message,
	summary string,
	tokensBefore int,
	direction string,
) *CompactionResult {
	// 压缩边界标记
	boundary := CreateCompactBoundary(tokensBefore, 0, "full_compact")

	// 摘要消息
	summaryMsg := GetCompactUserSummaryMessage(summary, true, direction == "up_to")

	result := []Message{boundary, summaryMsg}

	// up_to 模式：摘要后跟保留的最近消息
	if direction == "up_to" {
		// 保留最近的消息（由调用方传入）
		// 此处留空，由调用方在 Compact 之后追加
	}

	// 附件重注入：从原始消息中提取最近读取的文件
	injector := NewAttachmentInjector()
	recentFiles := ExtractRecentFilesFromMessages(originalMessages, f.config)
	if len(recentFiles) > 0 {
		injector.WithRecentFiles(recentFiles)
		result = injector.InjectAttachments(result, f.config)
	}

	tokensAfter := EstimateMessageTokens(result)

	return &CompactionResult{
		WasCompacted:   true,
		Messages:       result,
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		Trigger:        "full_compact",
		Summary:        summary,
		BoundaryMarker: true,
	}
}

// preprocessMessages 压缩预处理管线.
//
// 步骤:
//  1. 替换图片/文档为占位符（防止压缩请求自身超限）
//  2. 规范化消息格式
func preprocessMessages(messages []Message) []Message {
	var processed []Message
	for _, msg := range messages {
		processed = append(processed, stripMediaBlocks(msg))
	}
	return processed
}

// stripMediaBlocks 将图片/文档替换为占位符文本.
func stripMediaBlocks(msg Message) Message {
	stripped := Message{
		Role:      msg.Role,
		MessageID: msg.MessageID,
		Usage:     msg.Usage,
		Extra:     msg.Extra,
	}
	for _, block := range msg.Content {
		switch block.Type {
		case ContentTypeImage:
			stripped.Content = append(stripped.Content, ContentBlock{
				Type: ContentTypeText,
				Text: "[image]",
			})
		case ContentTypeThinking:
			// 保留 thinking 内容
			stripped.Content = append(stripped.Content, block)
		default:
			stripped.Content = append(stripped.Content, block)
		}
	}
	return stripped
}

// isPromptTooLongError 判断是否为 prompt-too-long 错误.
func isPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "prompt is too long") ||
		strings.Contains(errMsg, "prompt_too_long") ||
		strings.Contains(errMsg, "context_length_exceeded") ||
		strings.Contains(errMsg, "400") && strings.Contains(errMsg, "token")
}

// PartialCompact 部分压缩：只压缩一部分消息.
//
// direction:
//   - "from": 保留旧消息，只摘要最近消息（默认）
//   - "up_to": 摘要旧消息，保留新消息
func PartialCompact(
	ctx context.Context,
	fullCompactor *FullCompactor,
	messages []Message,
	splitIndex int,
	direction string,
) (*CompactionResult, error) {
	if direction == "up_to" {
		// up_to: 摘要 [0, splitIndex) 的消息
		oldMessages := messages[:splitIndex]
		recentMessages := messages[splitIndex:]

		prompt := PartialCompactUpToPrompt
		result, err := fullCompactor.Compact(ctx, oldMessages, prompt, "up_to")
		if err != nil || result == nil {
			return result, err
		}

		// 追加保留的最近消息
		result.Messages = append(result.Messages, recentMessages...)
		result.TokensAfter = EstimateMessageTokens(result.Messages)
		return result, nil
	}

	// from: 摘要 [splitIndex, end) 的消息（默认）
	recentMessages := messages[splitIndex:]

	prompt := PartialCompactPrompt
	return fullCompactor.Compact(ctx, recentMessages, prompt, "from")
}
