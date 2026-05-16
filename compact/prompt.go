package compact

// noToolsPreamble 强力禁止工具调用的前导词.
//
// 为什么需要这么强力：Sonnet 4.6+ 自适应思考模型有时会忽略较弱的尾部指令
// 并尝试调用工具。在 maxTurns: 1 的情况下，被拒绝的工具调用意味着没有文本输出
// → 回退到流式备用路径。把这个放在最前面并明确说明拒绝后果，可以防止浪费轮次.
const noToolsPreamble = `关键提示：仅输出纯文本，不要调用任何工具。

- 不要使用 Read、Bash、Grep、Glob、Edit、Write 或任何其他工具。
- 上述对话已包含你所需的全部上下文。
- 工具调用将被拒绝，并浪费你唯一的一轮输出机会 —— 你将无法完成任务。
- 你的完整回复必须是纯文本：先输出一个 <分析> 块，再输出一个 <摘要> 块。
`

// noToolsTrailer 尾部禁止工具调用提醒.
const noToolsTrailer = `提醒：不要调用任何工具。仅回复 <分析> 和 <摘要> 块。`

// BaseCompactPrompt 完整压缩提示词模板.
const BaseCompactPrompt = noToolsPreamble + `
你正在总结一个 AI Agent 与用户之间的对话。
你的任务是生成一份详细、结构化的摘要，使对话能够从断点处继续，且不丢失上下文。

首先，输出一个 <分析> 块，按时间顺序逐步梳理对话：
1. 逐条查看每条消息
2. 识别用户意图、技术决策和代码模式
3. 特别关注用户反馈 —— 当用户让你做不同的事情时
4. 仔细检查技术准确性和完整性

然后，输出一个 <摘要> 块，包含以下章节：

1. 主要请求和意图：
   [详细描述所有用户请求和意图]

2. 关键技术概念：
   - [概念 1]
   - [概念 2]

3. 文件和代码段：
   - [文件名]
     - 该文件为什么重要
     - 关键代码片段或所做的修改
   - [文件名]
     - 代码片段

4. 错误与修复：
   - [错误描述]：
     - 修复方式
     - 用户对修复的反馈

5. 问题解决：
   [描述已解决的问题和推理过程]

6. 所有用户消息：
   - [列出每条非工具结果的用户消息]

7. 待办任务：
   - [任务 1]
   - [任务 2]

8. 当前工作：
   [精确描述当前正在进行的工作，包含文件名和代码片段]

9. 下一步（可选）：
   [接下来应该做什么，引用最近对话中的原话]

` + noToolsTrailer

// PartialCompactPrompt 部分压缩提示词（from 模式）.
// 只摘要最近的消息，旧消息保留.
const PartialCompactPrompt = noToolsPreamble + `
你正在总结一段较长对话的最近部分。
仅关注最新的消息 —— 对话的早期部分已被单独保留。

输出一个 <分析> 块，后跟一个 <摘要> 块。
<摘要> 必须涵盖完整压缩格式中的第 1-9 节，
但仅限于最近的消息。

不要总结整个对话 —— 仅总结最近的部分。
` + noToolsTrailer

// PartialCompactUpToPrompt 部分压缩提示词（up_to 模式）.
// 摘要旧消息，保留新消息。摘要放在开头作为后续消息的前导.
const PartialCompactUpToPrompt = noToolsPreamble + `
你正在总结一段对话的早期部分。
摘要点之后的最近消息将被逐字保留。

你的摘要将放在最近消息之前，因此请包含：

除了标准格式中的第 1-9 节外，再添加：

10. 继续工作的上下文：
    [提供有助于理解后续最近消息的上下文。
     解释发生了什么、做了哪些决策、以及摘要部分结束时工作处于什么状态。
     这段上下文对于理解接下来的逐字消息至关重要。]

输出一个 <分析> 块，后跟一个 <摘要> 块。
` + noToolsTrailer

// FormatCompactSummary 后处理压缩摘要.
//
// 操作:
//  1. 删除 <analysis>/<分析> 块（思考草稿，不再有价值）
//  2. 提取 <summary>/<摘要> 内容，替换为可读格式
//  3. 清理多余空行
//
// 同时支持中英文标签（向后兼容）.
func FormatCompactSummary(summary string) string {
	result := summary
	// 删除 <analysis> / <分析> 块（先英文后中文）
	result = replaceXMLBlock(result, "analysis", "")
	result = replaceXMLBlock(result, "分析", "")
	// 提取 <summary> / <摘要> 内容并格式化（先英文后中文）
	summaryContent := extractXMLBlock(result, "summary")
	if summaryContent == "" {
		summaryContent = extractXMLBlock(result, "摘要")
	}
	if summaryContent != "" {
		result = replaceXMLBlock(result, "summary", "Summary:\n"+summaryContent)
		result = replaceXMLBlock(result, "摘要", "Summary:\n"+summaryContent)
	}
	// 清理多余空行
	result = compactEmptyLines(result)
	return result
}

// extractXMLBlock 提取 XML 块内容（不含标签）.
func extractXMLBlock(text, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"

	start := indexOf(text, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)

	end := indexOf(text, closeTag)
	if end < 0 || end <= start {
		return ""
	}

	return text[start:end]
}

// replaceXMLBlock 替换 XML 块内容.
func replaceXMLBlock(text, tag, replacement string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"

	start := indexOf(text, openTag)
	if start < 0 {
		return text
	}

	end := indexOf(text, closeTag)
	if end < 0 {
		return text
	}
	end += len(closeTag)

	if replacement == "" {
		// 删除整个块（保留后面的内容）
		return text[:start] + text[end:]
	}
	return text[:start] + replacement + text[end:]
}

// compactEmptyLines 将 2 个以上连续空行压缩为 2 个.
func compactEmptyLines(text string) string {
	result := make([]byte, 0, len(text))
	newlineCount := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			newlineCount++
			if newlineCount <= 2 {
				result = append(result, '\n')
			}
		} else {
			newlineCount = 0
			result = append(result, text[i])
		}
	}
	return string(result)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// GetCompactUserSummaryMessage 生成压缩后的摘要用户消息.
func GetCompactUserSummaryMessage(summary string, suppressFollowUp bool, recentPreserved bool) Message {
	formattedSummary := FormatCompactSummary(summary)

	msg := `此对话正从之前因上下文不足而中断的会话中继续。
以下摘要涵盖了对话的早期部分。

` + formattedSummary

	if recentPreserved {
		msg += `\n\n最近的消息已逐字保留。`
	}

	if suppressFollowUp {
		msg += `\n\n从断点处继续对话，不要再向用户提问。
直接继续 —— 不要提及摘要内容，
不要复述之前发生了什么，
不要用「我将继续」之类的话开头。
像中断从未发生过一样，接着处理最后一项任务。`
	}

	return Message{
		Role: RoleUser,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: msg,
		}},
	}
}

// CreateCompactBoundary 创建压缩边界标记消息.
func CreateCompactBoundary(tokensBefore, tokensAfter int, trigger string) Message {
	text := "[压缩边界]"
	if tokensBefore > 0 {
		text += "\n压缩前 tokens: " + itoa(tokensBefore)
	}
	if tokensAfter > 0 {
		text += "\n压缩后 tokens: " + itoa(tokensAfter)
	}
	if trigger != "" {
		text += "\n触发器: " + trigger
	}

	return Message{
		Role: RoleSystem,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: text,
		}},
	}
}

// GetNextStepHint 生成下一步提示（用于压缩后的引导）.
func GetNextStepHint(lastAssistantMsg string) string {
	if lastAssistantMsg == "" {
		return ""
	}
	return "The agent was last working on: " + truncateContent(lastAssistantMsg, 500)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return sign + string(buf[i:])
}
