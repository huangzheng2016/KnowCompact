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
//	store := compact.NewInMemorySessionMemoryStore()
//	summarizer := eino.NewSummarizerFromEinoModel(chatModel, "kimi-k2.6")
//	compactor := compact.NewDefaultCompactor(config, summarizer, store)
//
// 当 modelName 无法匹配任何内置预设时，会通过 logger 输出一次性警告，
// 提示调用方使用 NewCompactMiddleware(..., modelMaxTokens) 显式指定窗口大小，
// 否则真实窗口大小可能与默认 128K 不一致，导致阈值偏差。
func NewDefaultCompactorWithEinoModel(config compact.CompactionConfig, chatModel model.BaseChatModel, modelName string) *compact.DefaultCompactor {
	preset := compact.PresetForModel(modelName)
	if compact.IsUnknownPreset(preset) {
		log := compact.GetLogger(config)
		log.Warn("unknown model preset, falling back to 128K window — pass an explicit modelMaxTokens to NewCompactMiddleware",
			"model", modelName,
			"fallback_window", preset.ContextWindow)
	}
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
	logger         compact.Logger
}

// NewCompactMiddleware 创建 KnowCompact 上下文压缩中间件.
//
// modelMaxTokens: 模型上下文窗口大小（默认 256_000）
//
// 日志通过 compactor.Config().Logger 解析；如需自定义，请在 CompactionConfig
// 中通过 WithLogger / WithLogLevel 配置。
func NewCompactMiddleware(compactor *compact.DefaultCompactor, modelMaxTokens int) *CompactMiddleware {
	if modelMaxTokens <= 0 {
		modelMaxTokens = 256_000
	}
	var log compact.Logger
	if compactor != nil {
		log = compact.GetLogger(compactor.Config())
	} else {
		log = compact.NewDefaultLogger()
	}
	return &CompactMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		compactor:                    compactor,
		modelMaxTokens:               modelMaxTokens,
		logger:                       log,
	}
}

// NewEinoMiddleware 是 NewCompactMiddleware 的一行包装，便于快速接入.
//
// 这是 KnowCompact 与 eino 集成的"最短路径"：
//
//	mw := eino.NewEinoMiddleware(chatModel, "claude-opus-4-7")
//	agent.Use(mw)
//
// 行为:
//  1. 通过 modelName 自动选择 ModelPreset（窗口大小、阈值）
//  2. 自动用同一个 chatModel 作为压缩摘要器（节省一次模型创建）
//  3. 内置 Layer 1/2/3 压缩；如需 Layer 4 Session Memory，
//     请改用 NewDefaultCompactorWithEinoModel + 显式传入 store.
//
// 如果想覆盖配置（缓冲大小、回调、自定义 logger 等），用
// NewEinoMiddlewareWithConfig 替代.
func NewEinoMiddleware(chatModel model.BaseChatModel, modelName string) *CompactMiddleware {
	return NewEinoMiddlewareWithConfig(compact.DefaultCompactionConfig(), chatModel, modelName)
}

// NewEinoMiddlewareWithConfig 接受显式 CompactionConfig 的一行 API.
//
// 模型窗口大小取 ModelPreset.ContextWindow；当 modelName 未命中预设时，
// NewDefaultCompactorWithEinoModel 会输出 Warn 日志.
func NewEinoMiddlewareWithConfig(
	config compact.CompactionConfig,
	chatModel model.BaseChatModel,
	modelName string,
) *CompactMiddleware {
	preset := compact.PresetForModel(modelName)
	compactor := NewDefaultCompactorWithEinoModel(config, chatModel, modelName)
	return NewCompactMiddleware(compactor, preset.ContextWindow)
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
		m.logger.Error("compact failed", "error", err)
		return ctx, state, nil
	}

	// 响应式检查：压缩后仍然超过危险阈值？前置降级
	dangerThreshold := int(float64(m.modelMaxTokens) * 0.95)
	if result.TokensAfter > dangerThreshold {
		m.logger.Warn("post-compact still over danger threshold, triggering reactive truncate",
			"tokens_after", result.TokensAfter,
			"danger_threshold", dangerThreshold)
		reactiveResult, rerr := m.compactor.ReactiveCompact(ctx, result.Messages, m.modelMaxTokens)
		if rerr == nil && reactiveResult != nil {
			result = reactiveResult
			m.logger.Info("reactive truncate done",
				"tokens_before", result.TokensBefore,
				"tokens_after", result.TokensAfter,
				"trigger", result.Trigger)
		} else if rerr != nil {
			m.logger.Error("reactive truncate failed", "error", rerr)
		}
	}

	if result.WasCompacted {
		m.logger.Info("compact applied",
			"tokens_before", result.TokensBefore,
			"tokens_after", result.TokensAfter,
			"trigger", result.Trigger,
			"messages_before", len(state.Messages),
			"messages_after", len(result.Messages))
	}

	state.Messages = ToSchemaMessages(result.Messages)
	return ctx, state, nil
}
