package compact

import "math"

// FileAttachment 文件附件信息.
type FileAttachment struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ReinjectBudget 文件重注入预算管理.
//
// 压缩后重新注入最近读取的文件，以保持上下文连续性。
// 预算控制:
//   - 最近 N 个文件（默认 5）
//   - 每个文件 ≤ maxTokens（默认 5K）
//   - 总预算 ≤ budget（默认 50K tokens）
type ReinjectBudget struct {
	maxFiles  int
	maxTokens int
	budget    int
}

// NewReinjectBudget 创建文件重注入预算管理器.
func NewReinjectBudget(maxFiles, maxTokens, budget int) *ReinjectBudget {
	return &ReinjectBudget{
		maxFiles:  maxFiles,
		maxTokens: maxTokens,
		budget:    budget,
	}
}

// DefaultReinjectBudget 默认重注入预算.
func DefaultReinjectBudget() *ReinjectBudget {
	return &ReinjectBudget{
		maxFiles:  5,
		maxTokens: 5_000,
		budget:    50_000,
	}
}

// SelectFiles 从文件列表中选择可重注入的文件.
//
// 策略:
//  1. 按最近使用排序（调用方保证顺序）
//  2. 逐个检查 token 预算
//  3. 在总预算内尽可能多地包含文件
func (r *ReinjectBudget) SelectFiles(files []FileAttachment) []FileAttachment {
	if len(files) == 0 {
		return nil
	}

	fileCount := int(math.Min(float64(len(files)), float64(r.maxFiles)))

	var selected []FileAttachment
	totalTokens := 0

	for i := 0; i < fileCount; i++ {
		file := files[i]
		fileTokens := RoughTokenEstimate(file.Content)

		if fileTokens > r.maxTokens {
			file.Content = truncateContent(file.Content, r.maxTokens)
			fileTokens = r.maxTokens
		}

		if totalTokens+fileTokens > r.budget {
			break
		}

		selected = append(selected, file)
		totalTokens += fileTokens
	}

	return selected
}
