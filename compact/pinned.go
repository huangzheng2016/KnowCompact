package compact

import "strings"

// PinnedMessageFilter 判断一条消息是否在降级截断中必须保留。
//
// 参数:
//   - index: 该消息在原始列表中的位置
//   - total: 原始列表的总长度
//   - msg:   被判断的消息本体
//
// 返回 true 表示"必须保留"。Pinned 消息会被附加到截断后保留消息的前面（按原始顺序）。
type PinnedMessageFilter func(index, total int, msg Message) bool

// DefaultPinnedMessageFilter 默认 pinned 判定规则:
//  1. 首条 user 消息（通常承载原始任务指令）始终保留
//  2. Extra["pinned"] == "true" 的任何消息保留
//  3. Extra["role"] == "system_prompt" 的消息保留（兼容部分接入方约定）
func DefaultPinnedMessageFilter(index, total int, msg Message) bool {
	if index == 0 && msg.Role == RoleUser {
		return true
	}
	if msg.Extra != nil {
		if strings.EqualFold(msg.Extra["pinned"], "true") {
			return true
		}
		if msg.Extra["role"] == "system_prompt" {
			return true
		}
	}
	return false
}

// collectPinnedMessages 从原始消息中提取 pinned 消息，按原始顺序返回。
//
// 当截断后保留范围已经包含某条 pinned 消息时，不会重复添加。
//
// kept 是截断后保留下来的消息切片（messages[startIndex:]），
// 通过指针位置判断已包含的范围，避免重复注入。
func collectPinnedMessages(
	messages []Message,
	startIndex int,
	filter PinnedMessageFilter,
) []Message {
	if filter == nil {
		filter = DefaultPinnedMessageFilter
	}
	total := len(messages)
	var pinned []Message
	for i := 0; i < startIndex && i < total; i++ {
		if filter(i, total, messages[i]) {
			pinned = append(pinned, messages[i])
		}
	}
	return pinned
}
