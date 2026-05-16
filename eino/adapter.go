// Package eino 提供 KnowCompact 与 eino 框架的集成适配。
//
// 包含：
//   - Message 双向转换（eino schema.Message ↔ compact.Message）
//   - ChatModel → Summarizer 适配
//   - 一键创建完整压缩器（复用 Agent ChatModel）
//   - eino ADK Middleware 自动压缩
//
// 用法：
//
//	import "github.com/huangzheng2016/KnowCompact/eino"
//
//	compactor := eino.NewDefaultCompactorWithEinoModel(config, chatModel, "kimi-k2.6")
//	mw := eino.NewCompactMiddleware(compactor, 0) // 0 = 使用默认值 256K
package eino

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/huangzheng2016/KnowCompact/compact"
)

// ============================================================
// KnowCompact Message ↔ eino schema.Message 双向转换
// ============================================================

// FromSchemaMessages 将 eino schema.Message 转换为 compact.Message.
func FromSchemaMessages(msgs []*schema.Message) []compact.Message {
	result := make([]compact.Message, 0, len(msgs))
	for _, msg := range msgs {
		m := compact.Message{Role: compact.Role(msg.Role)}

		if msg.Extra != nil {
			if id, ok := msg.Extra["message_id"].(string); ok {
				m.MessageID = id
			}
		}

		if msg.Content != "" {
			m.Content = append(m.Content, compact.ContentBlock{
				Type: compact.ContentTypeText,
				Text: msg.Content,
			})
		}

		for _, tc := range msg.ToolCalls {
			m.Content = append(m.Content, compact.ContentBlock{
				Type:      compact.ContentTypeToolUse,
				ToolName:  tc.Function.Name,
				ToolUseID: tc.ID,
				ToolInput: tc.Function.Arguments,
			})
		}

		if msg.ReasoningContent != "" {
			m.Content = append(m.Content, compact.ContentBlock{
				Type:     compact.ContentTypeThinking,
				Thinking: msg.ReasoningContent,
			})
		}

		if msg.Role == schema.Tool && msg.ToolCallID != "" {
			m.Content = append(m.Content, compact.ContentBlock{
				Type:       compact.ContentTypeToolResult,
				ToolName:   msg.ToolName,
				ToolUseID:  msg.ToolCallID,
				ToolOutput: msg.Content,
			})
		}

		if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
			m.Usage = &compact.TokenUsage{
				InputTokens:  msg.ResponseMeta.Usage.PromptTokens,
				OutputTokens: msg.ResponseMeta.Usage.CompletionTokens,
			}
		}

		result = append(result, m)
	}
	return result
}

// ToSchemaMessages 将 compact.Message 转换为 eino schema.Message.
func ToSchemaMessages(msgs []compact.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(msgs))
	for _, msg := range msgs {
		m := &schema.Message{Role: schema.RoleType(msg.Role)}

		if msg.MessageID != "" {
			if m.Extra == nil {
				m.Extra = make(map[string]any)
			}
			m.Extra["message_id"] = msg.MessageID
		}

		for _, block := range msg.Content {
			switch block.Type {
			case compact.ContentTypeText:
				if m.Content != "" {
					m.Content += "\n"
				}
				m.Content += block.Text

			case compact.ContentTypeToolUse:
				m.ToolCalls = append(m.ToolCalls, schema.ToolCall{
					ID:   block.ToolUseID,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      block.ToolName,
						Arguments: block.ToolInput,
					},
				})

			case compact.ContentTypeToolResult:
				m.Role = schema.Tool
				m.ToolCallID = block.ToolUseID
				m.ToolName = block.ToolName
				m.Content = block.ToolOutput

			case compact.ContentTypeThinking:
				m.ReasoningContent = block.Thinking
			}
		}

		if msg.Usage != nil {
			m.ResponseMeta = &schema.ResponseMeta{
				Usage: &schema.TokenUsage{
					PromptTokens:     msg.Usage.InputTokens,
					CompletionTokens: msg.Usage.OutputTokens,
				},
			}
		}

		result = append(result, m)
	}
	return result
}

// ============================================================
// eino ChatModel → compact.LLMClient 适配器
// ============================================================

type chatModelAdapter struct {
	model model.BaseChatModel
}

func (a *chatModelAdapter) Chat(ctx context.Context, prompt string, messages []compact.Message) (string, error) {
	schemaMsgs := ToSchemaMessages(messages)
	msgs := append([]*schema.Message{
		{Role: schema.System, Content: prompt},
	}, schemaMsgs...)

	resp, err := a.model.Generate(ctx, msgs)
	if err != nil {
		return "", fmt.Errorf("eino model generate failed: %w", err)
	}
	if resp == nil {
		return "", fmt.Errorf("eino model returned nil response")
	}
	return resp.Content, nil
}

// NewSummarizerFromEinoModel 用 eino 的 ChatModel 创建 KnowCompact 摘要器.
func NewSummarizerFromEinoModel(m model.BaseChatModel, modelName string) compact.Summarizer {
	return compact.NewSummarizerWithClient(&chatModelAdapter{model: m}, modelName)
}

// NewDefaultCompactorWithEinoModel 创建默认压缩器（启用 Layer 1/2/3，复用 eino ChatModel）。
//
// 这是 eino 用户的推荐用法，一行代码即可启用完整的上下文压缩：
//
//	config := compact.DefaultCompactionConfig()
//	compactor := eino.NewDefaultCompactorWithEinoModel(config, chatModel, "kimi-k2.6")
//
// 如果需要 Session Memory（Layer 4），手动传入 memStore：
//
//	store := &compact.InMemorySessionStore{}
//	compactor := compact.NewDefaultCompactor(config, summarizer, store)
func NewDefaultCompactorWithEinoModel(config compact.CompactionConfig, chatModel model.BaseChatModel, modelName string) *compact.DefaultCompactor {
	summarizer := NewSummarizerFromEinoModel(chatModel, modelName)
	return compact.NewDefaultCompactor(config, summarizer, nil)
}

// ============================================================
// eino ADK Middleware: CompactMiddleware
// ============================================================

// CompactMiddleware 实现 eino ChatModelAgentMiddleware 接口，
// 通过 BeforeModelRewriteState 在每轮模型调用前自动执行上下文压缩。
type CompactMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	compactor      *compact.DefaultCompactor
	modelMaxTokens int
}

// NewCompactMiddleware 创建 KnowCompact 上下文压缩中间件.
//
// modelMaxTokens: 模型上下文窗口大小（默认 200_000）
func NewCompactMiddleware(compactor *compact.DefaultCompactor, modelMaxTokens int) *CompactMiddleware {
	if modelMaxTokens <= 0 {
		modelMaxTokens = 256_000
	}
	return &CompactMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		compactor:                    compactor,
		modelMaxTokens:               modelMaxTokens,
	}
}

// BeforeModelRewriteState 在每次模型调用前执行上下文压缩.
func (m *CompactMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	mc *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if m.compactor == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	compactMsgs := FromSchemaMessages(state.Messages)
	result, err := m.compactor.Compact(ctx, compactMsgs, m.modelMaxTokens)
	if err != nil {
		fmt.Printf("  [KnowCompact] 压缩失败: %v\n", err)
		return ctx, state, nil
	}

	// 响应式检查：压缩后仍然超过危险阈值？前置降级
	dangerThreshold := int(float64(m.modelMaxTokens) * 0.95)
	if result.TokensAfter > dangerThreshold {
		fmt.Printf("  [KnowCompact] 压缩后仍超过危险阈值 (%d > %d)，触发响应式截断\n",
			result.TokensAfter, dangerThreshold)
		reactiveResult, rerr := m.compactor.ReactiveCompact(ctx, result.Messages, m.modelMaxTokens)
		if rerr == nil && reactiveResult != nil {
			result = reactiveResult
			fmt.Printf("  [KnowCompact] 响应式截断: %d → %d tokens (trigger=%s)\n",
				result.TokensBefore, result.TokensAfter, result.Trigger)
		}
	}

	if result.WasCompacted {
		fmt.Printf("  [KnowCompact] %d → %d tokens (trigger=%s, messages=%d→%d)\n",
			result.TokensBefore, result.TokensAfter, result.Trigger,
			len(state.Messages), len(result.Messages))
	}

	state.Messages = ToSchemaMessages(result.Messages)
	return ctx, state, nil
}
