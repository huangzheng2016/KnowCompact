package compact

import "strings"

// CompactionModelStrategy 压缩模型选择策略.
type CompactionModelStrategy string

const (
	// StrategySameModel 复用主 agent 模型 —— 摘要质量最高，cache 可共享.
	StrategySameModel CompactionModelStrategy = "same_model"

	// StrategyLightModel 使用轻量模型 —— 成本低速度快，推荐用于日常.
	StrategyLightModel CompactionModelStrategy = "light_model"

	// StrategyCustomModel 自定义模型 —— 完全由调用方指定.
	StrategyCustomModel CompactionModelStrategy = "custom"
)

// LightModelFor 根据主模型推荐轻量压缩模型.
func LightModelFor(mainModel string) string {
	lower := strings.ToLower(mainModel)
	switch {
	case strings.Contains(lower, "opus"):
		return "claude-sonnet-4-6"
	case strings.Contains(lower, "sonnet"):
		return "claude-haiku-4-5"
	case strings.Contains(lower, "haiku"):
		return "claude-haiku-4-5"
	case strings.Contains(lower, "gpt-4o-mini"):
		return "gpt-4o-mini"
	case strings.Contains(lower, "gpt-4o"):
		return "gpt-4o-mini"
	case strings.Contains(lower, "gpt-4"):
		return "gpt-4o-mini"
	case strings.Contains(lower, "deepseek"):
		return mainModel
	default:
		return mainModel
	}
}

// CompactionModelConfig 压缩模型配置.
type CompactionModelConfig struct {
	Strategy    CompactionModelStrategy // 策略
	MainModel   string                  // 主 agent 模型名
	CustomModel string                  // 自定义模型名（仅 StrategyCustomModel 时生效）
}

// ResolveModel 根据策略解析实际使用的压缩模型.
func (c *CompactionModelConfig) ResolveModel() string {
	switch c.Strategy {
	case StrategyLightModel:
		return LightModelFor(c.MainModel)
	case StrategyCustomModel:
		return c.CustomModel
	default: // StrategySameModel
		return c.MainModel
	}
}
