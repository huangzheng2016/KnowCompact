package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/huangzheng2016/KnowCompact/compact"
	einocompact "github.com/huangzheng2016/KnowCompact/eino"
)

// mockModel 是一个本地 mock 模型，用于测试中间件集成.
// 它记录每次接收到的消息数量，并返回固定回复.
type mockModel struct {
	name            string
	callCount       int
	lastMsgCount    int
	lastTotalTokens int
}

func newMockModel(name string) model.BaseChatModel {
	return &mockModel{name: name}
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
