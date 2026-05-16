package compact

import "strings"

// collectToolUseIDs 收集消息列表中所有 tool_use 的 ID.
func collectToolUseIDs(messages []Message) map[string]bool {
	ids := make(map[string]bool)
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolUse && block.ToolUseID != "" {
				ids[block.ToolUseID] = true
			}
		}
	}
	return ids
}

// collectToolResultIDs 收集消息列表中所有 tool_result 引用的 tool_use_id.
func collectToolResultIDs(messages []Message) map[string]bool {
	ids := make(map[string]bool)
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolResult && block.ToolUseID != "" {
				ids[block.ToolUseID] = true
			}
		}
	}
	return ids
}

// collectMessageIDs 收集消息列表中所有的 message.id（assistant 消息）.
func collectMessageIDs(messages []Message) map[string]bool {
	ids := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == RoleAssistant && msg.MessageID != "" {
			ids[msg.MessageID] = true
		}
	}
	return ids
}

// adjustIndexToPreserveAPIInvariants 调整起始索引以保证 API 不变量.
//
// 两条核心规则:
//  1. 不能切断 tool_use / tool_result 配对 —— 孤立 tool_result 会导致 API 报错
//  2. 不能分离共享 message.id 的流式块 —— normalize 合并时会丢失 thinking
//
// 算法: 迭代直到不动点（先 thinking 分离 → 再配对修复）。
// 先 thinking 是因为 thinking 分离通常产生更小的索引，扩展后可能
// 揭示新的配对问题，需要再次修复。
//
// 返回调整后的 startIndex.
func adjustIndexToPreserveAPIInvariants(messages []Message, startIndex int) int {
	if startIndex <= 0 || startIndex >= len(messages) {
		return startIndex
	}

	adjusted := startIndex
	// 迭代直到不动点，上限 len(messages) 次防止无限循环
	for i := 0; i < len(messages); i++ {
		prev := adjusted
		// 步骤1: 先修复 thinking 块分离（通常产生更小的索引）
		adjusted = fixThinkingBlockSeparation(messages, adjusted)
		// 步骤2: 再用新索引修复 tool_use / tool_result 配对
		adjusted = fixToolUseToolResultPairing(messages, adjusted)
		if adjusted == prev {
			break // 不动点，收敛
		}
	}

	return adjusted
}

// fixToolUseToolResultPairing 确保不切断 tool_use/tool_result 配对.
//
// 场景:
//
//	Index N:   assistant, content: [tool_use: ID_A]
//	Index N+1: user,      content: [tool_result: ID_A, tool_result: ID_B]
//
// 如果 startIndex = N+1，则:
//
//	保留范围 [N+1, end] 中有 tool_result: ID_A 和 tool_result: ID_B
//	但 tool_use: ID_B 在范围外（在前面某条被切掉的消息中）
//	→ ID_B 的 tool_result 变成孤立引用 → API 报错
//
// 解决方案: 向前搜索，把包含缺失 tool_use 的消息纳入范围.
func fixToolUseToolResultPairing(messages []Message, startIndex int) int {
	// 收集保留范围内的 tool_use ID
	keptToolUseIDs := collectToolUseIDs(messages[startIndex:])
	// 收集保留范围内的 tool_result 引用的 ID
	keptToolResultRefs := collectToolResultIDs(messages[startIndex:])

	// 找出缺失的 tool_use（tool_result 引用了但 tool_use 不在范围内）
	missingToolUseIDs := make(map[string]bool)
	for id := range keptToolResultRefs {
		if !keptToolUseIDs[id] {
			missingToolUseIDs[id] = true
		}
	}

	if len(missingToolUseIDs) == 0 {
		return startIndex
	}

	// 向前搜索包含缺失 tool_use 的消息，扩展起始索引
	minIndex := startIndex
	for i := startIndex - 1; i >= 0; i-- {
		for _, block := range messages[i].Content {
			if block.Type == ContentTypeToolUse && missingToolUseIDs[block.ToolUseID] {
				if i < minIndex {
					minIndex = i
				}
				delete(missingToolUseIDs, block.ToolUseID)
			}
		}
		if len(missingToolUseIDs) == 0 {
			break
		}
	}
	return minIndex
}

// fixThinkingBlockSeparation 确保不分离共享 message.id 的 thinking 块.
//
// 流式传输场景:
//
//	Index N:   assistant, message.id: X, content: [thinking]
//	Index N+1: assistant, message.id: X, content: [tool_use: ID_1]
//	Index N+2: assistant, message.id: X, content: [tool_use: ID_2]
//
// 如果 startIndex = N+1，normalize 合并时排除了 thinking 块.
func fixThinkingBlockSeparation(messages []Message, startIndex int) int {
	// 收集保留范围内 assistant 消息的 message.id
	keptMessageIDs := collectMessageIDs(messages[startIndex:])

	// 向前搜索共享同一 message.id 的消息，扩展起始索引
	minIndex := startIndex
	for i := startIndex - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == RoleAssistant && msg.MessageID != "" && keptMessageIDs[msg.MessageID] {
			if i < minIndex {
				minIndex = i
			}
		}
	}
	return minIndex
}

// ensureFirstMessageIsUser 确保消息列表第一条是 user 角色（API 要求）.
func ensureFirstMessageIsUser(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	if messages[0].Role == RoleUser {
		return messages
	}
	// 前插一个标记消息
	marker := Message{
		Role: RoleUser,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: "[PTL Retry Marker - context truncated]",
		}},
	}
	return append([]Message{marker}, messages...)
}

// compactableToolResult 判断工具结果是否可压缩.
// whitelist 为 nil 或空时，默认所有工具均可压缩；否则仅压缩白名单内工具.
// caseInsensitive 为 true 时，忽略工具名大小写匹配.
func compactableToolResult(block ContentBlock, whitelist map[string]bool, caseInsensitive bool) bool {
	if block.Type != ContentTypeToolResult {
		return false
	}
	// 未指定白名单时，默认全部可压缩
	if whitelist == nil || len(whitelist) == 0 {
		return true
	}
	if caseInsensitive {
		for name := range whitelist {
			if strings.EqualFold(name, block.ToolName) {
				return whitelist[name]
			}
		}
		return false
	}
	return whitelist[block.ToolName]
}
