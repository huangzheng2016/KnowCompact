package compact

import (
	"context"
	"strings"
	"testing"
)

func TestCompactionHook_PreProcess_MicroCompactOnly(t *testing.T) {
	config := DefaultCompactionConfig()
	config.MicroCompactEnabled = true
	config.RecentToolsKeep = 1

	compactor := NewDefaultCompactor(config, nil, nil)
	hook := NewMicroCompactHook(compactor)
	hook.Silent = true

	messages := []Message{
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Read", ToolUseID: "t1",
				ToolOutput: strings.Repeat("old result ", 1000)},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Bash", ToolUseID: "t2",
				ToolOutput: "recent result"},
		}},
	}

	result, err := hook.PreProcess(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 旧结果应被清除
	if result[0].Content[0].ToolOutput != "[Old tool result content cleared]" {
		t.Errorf("expected old result cleared, got: %s", result[0].Content[0].ToolOutput[:50])
	}
	// 新结果应保留
	if result[1].Content[0].ToolOutput != "recent result" {
		t.Errorf("expected recent result preserved, got: %s", result[1].Content[0].ToolOutput)
	}
}

func TestCompactionHook_PreProcess_FullCompactTriggered(t *testing.T) {
	config := DefaultCompactionConfig()
	config.MicroCompactEnabled = true
	// 设一个极低阈值，确保触发
	config.AutoCompactBufferTokens = 199_000
	config.MaxOutputTokensForSummary = 500

	compactor := NewDefaultCompactor(config, &mockHookSummarizer{}, nil)
	hook := NewCompactionHook(compactor, 200_000)
	hook.Silent = true

	// 构造大量消息确保超过阈值
	var messages []Message
	for i := 0; i < 100; i++ {
		messages = append(messages, Message{
			Role: RoleUser,
			Content: []ContentBlock{
				{Type: ContentTypeText, Text: strings.Repeat("long message content to fill context ", 50)},
			},
			MessageID: "msg-" + itoa(i),
		})
	}

	result, err := hook.PreProcess(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 压缩后消息数应显著减少
	if len(result) >= len(messages) {
		t.Errorf("expected compaction to reduce message count, got %d → %d", len(messages), len(result))
	}
}

func TestCompactionHook_NoCompactor(t *testing.T) {
	hook := &CompactionHook{Silent: true}
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}}},
	}

	result, err := hook.PreProcess(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Error("should return unchanged messages when no compactor")
	}
}

func TestCompactionHook_OnCompacted(t *testing.T) {
	config := DefaultCompactionConfig()
	config.AutoCompactBufferTokens = 199_000
	config.MaxOutputTokensForSummary = 500

	compactor := NewDefaultCompactor(config, &mockHookSummarizer{}, nil)

	var capturedResult *CompactionResult
	hook := NewCompactionHook(compactor, 200_000).
		WithCallback(func(r *CompactionResult) { capturedResult = r }).
		WithSilent(true)

	var messages []Message
	for i := 0; i < 100; i++ {
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentTypeText, Text: strings.Repeat("fill ", 100)}},
		})
	}

	_, err := hook.PreProcess(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedResult == nil {
		t.Fatal("OnCompacted callback should have been called")
	}
	if !capturedResult.WasCompacted {
		t.Error("expected compaction to happen")
	}
}

func TestCompactionHook_FallbackOnError(t *testing.T) {
	// 创建一个无 summarizer 的 compactor，全量压缩不会触发 LLM
	config := DefaultCompactionConfig()
	config.AutoCompactBufferTokens = 199_000
	config.MaxOutputTokensForSummary = 500

	// 不设置 summarizer → 第3层不可用
	compactor := NewDefaultCompactor(config, nil, nil)
	hook := NewCompactionHook(compactor, 200_000)
	hook.Silent = true

	var messages []Message
	for i := 0; i < 100; i++ {
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentTypeText, Text: strings.Repeat("fill ", 100)}},
		})
	}

	// 不应 panic，应优雅回退
	result, err := hook.PreProcess(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 至少微压缩应执行
	_ = result
}

// mockHookSummarizer 用于测试的摘要生成器.
type mockHookSummarizer struct{}

func (m *mockHookSummarizer) GenerateSummary(ctx context.Context, messages []Message) (string, error) {
	return `<analysis>test</analysis>
<summary>
1. Primary Request: test
2. Key Technical Concepts: none
3. Files and Code Sections: none
4. Errors and fixes: none
5. Problem Solving: none
6. All user messages: none
7. Pending Tasks: none
8. Current Work: testing
9. Optional Next Step: none
</summary>`, nil
}
