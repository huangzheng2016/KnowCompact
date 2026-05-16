package compact

import (
	"context"
)

// SessionMemoryCompactor 第4层压缩：复用已有 Session Memory 作为摘要.
//
// 不调用 LLM，直接使用后台记忆提取积累的 Session Memory。
// 优势:
//  - <10ms 延迟
//  - 质量可预测（渐进更新，非一次性压缩）
//  - 保留近期消息
type SessionMemoryCompactor struct {
	store  SessionMemoryStore
	config CompactionConfig
}

// NewSessionMemoryCompactor 创建 Session Memory 压缩器.
func NewSessionMemoryCompactor(store SessionMemoryStore, config CompactionConfig) *SessionMemoryCompactor {
	return &SessionMemoryCompactor{store: store, config: config}
}

// TryCompact 尝试使用 Session Memory 进行压缩.
// 返回 nil 表示不适用或失败，需要回退到传统压缩.
func (s *SessionMemoryCompactor) TryCompact(
	ctx context.Context,
	messages []Message,
	autoCompactThreshold int,
) (*CompactionResult, error) {
	// 1. 检查 Session Memory 是否存在且非空
	if s.store == nil {
		return nil, nil
	}
	isEmpty := s.store.IsEmpty(ctx)
	if isEmpty {
		return nil, nil
	}

	// 2. 获取 Session Memory 内容和最后摘要的消息 ID
	sessionMemoryContent, err := s.store.GetMemory(ctx)
	if err != nil || sessionMemoryContent == "" {
		return nil, nil
	}

	lastSummarizedID := s.store.GetLastSummarizedMessageID(ctx)

	// 3. 确定 lastSummarizedIndex
	lastSummarizedIndex := findMessageIndexByID(messages, lastSummarizedID)
	if lastSummarizedIndex < 0 {
		// 恢复的会话：从末尾开始
		lastSummarizedIndex = len(messages) - 1
	}

	// 4. 计算保留消息的起始索引
	keepStartIndex := calculateMessagesToKeepIndex(messages, lastSummarizedIndex, s.config)
	tokensBefore := EstimateMessageTokens(messages)

	// 5. 过滤旧 CompactBoundary
	messagesToKeep := filterOldCompactBoundaries(messages[keepStartIndex:])

	// 6. 截断过大的 Session Memory 章节
	sessionMemoryContent = truncateMemorySections(sessionMemoryContent, s.config.SMMaxTokens)

	// 7. 生成摘要用户消息
	summaryMsg := Message{
		Role: RoleUser,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: `This session is being continued from a previous conversation.
The summary below was progressively generated during the conversation.

` + sessionMemoryContent + `

Recent messages are preserved verbatim below.`,
		}},
	}

	// 8. 组装压缩结果
	boundary := CreateCompactBoundary(tokensBefore, 0, "session_memory")
	result := []Message{boundary, summaryMsg}
	result = append(result, messagesToKeep...)

	// 9. 附件重注入：从原始消息中提取最近读取的文件
	injector := NewAttachmentInjector()
	recentFiles := ExtractRecentFilesFromMessages(messages, s.config)
	if len(recentFiles) > 0 {
		injector.WithRecentFiles(recentFiles)
		result = injector.InjectAttachments(result, s.config)
	}

	tokensAfter := EstimateMessageTokens(result)

	// 10. 检查压缩后 token 是否仍超过阈值
	if tokensAfter >= autoCompactThreshold {
		return nil, nil // 回退到传统压缩
	}

	return &CompactionResult{
		WasCompacted: true,
		Messages:     result,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
		Trigger:      "session_memory",
		Summary:      sessionMemoryContent,
	}, nil
}

// calculateMessagesToKeepIndex 计算保留消息的起始索引.
//
// 核心算法:
//  1. 从 lastSummarizedIndex + 1 开始（Session Memory 未覆盖的消息）
//  2. 如果同时满足 minTokens 和 minTextBlockMessages → 直接返回
//  3. 否则向前扩展，直到满足条件或达到 maxTokens
//  4. 不跨越压缩边界
func calculateMessagesToKeepIndex(messages []Message, lastSummarizedIndex int, config CompactionConfig) int {
	startIndex := lastSummarizedIndex + 1
	if startIndex >= len(messages) {
		return len(messages) - 1
	}

	// 计算当前范围
	currentTokens := EstimateMessageTokens(messages[startIndex:])
	textBlockCount := countTextBlockMessages(messages[startIndex:])

	// 已超 maxTokens → 直接返回
	if currentTokens >= config.SMMaxTokens {
		return startIndex
	}

	// 同时满足 minTokens 和 minTextBlockMessages → 直接返回
	if currentTokens >= config.SMMinTokens && textBlockCount >= config.SMMinTextBlockMessages {
		return adjustIndexToPreserveAPIInvariants(messages, startIndex)
	}

	// 向前扩展
	compactBoundary := findCompactBoundary(messages, startIndex-1)
	for i := startIndex - 1; i > compactBoundary; i-- {
		currentTokens += estimateSingleMessageTokens(messages[i])
		if messages[i].Role == RoleUser || messages[i].Role == RoleAssistant {
			if hasTextBlock(messages[i]) {
				textBlockCount++
			}
		}

		// 停止条件：达到 maxTokens 或同时满足条件
		if currentTokens >= config.SMMaxTokens {
			return adjustIndexToPreserveAPIInvariants(messages, i)
		}
		if currentTokens >= config.SMMinTokens && textBlockCount >= config.SMMinTextBlockMessages {
			return adjustIndexToPreserveAPIInvariants(messages, i)
		}
	}

	// 回退：从 compactBoundary + 1 开始
	if compactBoundary >= 0 {
		return adjustIndexToPreserveAPIInvariants(messages, compactBoundary+1)
	}
	return adjustIndexToPreserveAPIInvariants(messages, 0)
}

// countTextBlockMessages 统计含文本块的消息数量.
func countTextBlockMessages(messages []Message) int {
	count := 0
	for _, msg := range messages {
		if hasTextBlock(msg) {
			count++
		}
	}
	return count
}

// hasTextBlock 判断消息是否包含文本内容.
func hasTextBlock(msg Message) bool {
	for _, block := range msg.Content {
		if block.Type == ContentTypeText && len(block.Text) > 0 {
			return true
		}
	}
	return false
}

// findMessageIndexByID 在消息列表中按 message.id 查找索引.
func findMessageIndexByID(messages []Message, id string) int {
	if id == "" {
		return -1
	}
	for i, msg := range messages {
		if msg.MessageID == id {
			return i
		}
	}
	return -1
}

// filterOldCompactBoundaries 过滤消息列表中的旧压缩边界标记.
func filterOldCompactBoundaries(messages []Message) []Message {
	var filtered []Message
	for _, msg := range messages {
		if !isCompactBoundary(msg) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

// truncateMemorySections 截断过大的 Session Memory 章节.
func truncateMemorySections(memory string, maxTokens int) string {
	estimatedTokens := RoughTokenEstimate(memory)
	if estimatedTokens <= maxTokens {
		return memory
	}

	// 粗略截断：按比例保留字符
	ratio := float64(maxTokens) / float64(estimatedTokens)
	keepChars := int(float64(len(memory)) * ratio * 0.9) // 10% 安全边际
	if keepChars >= len(memory) {
		return memory
	}
	return memory[:keepChars] + "\n\n[Session memory truncated due to token budget]"
}
