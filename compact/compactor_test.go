package compact

import (
	"context"
	"strings"
	"testing"
)

func TestMicroCompact(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "read main.go"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentTypeToolUse, ToolName: "Read", ToolUseID: "t1", ToolInput: `{"path":"main.go"}`},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Read", ToolUseID: "t1", ToolOutput: strings.Repeat("a", 4000)},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentTypeToolUse, ToolName: "Bash", ToolUseID: "t2", ToolInput: `{"cmd":"ls"}`},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Bash", ToolUseID: "t2", ToolOutput: strings.Repeat("b", 4000)},
		}},
	}

	result := MicroCompact(messages, 1, true) // 只保留最近 1 个

	if !result.WasCompacted {
		t.Fatal("expected compaction to happen")
	}
	if result.ToolsCleared != 1 {
		t.Fatalf("expected 1 tool cleared, got %d", result.ToolsCleared)
	}

	// 第一个工具结果应该被清除
	firstToolOutput := messages[2].Content[0].ToolOutput
	if firstToolOutput != "[Old tool result content cleared]" {
		t.Fatalf("expected first tool output cleared, got: %s", firstToolOutput[:50])
	}

	// 第二个工具结果应该保留
	secondToolOutput := messages[4].Content[0].ToolOutput
	if secondToolOutput == "[Old tool result content cleared]" {
		t.Fatal("expected second tool output to be preserved")
	}

	// tool_use 块不应被修改
	toolUseInput := messages[1].Content[0].ToolInput
	if toolUseInput != `{"path":"main.go"}` {
		t.Fatal("tool_use block was modified")
	}
}

func TestMicroCompactWhitelist(t *testing.T) {
	messages := []Message{
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Read", ToolUseID: "t1", ToolOutput: "old read result"},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Agent", ToolUseID: "t2", ToolOutput: "agent result (not compactable)"},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Bash", ToolUseID: "t3", ToolOutput: "old bash result 1"},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Bash", ToolUseID: "t4", ToolOutput: "old bash result 2"},
		}},
	}

	customWhitelist := map[string]bool{"Bash": true} // 只有 Bash 可压缩
	result := MicroCompact(messages, 1, true, customWhitelist)

	if !result.WasCompacted {
		t.Fatal("expected compaction with custom whitelist")
	}

	// Read 和 Agent 结果不应被清除（不在白名单中）
	if messages[0].Content[0].ToolOutput != "old read result" {
		t.Error("Read result should NOT be cleared (not in whitelist)")
	}
	if messages[1].Content[0].ToolOutput != "agent result (not compactable)" {
		t.Error("Agent result should NOT be cleared (not in whitelist)")
	}
	// 最近的 1 个 Bash 结果（index 3）应保留
	if messages[3].Content[0].ToolOutput != "old bash result 2" {
		t.Error("most recent Bash result should be kept")
	}
	// 更早的 Bash 结果（index 2）应被清除
	if messages[2].Content[0].ToolOutput != "[Old tool result content cleared]" {
		t.Error("older Bash result should be cleared (in whitelist)")
	}
}

func TestRoughTokenEstimate(t *testing.T) {
	tests := []struct {
		text     string
		expected int
	}{
		{"", 0},
		{"hello", 2},   // 5 chars / 4 = 2
		{"1234", 1},    // 4 chars / 4 = 1
		{"12345", 2},   // 5 chars / 4 = 2
		{"hello world, this is a test", 7}, // 28 chars / 4 = 7
	}

	for _, tt := range tests {
		got := RoughTokenEstimate(tt.text)
		if got != tt.expected {
			t.Errorf("RoughTokenEstimate(%q) = %d, want %d", tt.text, got, tt.expected)
		}
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentTypeText, Text: "hello world"},
		}},
	}

	tokens := EstimateMessageTokens(messages)
	// "hello world" = 11 chars / 4 * 4/3 = ~4
	if tokens < 1 || tokens > 20 {
		t.Errorf("unexpected token count: %d", tokens)
	}
}

func TestGroupMessagesByAPIRound(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "q1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "a1"}}, MessageID: "id-1"},
		{Role: RoleTool, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolOutput: "r1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "a2"}}, MessageID: "id-2"},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "q2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "a3"}}, MessageID: "id-3"},
	}

	groups := groupMessagesByAPIRound(messages)

	if len(groups) < 2 {
		t.Fatalf("expected at least 2 groups, got %d", len(groups))
	}

	// 验证: 每条 assistant 消息标志新组的开始
	// 算法保证 tool 消息和同轮的 user 消息跟随上一 assistant
	foundA1 := false
	foundTool := false
	for _, g := range groups {
		for _, m := range g {
			if m.MessageID == "id-1" {
				foundA1 = true
			}
			if m.Role == RoleTool {
				foundTool = true
				// tool 结果应和产生它的 assistant 在同一组
				hasAssistant := false
				for _, gm := range g {
					if gm.Role == RoleAssistant {
						hasAssistant = true
					}
				}
				if !hasAssistant {
					t.Error("tool result should be in same group as the assistant that called it")
				}
			}
		}
	}
	if !foundA1 {
		t.Error("message with id-1 not found in any group")
	}
	if !foundTool {
		t.Error("tool message not found in any group")
	}
}

func TestAdjustIndexToPreserveAPIInvariants_ToolPairing(t *testing.T) {
	// 场景: tool_use 在范围外，tool_result 在范围内
	messages := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentTypeToolUse, ToolName: "Read", ToolUseID: "t_missing"},
		}},
		{Role: RoleTool, Content: []ContentBlock{
			{Type: ContentTypeToolResult, ToolName: "Read", ToolUseID: "t_missing"},
		}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "next question"}}},
	}

	// startIndex = 1 会切断 tool_use
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 1)

	if adjusted != 0 {
		t.Errorf("expected startIndex 0 to include missing tool_use, got %d", adjusted)
	}
}

func TestAdjustIndexToPreserveAPIInvariants_ThinkingSeparation(t *testing.T) {
	messages := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentTypeThinking, Thinking: "let me think..."},
		}, MessageID: "shared-id"},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentTypeToolUse, ToolName: "Read", ToolUseID: "t1"},
		}, MessageID: "shared-id"},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "done"}}},
	}

	// startIndex = 1 会分离 thinking 块
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 1)

	if adjusted != 0 {
		t.Errorf("expected startIndex 0 to include shared message.id thinking, got %d", adjusted)
	}
}

func TestFormatCompactSummary(t *testing.T) {
	summary := `<analysis>
This is a draft analysis.
It has multiple lines.
</analysis>
<summary>
1. Primary Request: Test the compaction system
2. Key Technical Concepts:
   - Go programming
</summary>
Some trailing text.`

	result := FormatCompactSummary(summary)

	if strings.Contains(result, "<analysis>") {
		t.Error("analysis block should be removed")
	}
	if strings.Contains(result, "</analysis>") {
		t.Error("analysis closing tag should be removed")
	}
	if strings.Contains(result, "<summary>") {
		t.Error("summary tags should be replaced")
	}
	if !strings.Contains(result, "Summary:") {
		t.Error("summary should be prefixed with 'Summary:'")
	}
	if !strings.Contains(result, "Primary Request") {
		t.Error("summary content should be preserved")
	}
}

func TestCircuitBreaker(t *testing.T) {
	cb := newCircuitBreaker(3)

	if cb.isOpen() {
		t.Error("circuit breaker should be closed initially")
	}

	// 记录 3 次失败
	cb.recordFailure()
	cb.recordFailure()
	if cb.isOpen() {
		t.Error("circuit breaker should not open before threshold")
	}
	cb.recordFailure()
	if !cb.isOpen() {
		t.Error("circuit breaker should open after 3 failures")
	}

	// 重置
	cb.reset()
	if cb.isOpen() {
		t.Error("circuit breaker should be closed after reset")
	}

	// 成功后重置
	cb.recordFailure()
	cb.recordFailure()
	cb.recordSuccess()
	if cb.isOpen() {
		t.Error("circuit breaker should be closed after success")
	}
}

func TestPreprocessMessages(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentTypeText, Text: "look at this"},
			{Type: ContentTypeImage},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentTypeText, Text: "I see an image"},
			{Type: ContentTypeThinking, Thinking: "the image shows..."},
		}},
	}

	processed := preprocessMessages(messages)

	// 图片应被替换为占位符
	if len(processed) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(processed))
	}

	hasPlaceholder := false
	for _, block := range processed[0].Content {
		if block.Type == ContentTypeText && block.Text == "[image]" {
			hasPlaceholder = true
		}
	}
	if !hasPlaceholder {
		t.Error("image should be replaced with [image] placeholder")
	}

	// thinking 应保留
	hasThinking := false
	for _, block := range processed[1].Content {
		if block.Type == ContentTypeThinking {
			hasThinking = true
		}
	}
	if !hasThinking {
		t.Error("thinking block should be preserved")
	}
}

func TestSessionMemoryCompactor(t *testing.T) {
	store := &mockSessionStore{
		memory:              "test memory content",
		lastSummarizedMsgID: "msg-002",
		isEmpty:             false,
	}

	config := DefaultCompactionConfig()
	smCompactor := NewSessionMemoryCompactor(store, config)

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "q1"}}, MessageID: "msg-001"},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "a1"}}, MessageID: "msg-002"},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "q2"}}, MessageID: "msg-003"},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "a2"}}, MessageID: "msg-004"},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "q3"}}, MessageID: "msg-005"},
	}

	result, err := smCompactor.TryCompact(context.Background(), messages, 100000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected compaction result")
	}
	if !result.WasCompacted {
		t.Fatal("expected compaction to happen")
	}
	if result.Trigger != "session_memory" {
		t.Errorf("expected trigger 'session_memory', got '%s'", result.Trigger)
	}
}

func TestSessionMemoryCompactor_EmptyStore(t *testing.T) {
	store := &mockSessionStore{isEmpty: true}
	config := DefaultCompactionConfig()
	smCompactor := NewSessionMemoryCompactor(store, config)

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}}},
	}

	result, err := smCompactor.TryCompact(context.Background(), messages, 100000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for empty store")
	}
}

// mockSessionStore 模拟 Session Memory 存储.
type mockSessionStore struct {
	memory              string
	lastSummarizedMsgID string
	isEmpty             bool
}

func (m *mockSessionStore) GetMemory(ctx context.Context) (string, error) {
	return m.memory, nil
}

func (m *mockSessionStore) GetLastSummarizedMessageID(ctx context.Context) string {
	return m.lastSummarizedMsgID
}

func (m *mockSessionStore) IsEmpty(ctx context.Context) bool {
	return m.isEmpty
}

func TestPTLTruncation(t *testing.T) {
	fc := &FullCompactor{maxPTLRetries: 3}

	// 构造多组消息，每组足够长以确保裁剪后数量明显减少
	var messages []Message
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentTypeText, Text: "query " + itoa(i)}},
		})
		messages = append(messages, Message{
			Role:      RoleAssistant,
			Content:   []ContentBlock{{Type: ContentTypeText, Text: "response " + itoa(i)}},
			MessageID: "id-" + itoa(i),
		})
	}

	truncated := fc.truncateForPTLRetry(messages)
	if truncated == nil {
		t.Fatal("truncation should succeed")
	}
	if len(truncated) >= len(messages) {
		t.Errorf("truncated messages (%d) should be fewer than original (%d)", len(truncated), len(messages))
	}
	if len(truncated) > 0 && truncated[0].Role != RoleUser {
		t.Error("first message after PTL truncation should be user role")
	}
}

func TestShouldAutoCompact_PreventsRecursion(t *testing.T) {
	config := DefaultCompactionConfig()
	ac := NewAutoCompactor(nil, nil, config)

	// session_memory 源不应触发压缩
	if ac.ShouldAutoCompact(nil, 200000, QuerySourceSessionMemory) {
		t.Error("session_memory query source should not trigger auto-compact")
	}

	// compact 源不应触发压缩
	if ac.ShouldAutoCompact(nil, 200000, QuerySourceCompact) {
		t.Error("compact query source should not trigger auto-compact")
	}
}

func TestReinjectBudget(t *testing.T) {
	budget := DefaultReinjectBudget()

	files := []FileAttachment{
		{Path: "a.go", Content: strings.Repeat("x", 20000)}, // ~5K tokens
		{Path: "b.go", Content: strings.Repeat("y", 20000)}, // ~5K tokens
		{Path: "c.go", Content: strings.Repeat("z", 20000)}, // ~5K tokens
	}

	selected := budget.SelectFiles(files)

	if len(selected) > budget.maxFiles {
		t.Errorf("selected %d files, max is %d", len(selected), budget.maxFiles)
	}

	// 每个文件不应超过 maxTokens
	for _, f := range selected {
		fileTokens := RoughTokenEstimate(f.Content)
		if fileTokens > budget.maxTokens {
			t.Errorf("file %s has %d tokens, max is %d", f.Path, fileTokens, budget.maxTokens)
		}
	}
}

func TestEnsureFirstMessageIsUser(t *testing.T) {
	messages := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "response"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "query"}}},
	}

	result := ensureFirstMessageIsUser(messages)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != RoleUser {
		t.Error("first message should be user role")
	}
	if !strings.Contains(result[0].Content[0].Text, "PTL Retry Marker") {
		t.Error("first message should be PTL marker")
	}
}

func TestIsPromptTooLongError(t *testing.T) {
	tests := []struct {
		errMsg   string
		expected bool
	}{
		{"prompt is too long", true},
		{"context_length_exceeded", true},
		{"normal error", false},
		{"400 bad request: token limit exceeded", true},
	}

	for _, tt := range tests {
		err := &testError{msg: tt.errMsg}
		if got := isPromptTooLongError(err); got != tt.expected {
			t.Errorf("isPromptTooLongError(%q) = %v, want %v", tt.errMsg, got, tt.expected)
		}
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
