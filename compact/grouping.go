package compact

import (
	"sort"
	"strings"
)

// groupMessagesByAPIRound 按 API 轮次对消息分组.
// 同一 assistant message.id 的流式块属于同一组.
func groupMessagesByAPIRound(messages []Message) [][]Message {
	var groups [][]Message
	var current []Message
	var lastAssistantID string

	for _, msg := range messages {
		if msg.Role == RoleAssistant &&
			msg.MessageID != "" &&
			msg.MessageID != lastAssistantID &&
			len(current) > 0 {
			groups = append(groups, current)
			current = []Message{msg}
		} else {
			current = append(current, msg)
		}
		if msg.Role == RoleAssistant {
			lastAssistantID = msg.MessageID
		}
	}

	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// groupMessagesByUserTurn 按用户轮次分组（每个 user 消息开始新的一组）.
func groupMessagesByUserTurn(messages []Message) [][]Message {
	var groups [][]Message
	var current []Message

	for _, msg := range messages {
		if msg.Role == RoleUser && len(current) > 0 {
			groups = append(groups, current)
			current = []Message{msg}
		} else {
			current = append(current, msg)
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// findCompactBoundary 向前搜索最近的压缩边界消息索引.
// 返回 -1 表示未找到.
func findCompactBoundary(messages []Message, fromIndex int) int {
	for i := fromIndex; i >= 0; i-- {
		if isCompactBoundary(messages[i]) {
			return i
		}
	}
	return -1
}

// isCompactBoundary 判断是否为压缩边界标记消息.
//
// 匹配规则:
//   - CompactBoundaryMarker（当前实现写入的固定英文标记）
//   - "compaction boundary"（兼容 Claude Code 原始格式）
//   - "[压缩边界]"（兼容历史中文标记，迁移过渡用）
func isCompactBoundary(msg Message) bool {
	if msg.Role != RoleSystem {
		return false
	}
	for _, b := range msg.Content {
		if b.Type != ContentTypeText || len(b.Text) == 0 {
			continue
		}
		if strings.Contains(b.Text, CompactBoundaryMarker) ||
			strings.Contains(b.Text, "compaction boundary") ||
			strings.Contains(b.Text, "[压缩边界]") {
			return true
		}
	}
	return false
}

// TopologicalSortMessages 按拓扑顺序排列消息（确保 tool_use 在 tool_result 之前）.
func TopologicalSortMessages(messages []Message) []Message {
	if len(messages) <= 1 {
		return messages
	}

	toolUsePos := make(map[string]int)
	for i, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolUse && block.ToolUseID != "" {
				toolUsePos[block.ToolUseID] = i
			}
		}
	}

	needsReorder := false
	for i, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolResult && block.ToolUseID != "" {
				if pos, ok := toolUsePos[block.ToolUseID]; ok && pos > i {
					needsReorder = true
					break
				}
			}
		}
		if needsReorder {
			break
		}
	}

	if !needsReorder {
		return messages
	}

	sorted := make([]Message, len(messages))
	copy(sorted, messages)
	sort.SliceStable(sorted, func(i, j int) bool {
		return messagePriority(sorted[i]) < messagePriority(sorted[j])
	})
	return sorted
}

func messagePriority(msg Message) int {
	for _, block := range msg.Content {
		if block.Type == ContentTypeToolUse {
			return 0
		}
	}
	if msg.Role == RoleUser {
		return 1
	}
	if msg.Role == RoleTool {
		return 2
	}
	return 1
}
