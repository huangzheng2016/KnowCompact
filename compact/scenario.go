package compact

// Scenario 压缩场景类型 —— 不同应用场景对压缩策略的偏好不同.
//
// 通过 PresetForScenario 一键拿到针对该场景调优过的 CompactionConfig，
// 避免每个用户都重新摸索"缓冲该设多大、文件该重注入多少"等参数。
type Scenario string

const (
	// ScenarioCodingAgent 编码代理：长对话、大量 tool_result、文件读取频繁.
	//
	// 特点:
	//   - RecentToolsKeep 较大（保留更多最近工具结果，便于 LLM 引用）
	//   - FileReinjectBudget 充裕（鼓励重注入文件给后续轮次使用）
	//   - 自动压缩缓冲略大（避免一次压缩后立即又超阈值）
	ScenarioCodingAgent Scenario = "coding_agent"

	// ScenarioCustomerSupport 客服 / 长会话问答：以纯文本对话为主、少工具调用.
	//
	// 特点:
	//   - 微压缩白名单缩窄（少量工具，无需激进压缩）
	//   - SMMinTokens / SMMaxTokens 较小（早摘要、早释放）
	//   - 文件重注入预算很小（很少读文件）
	ScenarioCustomerSupport Scenario = "customer_support"

	// ScenarioResearchAnalysis 研究分析：多检索 / 多 RAG 调用、上下文跨多份文档.
	//
	// 特点:
	//   - RecentToolsKeep 较大（保留最近检索结果链）
	//   - SMMaxTokens 较大（允许 session memory 容纳更多源信息）
	//   - 摘要预算较大（让 LLM 生成更详尽摘要）
	ScenarioResearchAnalysis Scenario = "research_analysis"

	// ScenarioLightweight 轻量 / 嵌入式：尽可能少触发 LLM 压缩，省 token.
	//
	// 特点:
	//   - 关闭 Session Memory（SMMinTokens 设大到等同于禁用）
	//   - 降级截断更激进（保留 25% 即可）
	//   - 微压缩开启但 RecentToolsKeep 调小
	ScenarioLightweight Scenario = "lightweight"
)

// PresetForScenario 根据场景返回调优过的默认配置.
//
// 用户可在返回值上继续链式调整：
//
//	cfg := compact.PresetForScenario(compact.ScenarioCodingAgent).
//	    WithRecentToolsKeep(5)
//
// 未识别场景时返回 DefaultCompactionConfig().
func PresetForScenario(s Scenario) CompactionConfig {
	switch s {
	case ScenarioCodingAgent:
		return DefaultCompactionConfig().
			WithRecentToolsKeep(5).
			WithAutoCompactBufferTokens(15_000).
			WithMaxOutputTokensForSummary(24_000).
			WithRecentFilesMax(8).
			WithFileReinjectBudget(80_000)
	case ScenarioCustomerSupport:
		return DefaultCompactionConfig().
			WithRecentToolsKeep(1).
			WithAutoCompactBufferTokens(8_000).
			WithMaxOutputTokensForSummary(12_000).
			WithSMMinTokens(5_000).
			WithSMMaxTokens(20_000).
			WithRecentFilesMax(0).
			WithFileReinjectBudget(0)
	case ScenarioResearchAnalysis:
		return DefaultCompactionConfig().
			WithRecentToolsKeep(4).
			WithAutoCompactBufferTokens(15_000).
			WithMaxOutputTokensForSummary(32_000).
			WithSMMinTokens(15_000).
			WithSMMaxTokens(60_000).
			WithRecentFilesMax(10).
			WithRecentFileMaxTokens(8_000).
			WithFileReinjectBudget(100_000)
	case ScenarioLightweight:
		// 用极大的 SMMinTokens 实质禁用 Session Memory 触发
		return DefaultCompactionConfig().
			WithRecentToolsKeep(1).
			WithAutoCompactBufferTokens(8_000).
			WithMaxOutputTokensForSummary(8_000).
			WithSMMinTokens(1_000_000_000).
			WithFallbackTruncateRatio(0.25).
			WithRecentFilesMax(0).
			WithFileReinjectBudget(0)
	default:
		return DefaultCompactionConfig()
	}
}
