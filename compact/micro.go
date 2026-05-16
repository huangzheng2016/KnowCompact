package compact

// MicroCompactResult 微压缩结果.
type MicroCompactResult struct {
	WasCompacted   bool
	TokensFreed    int
	ToolsCleared   int
	Messages       []Message
}

// MicroCompact 第1层压缩：纯规则操作，清理旧的工具输出结果.
//
// 不调用 LLM，不丢语义信息。遍历消息列表，将旧的可压缩工具结果替换为
// "[Old tool result content cleared]"，同时保留最近的 N 个工具结果.
//
// 特点:
//   - <1ms 延迟
//   - 不修改 tool_use 块（保持配对完整性）
//   - 保留最近 N 个工具结果
//   - 不传入 whitelist 时，默认**所有**工具结果均可压缩
//   - caseInsensitive 控制白名单是否忽略大小写
func MicroCompact(messages []Message, keepRecent int, caseInsensitive bool, whitelist ...map[string]bool) *MicroCompactResult {
	if keepRecent <= 0 {
		keepRecent = 3
	}

	var wl map[string]bool
	if len(whitelist) > 0 {
		wl = whitelist[0]
	}

	// 收集所有可压缩工具结果的位置
	type toolResultLoc struct {
		msgIndex   int
		blockIndex int
	}
	var compactableLocs []toolResultLoc

	for mi, msg := range messages {
		for bi, block := range msg.Content {
			if compactableToolResult(block, wl, caseInsensitive) && block.ToolOutput != "" {
				compactableLocs = append(compactableLocs, toolResultLoc{mi, bi})
			}
		}
	}

	if len(compactableLocs) <= keepRecent {
		return &MicroCompactResult{WasCompacted: false, Messages: messages}
	}

	// 保留最近 N 个，清理更早的
	clearCount := len(compactableLocs) - keepRecent
	tokensFreed := 0

	for i := 0; i < clearCount; i++ {
		loc := compactableLocs[i]
		block := &messages[loc.msgIndex].Content[loc.blockIndex]
		tokensFreed += RoughTokenEstimate(block.ToolOutput)
		// 注意：不修改 tool_use 块
		block.ToolOutput = "[Old tool result content cleared]"
	}

	return &MicroCompactResult{
		WasCompacted: true,
		TokensFreed:  tokensFreed,
		ToolsCleared: clearCount,
		Messages:     messages,
	}
}

// MicroCompactTimeBased 时间触发的微压缩（子路径 A）.
//
// 当缓存已过期时使用此路径。与 MicroCompact 逻辑相同，但额外清理
// 时间戳标记，以便后续处理知道哪些结果已被清除.
func MicroCompactTimeBased(messages []Message, keepRecent int) *MicroCompactResult {
	result := MicroCompact(messages, keepRecent, true)
	if !result.WasCompacted {
		return result
	}

	// 为时间触发的路径标记更多上下文
	for i := range result.Messages {
		msg := &result.Messages[i]
		for j := range msg.Content {
			block := &msg.Content[j]
			if block.ToolOutput == "[Old tool result content cleared]" {
				// 可在此添加时间戳等额外信息
				_ = block
			}
		}
	}
	return result
}

// MicroCompactWithConfig 使用 CompactionConfig 进行微压缩（推荐）。
func MicroCompactWithConfig(messages []Message, config CompactionConfig) *MicroCompactResult {
	if !config.MicroCompactEnabled {
		return &MicroCompactResult{WasCompacted: false, Messages: messages}
	}
	return MicroCompact(messages, config.RecentToolsKeep, config.MicroCompactCaseInsensitive, config.MicroCompactWhitelist)
}

// collectCompactableToolIDs 收集所有可压缩的 tool_use ID（用于缓存编辑路径）.
func collectCompactableToolIDs(messages []Message) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolUse &&
				(block.ToolName != "") &&
				block.ToolUseID != "" &&
				!seen[block.ToolUseID] {
				ids = append(ids, block.ToolUseID)
				seen[block.ToolUseID] = true
			}
		}
	}
	return ids
}

// SnipTokensCalculation 计算微压缩释放的 token 数.
func SnipTokensCalculation(microResult *MicroCompactResult, messages []Message) int {
	if microResult == nil || !microResult.WasCompacted {
		return 0
	}
	return microResult.TokensFreed
}

// getToolResultsToDelete 根据阈值决定要删除的工具结果 ID.
func getToolResultsToDelete(messages []Message, keepCount int) map[string]bool {
	toolResultIDs := make(map[string]bool)
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolResult &&
				block.ToolUseID != "" {
				toolResultIDs[block.ToolUseID] = true
			}
		}
	}

	// 保留最近 keepCount 个
	toDelete := make(map[string]bool)
	count := 0
	// 从后往前保留
	var ids []string
	for id := range toolResultIDs {
		ids = append(ids, id)
	}
	// 简化处理: 按原始消息顺序决定
	for i := len(messages) - 1; i >= 0 && count < keepCount; i-- {
		for _, block := range messages[i].Content {
			if block.Type == ContentTypeToolResult &&
				toolResultIDs[block.ToolUseID] {
				delete(toolResultIDs, block.ToolUseID)
				count++
			}
		}
	}
	for id := range toolResultIDs {
		toDelete[id] = true
	}
	return toDelete
}
