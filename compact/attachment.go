package compact

import (
	"fmt"
	"strings"
)

// ============================================================
// 附件重注入（Attachments Reinjection）
// ============================================================
//
// Claude Code 压缩后会重新注入关键上下文附件：
//   - 最近读取的文件（前 5 个，每个 ≤ 5K tokens，总预算 50K）
//   - Plan 文件（如果有活跃计划）
//   - MCP 指令增量
//   - 技能发现增量
//   - 代理列表增量
//
// 本实现提供简化版：支持最近文件和 Plan 文件注入。
// MCP/技能/代理为预留接口，后续可扩展。
//
// 注意：FileAttachment 类型定义在 budget.go 中，此处复用。

// AttachmentInjector 附件重注入器.
type AttachmentInjector struct {
	// 最近读取的文件附件（由调用方提供，或从消息历史中自动提取）
	RecentFiles []FileAttachment
	// Plan 文件内容（可选）
	PlanContent string
}

// NewAttachmentInjector 创建附件注入器.
func NewAttachmentInjector() *AttachmentInjector {
	return &AttachmentInjector{}
}

// WithRecentFiles 设置最近读取的文件.
func (a *AttachmentInjector) WithRecentFiles(files []FileAttachment) *AttachmentInjector {
	a.RecentFiles = files
	return a
}

// WithPlanContent 设置 Plan 文件内容.
func (a *AttachmentInjector) WithPlanContent(content string) *AttachmentInjector {
	a.PlanContent = content
	return a
}

// InjectAttachments 将附件注入压缩后的消息列表.
//
// 注入顺序（Claude Code 兼容）：
//   1. Plan 文件（如果有）
//   2. 最近读取的文件
//   3. 其他附件
func (a *AttachmentInjector) InjectAttachments(
	messages []Message,
	config CompactionConfig,
) []Message {
	if len(a.RecentFiles) == 0 && a.PlanContent == "" {
		return messages
	}

	var attachments []Message

	// 1. Plan 文件
	if a.PlanContent != "" {
		attachments = append(attachments, buildPlanMessage(a.PlanContent, config))
	}

	// 2. 最近读取的文件（按预算截断）
	budget := NewReinjectBudget(
		config.RecentFilesMax,
		config.RecentFileMaxTokens,
		config.FileReinjectBudget,
	)
	files := budget.SelectFiles(a.RecentFiles)
	for _, f := range files {
		attachments = append(attachments, buildFileMessage(f))
	}

	// 追加到 messages 后面
	return append(messages, attachments...)
}

// buildPlanMessage 组装 Plan 文件消息.
func buildPlanMessage(content string, config CompactionConfig) Message {
	// 截断 Plan 内容
	planTokens := RoughTokenEstimate(content)
	maxTokens := config.RecentFileMaxTokens
	if maxTokens <= 0 {
		maxTokens = 5_000
	}
	if planTokens > maxTokens {
		content = truncateContent(content, maxTokens)
	}

	return Message{
		Role: RoleSystem,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: fmt.Sprintf("[当前活跃计划]\n\n%s", content),
		}},
	}
}

// buildFileMessage 组装单个文件附件消息.
func buildFileMessage(f FileAttachment) Message {
	return Message{
		Role: RoleSystem,
		Content: []ContentBlock{{
			Type: ContentTypeText,
			Text: fmt.Sprintf("[最近读取的文件: %s]\n\n%s", f.Path, f.Content),
		}},
	}
}

// ============================================================
// 自动提取最近文件（简化实现）
// ============================================================

// ExtractRecentFilesFromMessages 从消息历史中自动提取最近读取的文件.
//
// 扫描配置中指定的"文件读取"工具（FileReadToolNames）的 tool_result，
// 按出现顺序收集文件内容。
//
// 路径来源优先级：
//   1. tool_use 的 ToolInput 中解析 file_path（如 {"file_path":"main.go"}）
//   2. tool_result 内容开头的文件路径标记（如 "// file: main.go"）
//   3. tool_use_id 作为回退标识
func ExtractRecentFilesFromMessages(messages []Message, config CompactionConfig) []FileAttachment {
	readToolNames := config.FileReadToolNames
	if len(readToolNames) == 0 {
		readToolNames = DefaultCompactionConfig().FileReadToolNames
	}
	readToolSet := make(map[string]bool, len(readToolNames))
	for _, name := range readToolNames {
		readToolSet[strings.ToLower(name)] = true
	}

	// tool_use_id -> 文件路径的映射
	toolUseToPath := make(map[string]string)
	// 按出现顺序收集（去重）
	var ordered []FileAttachment
	seen := make(map[string]bool)

	// 第一轮：收集所有文件读取工具的 tool_use（获取文件路径）
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == ContentTypeToolUse &&
				block.ToolUseID != "" &&
				readToolSet[strings.ToLower(block.ToolName)] {
				// 尝试从 ToolInput 解析 file_path
				path := parseFilePathFromToolInput(block.ToolInput)
				if path == "" {
					path = block.ToolUseID // 回退到 tool_use_id
				}
				toolUseToPath[block.ToolUseID] = path
			}
		}
	}

	// 第二轮：收集所有文件读取工具的 tool_result（获取文件内容）
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type != ContentTypeToolResult || block.ToolUseID == "" {
				continue
			}

			var path string
			if p, ok := toolUseToPath[block.ToolUseID]; ok {
				// 已知工具：使用解析出的路径
				path = p
			} else {
				// 未知工具：尝试启发式检测——tool_result 内容是否像文件内容
				if path = guessFilePathFromOutput(block.ToolOutput); path == "" {
					continue
				}
			}

			if !seen[path] {
				seen[path] = true
				ordered = append(ordered, FileAttachment{
					Path:    path,
					Content: block.ToolOutput,
				})
			}
		}
	}

	return ordered
}

// parseFilePathFromToolInput 从文件读取工具的 JSON 参数中解析 file_path.
// 支持格式：{"file_path":"..."} / {"path":"..."} / {"filepath":"..."}
func parseFilePathFromToolInput(input string) string {
	input = strings.TrimSpace(input)
	for _, key := range []string{`"file_path"`, `"filepath"`, `"path"`, `"file"`} {
		idx := strings.Index(input, key)
		if idx < 0 {
			continue
		}
		// 找到 key 后的值
		after := input[idx+len(key):]
		after = strings.TrimLeft(after, ` :"`)
		// 找到结束引号
		endIdx := strings.Index(after, `"`)
		if endIdx > 0 {
			return after[:endIdx]
		}
	}
	return ""
}

// guessFilePathFromOutput 启发式检测 tool_result 内容是否包含文件路径标记.
//
// 支持的标记格式：
//   // file: path/to/file.go
//   --- file: path/to/file.go ---
//   [file: path/to/file.go]
//   # file: path/to/file.go
func guessFilePathFromOutput(output string) string {
	lines := strings.SplitN(output, "\n", 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"// file:", "--- file:", "[file:", "# file:"} {
			if strings.HasPrefix(line, prefix) {
				path := strings.TrimSpace(line[len(prefix):])
				// 去掉尾部标记如 "---"
				path = strings.TrimSuffix(path, "---")
				path = strings.TrimSuffix(path, "]")
				path = strings.TrimSpace(path)
				if path != "" {
					return path
				}
			}
		}
	}
	return ""
}
