package compact

import "strings"

// ModelPreset 模型预设 —— 预定义的窗口大小和推荐配置.
//
// 使用方式:
//
//	preset := PresetClaudeOpus4()        // 推荐：按模型选择
//	preset := PresetForModel("sonnet")   // 自动匹配
type ModelPreset struct {
	Name             string // 模型显示名
	ContextWindow    int    // 总上下文窗口 (tokens)
	MaxOutputTokens  int    // 模型最大输出 (tokens)
	ReservedForSummary int  // 留给摘要输出的预算 (默认 20K)
	BufferTokens     int    // 自动压缩缓冲 (默认 13K)
}

// AutoCompactThreshold 该预设下的自动压缩触发阈值.
//
//	阈值 = ContextWindow - max(ReservedForSummary, MaxOutputTokens) - BufferTokens
func (p ModelPreset) AutoCompactThreshold() int {
	reserved := p.ReservedForSummary
	if p.MaxOutputTokens > 0 && p.MaxOutputTokens < reserved {
		reserved = p.MaxOutputTokens
	}
	return p.ContextWindow - reserved - p.BufferTokens
}

// EffectiveWindow 该预设下的有效上下文窗口.
func (p ModelPreset) EffectiveWindow() int {
	reserved := p.ReservedForSummary
	if p.MaxOutputTokens > 0 && p.MaxOutputTokens < reserved {
		reserved = p.MaxOutputTokens
	}
	return p.ContextWindow - reserved
}

// ============================================================
// 内置预设
// ============================================================

// Anthropic Claude 系列
func PresetClaudeOpus4() ModelPreset {
	return ModelPreset{
		Name:              "Claude Opus 4",
		ContextWindow:     200_000,
		MaxOutputTokens:   32_000,
		ReservedForSummary: 20_000,
		BufferTokens:      13_000,
	}
}

func PresetClaudeSonnet4() ModelPreset {
	return ModelPreset{
		Name:              "Claude Sonnet 4",
		ContextWindow:     200_000,
		MaxOutputTokens:   64_000,
		ReservedForSummary: 20_000,
		BufferTokens:      13_000,
	}
}

func PresetClaudeSonnet4_5() ModelPreset {
	return ModelPreset{
		Name:              "Claude Sonnet 4.5",
		ContextWindow:     200_000,
		MaxOutputTokens:   64_000,
		ReservedForSummary: 20_000,
		BufferTokens:      13_000,
	}
}

func PresetClaudeHaiku4_5() ModelPreset {
	return ModelPreset{
		Name:              "Claude Haiku 4.5",
		ContextWindow:     200_000,
		MaxOutputTokens:   64_000,
		ReservedForSummary: 20_000,
		BufferTokens:      13_000,
	}
}

// OpenAI 系列
func PresetGPT4o() ModelPreset {
	return ModelPreset{
		Name:              "GPT-4o",
		ContextWindow:     128_000,
		MaxOutputTokens:   16_384,
		ReservedForSummary: 16_000,
		BufferTokens:      10_000,
	}
}

func PresetGPT4Turbo() ModelPreset {
	return ModelPreset{
		Name:              "GPT-4 Turbo",
		ContextWindow:     128_000,
		MaxOutputTokens:   4_096,
		ReservedForSummary: 4_000,
		BufferTokens:      10_000,
	}
}

func PresetGPT4oMini() ModelPreset {
	return ModelPreset{
		Name:              "GPT-4o Mini",
		ContextWindow:     128_000,
		MaxOutputTokens:   16_384,
		ReservedForSummary: 16_000,
		BufferTokens:      10_000,
	}
}

// DeepSeek 系列
func PresetDeepSeekV3() ModelPreset {
	return ModelPreset{
		Name:              "DeepSeek V3",
		ContextWindow:     128_000,
		MaxOutputTokens:   8_192,
		ReservedForSummary: 8_000,
		BufferTokens:      10_000,
	}
}

// Qwen 系列
func PresetQwenMax() ModelPreset {
	return ModelPreset{
		Name:              "Qwen Max",
		ContextWindow:     128_000,
		MaxOutputTokens:   8_192,
		ReservedForSummary: 8_000,
		BufferTokens:      10_000,
	}
}

// ============================================================
// 自动匹配
// ============================================================

// PresetForModel 根据模型名自动选择预设.
// 匹配规则：模型名转小写后按关键词匹配.
func PresetForModel(modelName string) ModelPreset {
	lower := strings.ToLower(modelName)

	switch {
	case strings.Contains(lower, "opus"):
		return PresetClaudeOpus4()
	case strings.Contains(lower, "sonnet"):
		return PresetClaudeSonnet4()
	case strings.Contains(lower, "haiku"):
		return PresetClaudeHaiku4_5()
	case strings.Contains(lower, "claude"):
		return PresetClaudeSonnet4()
	case strings.Contains(lower, "gpt-4o-mini"):
		return PresetGPT4oMini()
	case strings.Contains(lower, "gpt-4o"):
		return PresetGPT4o()
	case strings.Contains(lower, "gpt-4"):
		return PresetGPT4Turbo()
	case strings.Contains(lower, "gpt-3.5"):
		return ModelPreset{
			Name: "GPT-3.5", ContextWindow: 16_000,
			MaxOutputTokens: 4_096, ReservedForSummary: 4_000, BufferTokens: 3_000,
		}
	case strings.Contains(lower, "deepseek"):
		return PresetDeepSeekV3()
	case strings.Contains(lower, "qwen"):
		return PresetQwenMax()

	default:
		return ModelPreset{
			Name: "Unknown (128K)", ContextWindow: 128_000,
			MaxOutputTokens: 8_192, ReservedForSummary: 8_000, BufferTokens: 10_000,
		}
	}
}

// ============================================================
// 自定义预设（灵活微调）
// ============================================================

// CustomPreset 创建自定义预设.
//
// 只需填关键参数，其他使用默认值:
//
//	preset := compact.CustomPreset(100_000, 8_000)  // 100K 窗口, 8K 输入
func CustomPreset(contextWindow, reservedForSummary int) ModelPreset {
	return ModelPreset{
		Name:               "Custom",
		ContextWindow:      contextWindow,
		MaxOutputTokens:    reservedForSummary,
		ReservedForSummary: reservedForSummary,
		BufferTokens:       10_000,
	}
}

// WithBuffer 自定义缓冲大小.
func (p ModelPreset) WithBuffer(tokens int) ModelPreset {
	p.BufferTokens = tokens
	return p
}

// WithName 设置显示名.
func (p ModelPreset) WithName(name string) ModelPreset {
	p.Name = name
	return p
}


