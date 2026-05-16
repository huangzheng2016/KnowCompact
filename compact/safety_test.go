package compact

import (
	"testing"
)

// ============================================================
// Test 1: Claude Code 文档中的真实 bug 场景
// ============================================================
//
// Session 存储（压缩前）:
//   Index N:   assistant, message.id: X, content: [thinking]
//   Index N+1: assistant, message.id: X, content: [tool_use: ORPHAN_ID]
//   Index N+2: assistant, message.id: X, content: [tool_use: VALID_ID]
//   Index N+3: user,      content: [tool_result: ORPHAN_ID, tool_result: VALID_ID]
//
// 旧代码 bug: startIndex = N+2 时，只检查 N+2 的 tool_results，
// 找不到 ORPHAN_ID，导致 ORPHAN tool_use 被排除 → API 报错.

func TestAdjustIndex_ClaudieCodeRealBugScenario(t *testing.T) {
	// 构造消息序列
	messages := []Message{
		{ // N=0
			Role:      RoleAssistant,
			MessageID: "msg-X",
			Content:   []ContentBlock{{Type: ContentTypeThinking, Thinking: "thinking..."}},
		},
		{ // N+1=1
			Role:      RoleAssistant,
			MessageID: "msg-X",
			Content:   []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "ORPHAN_ID", ToolName: "Read"}},
		},
		{ // N+2=2
			Role:      RoleAssistant,
			MessageID: "msg-X",
			Content:   []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "VALID_ID", ToolName: "Read"}},
		},
		{ // N+3=3
			Role: RoleUser,
			Content: []ContentBlock{
				{Type: ContentTypeToolResult, ToolUseID: "ORPHAN_ID", ToolName: "Read", ToolOutput: "result1"},
				{Type: ContentTypeToolResult, ToolUseID: "VALID_ID", ToolName: "Read", ToolOutput: "result2"},
			},
		},
	}

	// startIndex = 2 (N+2) —— 试图从 VALID_ID 的 tool_use 开始保留
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 2)

	// 期望扩展到 N=0，保留全部 4 条消息（thinking + ORPHAN tool_use + VALID tool_use + tool_results）
	if adjusted != 0 {
		t.Errorf("startIndex=2: expected adjusted=0, got %d. "+
			"ORPHAN_ID tool_use at index 1 must be preserved to match tool_result at index 3, "+
			"and thinking at index 0 shares msg-X with index 1-2", adjusted)
	}

	// startIndex = 3 (N+3) —— 从 user 消息开始保留
	adjusted = adjustIndexToPreserveAPIInvariants(messages, 3)

	// 期望扩展到 N=0
	if adjusted != 0 {
		t.Errorf("startIndex=3: expected adjusted=0, got %d. "+
			"Must preserve ORPHAN_ID tool_use (index 1) and thinking (index 0)", adjusted)
	}
}

// ============================================================
// Test 2: thinking 块被截断的场景
// ============================================================

func TestAdjustIndex_ThinkingBlockTruncation(t *testing.T) {
	messages := []Message{
		{ // 0: thinking block
			Role:      RoleAssistant,
			MessageID: "stream-1",
			Content:   []ContentBlock{{Type: ContentTypeThinking, Thinking: "Let me think..."}},
		},
		{ // 1: tool_use in same stream
			Role:      RoleAssistant,
			MessageID: "stream-1",
			Content:   []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "t1", ToolName: "Read"}},
		},
		{ // 2: tool_result
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t1", ToolName: "Read", ToolOutput: "ok"}},
		},
		{ // 3: assistant response
			Role:    RoleAssistant,
			MessageID: "stream-2",
			Content: []ContentBlock{{Type: ContentTypeText, Text: "Done"}},
		},
	}

	// startIndex = 1 —— 试图从 tool_use 开始保留
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 1)

	// 期望扩展到 0（thinking 块必须与同 message.id 的 tool_use 一起保留）
	if adjusted != 0 {
		t.Errorf("startIndex=1: expected adjusted=0, got %d. "+
			"thinking block at index 0 shares message.id 'stream-1' with index 1", adjusted)
	}

	// startIndex = 2 —— 从 tool_result 开始保留
	adjusted = adjustIndexToPreserveAPIInvariants(messages, 2)

	// 期望扩展到 0（tool_result 需要 tool_use，thinking 需要同 message.id）
	if adjusted != 0 {
		t.Errorf("startIndex=2: expected adjusted=0, got %d", adjusted)
	}
}

// ============================================================
// Test 3: 多轮嵌套 tool_use 场景
// ============================================================

func TestAdjustIndex_NestedToolUseRounds(t *testing.T) {
	messages := []Message{
		// Round 1
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "msg1"}}},
		{Role: RoleAssistant, MessageID: "a1", Content: []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "t1", ToolName: "Read"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t1", ToolName: "Read", ToolOutput: "r1"}}},
		{Role: RoleAssistant, MessageID: "a2", Content: []ContentBlock{{Type: ContentTypeText, Text: "ok"}}},
		// Round 2
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "msg2"}}},
		{Role: RoleAssistant, MessageID: "a3", Content: []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "t2", ToolName: "Bash"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t2", ToolName: "Bash", ToolOutput: "r2"}}},
		{Role: RoleAssistant, MessageID: "a4", Content: []ContentBlock{{Type: ContentTypeText, Text: "done"}}},
	}

	// startIndex = 5 —— 从 Round 2 的 tool_use 开始
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 5)

	// 期望保持 5（tool_use t2 和 tool_result t2 都在保留范围内）
	if adjusted != 5 {
		t.Errorf("startIndex=5: expected adjusted=5, got %d", adjusted)
	}

	// startIndex = 6 —— 从 tool_result 开始，需要向前找到 tool_use
	adjusted = adjustIndexToPreserveAPIInvariants(messages, 6)

	// 期望扩展到 5（tool_use t2 在 index 5）
	if adjusted != 5 {
		t.Errorf("startIndex=6: expected adjusted=5, got %d. "+
			"tool_use t2 at index 5 must be preserved to match tool_result at index 6", adjusted)
	}
}

// ============================================================
// Test 4: 无需调整的场景
// ============================================================

func TestAdjustIndex_NoAdjustmentNeeded(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}}},
		{Role: RoleAssistant, MessageID: "a1", Content: []ContentBlock{{Type: ContentTypeText, Text: "hi"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "bye"}}},
	}

	// startIndex = 1 —— 没有 tool_use/tool_result，没有共享 message.id
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 1)

	if adjusted != 1 {
		t.Errorf("startIndex=1: expected adjusted=1, got %d", adjusted)
	}
}

// ============================================================
// Test 5: 边界值
// ============================================================

func TestAdjustIndex_BoundaryValues(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "a"}}},
	}

	// startIndex = 0 —— 边界
	adjusted := adjustIndexToPreserveAPIInvariants(messages, 0)
	if adjusted != 0 {
		t.Errorf("startIndex=0: expected 0, got %d", adjusted)
	}

	// startIndex >= len —— 边界
	adjusted = adjustIndexToPreserveAPIInvariants(messages, 1)
	if adjusted != 1 {
		t.Errorf("startIndex=1 (>=len): expected 1, got %d", adjusted)
	}

	adjusted = adjustIndexToPreserveAPIInvariants(messages, -1)
	if adjusted != -1 {
		t.Errorf("startIndex=-1: expected -1, got %d", adjusted)
	}
}

// ============================================================
// Test 6: 迭代收敛测试 —— thinking 扩展后引入新的配对问题
// ============================================================

func TestAdjustIndex_IterativeConvergence(t *testing.T) {
	// 构造一个需要两次迭代才能收敛的场景：
	//
	// Index 0: assistant, id=A, [thinking]
	// Index 1: assistant, id=A, [tool_use: t1]
	// Index 2: user,      [tool_result: t1]
	// Index 3: assistant, id=B, [thinking]
	// Index 4: assistant, id=B, [tool_use: t2]
	// Index 5: user,      [tool_result: t2]
	//
	// startIndex = 4:
	//   迭代1: thinking 分离 → 保留范围 [4,5] 有 id=B → 向前找到 3 → adjusted=3
	//          配对修复 → [3,5] 有 tool_use t2, tool_result t2 → 无缺失 → 保持 3
	//   迭代2: thinking 分离 → 保留范围 [3,5] 有 id=B → 向前找共享 B 的 → 无（0,1 是 A）→ 保持 3
	//          配对修复 → [3,5] → 无缺失 → 保持 3
	//   不动点！
	messages := []Message{
		{Role: RoleAssistant, MessageID: "A", Content: []ContentBlock{{Type: ContentTypeThinking, Thinking: "t1"}}},
		{Role: RoleAssistant, MessageID: "A", Content: []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "t1", ToolName: "Read"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t1", ToolName: "Read", ToolOutput: "r1"}}},
		{Role: RoleAssistant, MessageID: "B", Content: []ContentBlock{{Type: ContentTypeThinking, Thinking: "t2"}}},
		{Role: RoleAssistant, MessageID: "B", Content: []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "t2", ToolName: "Bash"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t2", ToolName: "Bash", ToolOutput: "r2"}}},
	}

	adjusted := adjustIndexToPreserveAPIInvariants(messages, 4)
	if adjusted != 3 {
		t.Errorf("startIndex=4: expected adjusted=3, got %d. "+
			"thinking at index 3 shares message.id B with index 4", adjusted)
	}

	// 更复杂的场景：startIndex = 5，需要找到 tool_use t2 (index 4) 和 thinking (index 3)
	adjusted = adjustIndexToPreserveAPIInvariants(messages, 5)
	if adjusted != 3 {
		t.Errorf("startIndex=5: expected adjusted=3, got %d", adjusted)
	}
}

// ============================================================
// Test 7: 旧顺序 vs 新顺序对比
// ============================================================
//
// 这个测试证明了旧顺序（先配对后 thinking）会丢失 thinking 块。

func TestAdjustIndex_OldVsNewOrder(t *testing.T) {
	// 场景：
	// Index 0: assistant, id=X, [thinking]
	// Index 1: assistant, id=X, [tool_use: t1]
	// Index 2: user,      [tool_result: t1]
	// Index 3: assistant, id=Y, [text: "done"]
	//
	// startIndex = 2 (从 tool_result 开始)
	//
	// 旧顺序（先配对后 thinking）：
	//   配对修复: [2,3] 有 tool_result t1 但无 tool_use → 缺失 → 向前找到 index 1 → adjusted=1
	//   thinking 分离: [1,3] 有 id=X → 向前找共享 X 的 → 找到 index 0 → adjusted=0
	//   结果: 0 (正确，但这是巧合)
	//
	// 但如果 startIndex = 1 (从 tool_use 开始)：
	//   旧顺序：
	//     配对修复: [1,3] 有 tool_use t1 和 tool_result t1 → 无缺失 → adjusted=1
	//     thinking 分离: [1,3] 有 id=X → 向前找 → 找到 index 0 → adjusted=0
	//   结果: 0 (正确)
	//
	// 新顺序（先 thinking 后配对）对两个 startIndex 都正确。
	// 这个测试验证新顺序的正确性。

	messages := []Message{
		{Role: RoleAssistant, MessageID: "X", Content: []ContentBlock{{Type: ContentTypeThinking, Thinking: "..."}}},
		{Role: RoleAssistant, MessageID: "X", Content: []ContentBlock{{Type: ContentTypeToolUse, ToolUseID: "t1", ToolName: "Read"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t1", ToolName: "Read", ToolOutput: "ok"}}},
		{Role: RoleAssistant, MessageID: "Y", Content: []ContentBlock{{Type: ContentTypeText, Text: "done"}}},
	}

	for _, tc := range []struct {
		start    int
		expected int
	}{
		{1, 0}, // 从 tool_use 开始 → 需要 thinking
		{2, 0}, // 从 tool_result 开始 → 需要 tool_use 和 thinking
		{3, 3}, // 从 "done" 开始 → 无需调整
	} {
		adjusted := adjustIndexToPreserveAPIInvariants(messages, tc.start)
		if adjusted != tc.expected {
			t.Errorf("startIndex=%d: expected adjusted=%d, got %d",
				tc.start, tc.expected, adjusted)
		}
	}
}
