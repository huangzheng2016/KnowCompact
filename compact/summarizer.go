package compact

import (
	"context"
	"fmt"
)

// LLMClient 通用 LLM 客户端接口（兼容 eino ChatModel 和直接 API 调用）.
type LLMClient interface {
	// Chat 发送消息并获取回复文本.
	Chat(ctx context.Context, prompt string, messages []Message) (string, error)
}

// LLMSummarizer 基于通用 LLM 客户端的摘要生成器.
type LLMSummarizer struct {
	client        LLMClient
	modelName     string
	compactPrompt string
}

// NewLLMSummarizer 创建 LLM 摘要生成器.
func NewLLMSummarizer(client LLMClient, modelName string) *LLMSummarizer {
	return &LLMSummarizer{
		client:        client,
		modelName:     modelName,
		compactPrompt: BaseCompactPrompt,
	}
}

// WithPrompt 自定义压缩提示词.
func (s *LLMSummarizer) WithPrompt(prompt string) *LLMSummarizer {
	s.compactPrompt = prompt
	return s
}

// ModelName 返回使用的模型名.
func (s *LLMSummarizer) ModelName() string { return s.modelName }

// GenerateSummary 实现 Summarizer 接口.
func (s *LLMSummarizer) GenerateSummary(ctx context.Context, messages []Message) (string, error) {
	return s.client.Chat(ctx, s.compactPrompt, messages)
}

// NewSummarizerWithClient 用用户传入的 LLMClient 创建摘要器.
func NewSummarizerWithClient(client LLMClient, modelName string) *LLMSummarizer {
	return NewLLMSummarizer(client, modelName)
}

// NewSummarizer 根据策略创建摘要生成器.
func NewSummarizer(cfg CompactionModelConfig, client LLMClient) *LLMSummarizer {
	model := cfg.ResolveModel()
	return NewLLMSummarizer(client, model)
}

// EstimateCompactionCost 估算一次压缩的 token 消耗.
func EstimateCompactionCost(inputTokens int) (inputEstimate int, outputEstimate int) {
	outputEstimate = 20_000
	if inputTokens > 0 {
		inputEstimate = inputTokens
	} else {
		inputEstimate = 100_000
	}
	return
}

// FormatCompactionCost 格式化成本信息.
func FormatCompactionCost(modelName string, inputTokens, outputTokens int) string {
	return fmt.Sprintf(
		"compaction model=%s input=~%d tokens output=~%d tokens",
		modelName, inputTokens, outputTokens,
	)
}
