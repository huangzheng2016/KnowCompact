package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/huangzheng2016/KnowCompact/compact"
	einocompact "github.com/huangzheng2016/KnowCompact/eino"
)

// mockModel 是一个本地 mock 模型，用于测试中间件集成.
//
// 行为:
//   - 普通调用：返回固定回复，便于调试输入流
//   - 压缩调用：识别 BaseCompactPrompt 的关键短语，吐出
//     合法的 <analysis>/<summary> 块。这样 FullCompact 走到 LLM
//     摘要路径时也能拿到可解析的输出，端到端验证 Layer 3.
//
// CompactCalls 计数仅统计后一种情况，便于断言 FullCompact 是否被触发.
type mockModel struct {
	name            string
	callCount       int
	compactCalls    int
	lastMsgCount    int
	lastTotalTokens int
}

func newMockModel(name string) model.BaseChatModel {
	return &mockModel{name: name}
}

// isCompactRequest 判定本次输入是否来自 KnowCompact 的摘要生成调用.
//
// 主要依据 system prompt 中"摘要"/"压缩"等关键词；命中即视为压缩请求.
func isCompactRequest(input []*schema.Message) bool {
	for _, m := range input {
		if m.Role != schema.System {
			continue
		}
		txt := m.Content
		if strings.Contains(txt, "<分析>") || strings.Contains(txt, "<summary>") ||
			strings.Contains(txt, "你正在总结一个 AI Agent") ||
			strings.Contains(txt, "你正在总结一段") {
			return true
		}
	}
	return false
}

// buildMockSummary 构造一个最小的、符合解析格式的压缩摘要回复.
//
// 包含 <分析> 与 <摘要> 双块，便于 FormatCompactSummary 提取.
func buildMockSummary(msgCount int) string {
	return fmt.Sprintf(`<分析>
对话共 %d 条消息，主要围绕调试与功能开发展开。
</分析>

<摘要>
1. 主要请求和意图：mock 摘要 —— 真实 LLM 将填充完整内容
2. 关键技术概念：Go, eino, KnowCompact
3. 文件和代码段：（mock 占位）
4. 错误与修复：（无）
5. 问题解决：（mock 占位）
6. 所有用户消息：（mock 占位）
7. 待办任务：（无）
8. 当前工作：mock 端到端测试 FullCompact 路径
9. 下一步：继续后续轮次
</摘要>`, msgCount)
}

func (m *mockModel) Generate(
	ctx context.Context,
	input []*schema.Message,
	opts ...model.Option,
) (*schema.Message, error) {
	m.callCount++
	m.lastMsgCount = len(input)

	// 估算 tokens
	compactMsgs := einocompact.FromSchemaMessages(input)
	tokens := compact.EstimateMessageTokens(compactMsgs)
	m.lastTotalTokens = tokens

	compactReq := isCompactRequest(input)
	if compactReq {
		m.compactCalls++
		fmt.Printf("  [MockModel] 压缩摘要调用 #%d | 消息数: %d | 估算 tokens: %d\n",
			m.compactCalls, m.lastMsgCount, m.lastTotalTokens)
		return &schema.Message{
			Role:    schema.Assistant,
			Content: buildMockSummary(m.lastMsgCount),
			ResponseMeta: &schema.ResponseMeta{
				Usage: &schema.TokenUsage{
					PromptTokens:     tokens,
					CompletionTokens: 200,
				},
			},
		}, nil
	}

	fmt.Printf("  [MockModel] 第 %d 次调用 | 消息数: %d | 估算 tokens: %d\n",
		m.callCount, m.lastMsgCount, m.lastTotalTokens)

	// 返回固定回复
	return &schema.Message{
		Role:    schema.Assistant,
		Content: fmt.Sprintf("[MockModel 回复] 收到 %d 条消息，估算 %d tokens。", m.lastMsgCount, m.lastTotalTokens),
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     tokens,
				CompletionTokens: 10,
			},
		},
	}, nil
}

func (m *mockModel) Stream(
	ctx context.Context,
	input []*schema.Message,
	opts ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		sw.Send(msg, nil)
	}()
	return sr, nil
}

var _ model.BaseChatModel = (*mockModel)(nil)
