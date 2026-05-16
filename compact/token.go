package compact

import (
	"encoding/json"
	"math"
)

const (
	// imageDocumentTokenEstimate 图片/文档固定 token 估算.
	imageDocumentTokenEstimate = 2000
	// conservativeMultiplier 保守填充系数 (4/3 ≈ 1.33).
	conservativeMultiplier = 4.0 / 3.0
	// charsPerToken 经验法则: 4 字符 ≈ 1 token.
	charsPerToken = 4.0
)

// RoughTokenEstimate 粗略 token 估算（字符数/4）.
func RoughTokenEstimate(text string) int {
	if len(text) == 0 {
		return 0
	}
	return int(math.Ceil(float64(len(text)) / charsPerToken))
}

// EstimateMessageTokens 估算消息列表的 token 总数（含保守填充）.
func EstimateMessageTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateSingleMessageTokens(msg)
	}
	return int(math.Ceil(float64(total) * conservativeMultiplier))
}

func estimateSingleMessageTokens(msg Message) int {
	total := 0
	for _, block := range msg.Content {
		total += estimateBlockTokens(block)
	}
	return total
}

func estimateBlockTokens(block ContentBlock) int {
	switch block.Type {
	case ContentTypeText:
		return RoughTokenEstimate(block.Text)
	case ContentTypeToolResult:
		return RoughTokenEstimate(block.ToolOutput)
	case ContentTypeImage:
		return imageDocumentTokenEstimate
	case ContentTypeThinking:
		return RoughTokenEstimate(block.Thinking)
	case ContentTypeToolUse:
		inputStr := block.ToolName
		if block.ToolInput != "" {
			inputStr += block.ToolInput
		} else {
			inputStr += "{}"
		}
		return RoughTokenEstimate(inputStr)
	default:
		raw, _ := json.Marshal(block)
		return RoughTokenEstimate(string(raw))
	}
}

// EstimateMessageTokensPrecise 优先使用 API 返回的精确 token 数，不可用时回退到估算.
func EstimateMessageTokensPrecise(messages []Message) int {
	// 从最后一条 assistant 消息的 usage 读取精确值
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleAssistant && messages[i].Usage != nil {
			return messages[i].Usage.InputTokens
		}
	}
	return EstimateMessageTokens(messages)
}

// GetContextWindow 根据模型名获取上下文窗口大小.
// 委托给 PresetForModel 以统一模型预设数据源.
func GetContextWindow(model string) int {
	return PresetForModel(model).ContextWindow
}

// GetEffectiveContextWindow 有效上下文窗口 = 总窗口 - 摘要输出预留.
func GetEffectiveContextWindow(model string, maxOutputForSummary int) int {
	return GetContextWindow(model) - maxOutputForSummary
}

// GetAutoCompactThreshold 自动压缩触发阈值.
func GetAutoCompactThreshold(model string, bufferTokens int, maxOutputForSummary int) int {
	return GetEffectiveContextWindow(model, maxOutputForSummary) - bufferTokens
}



// truncateContent 截断内容到指定 token 数.
func truncateContent(content string, maxTokens int) string {
	maxChars := maxTokens * 4 // 粗略估算
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "\n\n[Content truncated due to reinjection budget]"
}
