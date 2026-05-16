package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ============================================================
// InMemorySessionMemoryStore
// ============================================================

func TestInMemorySessionMemoryStore_BasicOps(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionMemoryStore()

	if !store.IsEmpty(ctx) {
		t.Fatal("fresh store should be empty")
	}

	store.SetMemory("first summary")
	if store.IsEmpty(ctx) {
		t.Fatal("store should not be empty after SetMemory")
	}

	mem, err := store.GetMemory(ctx)
	if err != nil {
		t.Fatalf("GetMemory error: %v", err)
	}
	if mem != "first summary" {
		t.Errorf("expected 'first summary', got %q", mem)
	}

	store.AppendMemory("second chunk")
	mem, _ = store.GetMemory(ctx)
	if !strings.Contains(mem, "first summary") || !strings.Contains(mem, "second chunk") {
		t.Errorf("appended memory missing pieces: %q", mem)
	}

	// 空白片段不应改变状态
	before := mem
	store.AppendMemory("   ")
	after, _ := store.GetMemory(ctx)
	if before != after {
		t.Error("AppendMemory should ignore whitespace-only snippets")
	}

	store.SetLastSummarizedMessageID("msg-42")
	if id := store.GetLastSummarizedMessageID(ctx); id != "msg-42" {
		t.Errorf("expected 'msg-42', got %q", id)
	}

	store.Clear()
	if !store.IsEmpty(ctx) {
		t.Fatal("Clear should empty memory")
	}
	if id := store.GetLastSummarizedMessageID(ctx); id != "" {
		t.Errorf("Clear should reset message id, got %q", id)
	}
}

func TestInMemorySessionMemoryStore_PluggedIntoCompactor(t *testing.T) {
	store := NewInMemorySessionMemoryStore()
	store.SetMemory("synthesized memory from background extractor")
	store.SetLastSummarizedMessageID("msg-002")

	config := DefaultCompactionConfig()
	sm := NewSessionMemoryCompactor(store, config)

	msgs := []Message{
		{Role: RoleUser, MessageID: "msg-001", Content: []ContentBlock{{Type: ContentTypeText, Text: "q1"}}},
		{Role: RoleAssistant, MessageID: "msg-002", Content: []ContentBlock{{Type: ContentTypeText, Text: "a1"}}},
		{Role: RoleUser, MessageID: "msg-003", Content: []ContentBlock{{Type: ContentTypeText, Text: "q2"}}},
	}
	result, err := sm.TryCompact(context.Background(), msgs, 100_000)
	if err != nil {
		t.Fatalf("TryCompact error: %v", err)
	}
	if result == nil || !result.WasCompacted {
		t.Fatal("expected compaction with InMemorySessionMemoryStore")
	}
	if !strings.Contains(result.Summary, "synthesized memory") {
		t.Errorf("summary missing memory text: %q", result.Summary)
	}
}

// ============================================================
// PreCompact / PostCompact 钩子
// ============================================================

func TestPreCompactHook_RewriteMessages(t *testing.T) {
	called := false
	hook := func(ctx context.Context, messages []Message) ([]Message, error) {
		called = true
		// 注入一条标记消息证明 hook 起作用
		marker := Message{
			Role:    RoleSystem,
			Content: []ContentBlock{{Type: ContentTypeText, Text: "[pre-compact tag]"}},
		}
		return append([]Message{marker}, messages...), nil
	}

	config := DefaultCompactionConfig().WithPreCompact(hook)
	compactor := NewDefaultCompactor(config, nil, nil)

	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}}},
	}
	result, err := compactor.Compact(context.Background(), msgs, 100_000)
	if err != nil {
		t.Fatalf("Compact error: %v", err)
	}
	if !called {
		t.Fatal("PreCompact hook never invoked")
	}
	if len(result.Messages) == 0 || result.Messages[0].Role != RoleSystem {
		t.Fatal("PreCompact-injected marker missing from output")
	}
}

func TestPreCompactHook_ErrorAborts(t *testing.T) {
	sentinel := errors.New("simulated pre-compact failure")
	hook := func(ctx context.Context, messages []Message) ([]Message, error) {
		return nil, sentinel
	}

	config := DefaultCompactionConfig().WithPreCompact(hook)
	compactor := NewDefaultCompactor(config, nil, nil)

	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}}},
	}
	_, err := compactor.Compact(context.Background(), msgs, 100_000)
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
}

func TestPostCompactHook_Rewrites(t *testing.T) {
	called := false
	hook := func(ctx context.Context, result *CompactionResult) (*CompactionResult, error) {
		called = true
		result.Trigger = "rewritten:" + result.Trigger
		return result, nil
	}

	// 让 MicroCompact 触发，借此走 PostCompact 路径
	tools := make([]Message, 0, 10)
	for i := 0; i < 6; i++ {
		tools = append(tools,
			Message{Role: RoleAssistant, Content: []ContentBlock{{
				Type: ContentTypeToolUse, ToolName: "Read", ToolUseID: fmt.Sprintf("t%d", i),
				ToolInput: `{"file_path":"x.go"}`,
			}}},
			Message{Role: RoleTool, Content: []ContentBlock{{
				Type: ContentTypeToolResult, ToolName: "Read", ToolUseID: fmt.Sprintf("t%d", i),
				ToolOutput: strings.Repeat("a", 1500),
			}}},
		)
	}

	config := DefaultCompactionConfig().WithPostCompact(hook)
	compactor := NewDefaultCompactor(config, nil, nil)
	result, err := compactor.Compact(context.Background(), tools, 100_000)
	if err != nil {
		t.Fatalf("Compact error: %v", err)
	}
	if !called {
		t.Fatal("PostCompact hook never invoked")
	}
	if !strings.HasPrefix(result.Trigger, "rewritten:") {
		t.Errorf("PostCompact rewrite lost: %q", result.Trigger)
	}
}

// ============================================================
// Pinned 消息保留（降级截断）
// ============================================================

func TestFallbackTruncate_PreservesPinnedMessages(t *testing.T) {
	// 构造足够多的消息让降级截断真正丢弃部分
	var msgs []Message
	msgs = append(msgs, Message{
		Role:    RoleUser,
		Extra:   map[string]string{"pinned": "true"},
		Content: []ContentBlock{{Type: ContentTypeText, Text: "PINNED_SYSTEM_PROMPT"}},
	})
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentTypeText, Text: fmt.Sprintf("filler %d", i)}},
		})
	}

	config := DefaultCompactionConfig().
		WithFallbackTruncateRatio(0.2).
		WithFallbackTruncateMinKeep(2)

	result, err := fallbackTruncateDirect(msgs, EstimateMessageTokens(msgs), config)
	if err != nil {
		t.Fatalf("fallbackTruncateDirect error: %v", err)
	}

	// pinned 消息必须仍存在
	found := false
	for _, m := range result.Messages {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "PINNED_SYSTEM_PROMPT") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("pinned message dropped by fallback truncation")
	}
}

// ============================================================
// Scenario 预设
// ============================================================

func TestPresetForScenario_RetainsSensibleValues(t *testing.T) {
	cases := []struct {
		scenario Scenario
		check    func(c CompactionConfig) error
	}{
		{ScenarioCodingAgent, func(c CompactionConfig) error {
			if c.RecentToolsKeep < 3 {
				return fmt.Errorf("coding agent should keep more tools, got %d", c.RecentToolsKeep)
			}
			return nil
		}},
		{ScenarioCustomerSupport, func(c CompactionConfig) error {
			if c.FileReinjectBudget != 0 {
				return fmt.Errorf("customer support should not reinject files, got %d", c.FileReinjectBudget)
			}
			return nil
		}},
		{ScenarioResearchAnalysis, func(c CompactionConfig) error {
			if c.SMMaxTokens < c.SMMinTokens {
				return fmt.Errorf("SMMaxTokens (%d) < SMMinTokens (%d)", c.SMMaxTokens, c.SMMinTokens)
			}
			return nil
		}},
		{ScenarioLightweight, func(c CompactionConfig) error {
			if c.SMMinTokens < 1_000_000 {
				return fmt.Errorf("lightweight should effectively disable SM, got %d", c.SMMinTokens)
			}
			return nil
		}},
	}
	for _, tc := range cases {
		cfg := PresetForScenario(tc.scenario)
		if err := tc.check(cfg); err != nil {
			t.Errorf("scenario %q: %v", tc.scenario, err)
		}
	}
}

// ============================================================
// JSON 附件路径解析
// ============================================================

func TestParseFilePathFromToolInput_JSONHappyPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"file_path":"src/main.go"}`, "src/main.go"},
		{`{"path":"a.txt","encoding":"utf-8"}`, "a.txt"},
		{`{"filepath":"with space.md"}`, "with space.md"},
		// 转义引号在 string 字段里
		{`{"file":"a\"b.go"}`, `a"b.go`},
		// 缺字段
		{`{"command":"ls"}`, ""},
	}
	for _, tc := range cases {
		got := parseFilePathFromToolInput(tc.input)
		if got != tc.want {
			t.Errorf("parseFilePathFromToolInput(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseFilePathFromToolInput_NonJSONFallback(t *testing.T) {
	// 不是合法 JSON，但仍能被旧字符串扫描捞到
	got := parseFilePathFromToolInput(`raw "file_path":"fallback.go", maybe-trailing`)
	if got != "fallback.go" {
		t.Errorf("expected fallback parse to return 'fallback.go', got %q", got)
	}
}

// ============================================================
// TokenCache
// ============================================================

func TestTokenCache_HitsAndEviction(t *testing.T) {
	cache := NewTokenCache(2).WithMinBlockLen(0) // 不限最小长度，方便测试
	a := strings.Repeat("a", 100)
	b := strings.Repeat("b", 100)
	c := strings.Repeat("c", 100)

	tA := cache.EstimateBlock(a)
	if tA == 0 {
		t.Fatal("expected non-zero tokens")
	}
	if cache.Size() != 1 {
		t.Errorf("expected size 1, got %d", cache.Size())
	}

	// 重复命中：tokens 一致，size 不变
	tA2 := cache.EstimateBlock(a)
	if tA != tA2 || cache.Size() != 1 {
		t.Errorf("cache hit should be deterministic; size=%d tA=%d tA2=%d", cache.Size(), tA, tA2)
	}

	cache.EstimateBlock(b)
	cache.EstimateBlock(c)
	if cache.Size() != 2 {
		t.Errorf("expected size 2 (capacity), got %d", cache.Size())
	}
}

func TestEstimateMessageTokensWithCache_MatchesUncached(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: strings.Repeat("xy", 1000)}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeToolUse, ToolName: "Read", ToolInput: `{"file_path":"a"}`, ToolUseID: "t1"}}},
		{Role: RoleTool, Content: []ContentBlock{{Type: ContentTypeToolResult, ToolName: "Read", ToolUseID: "t1", ToolOutput: strings.Repeat("z", 5000)}}},
	}
	cache := NewTokenCache(64)
	noCache := EstimateMessageTokens(msgs)
	withCache := EstimateMessageTokensWithCache(msgs, cache)
	// 允许 ±1 的舍入差异（保守填充使用浮点）
	if withCache < noCache-1 || withCache > noCache+1 {
		t.Errorf("cached estimate diverges too much: cached=%d uncached=%d", withCache, noCache)
	}
}

// ============================================================
// CompactBoundary 标记可识别
// ============================================================

func TestCompactBoundary_RoundTrip(t *testing.T) {
	msg := CreateCompactBoundary(1000, 500, "full_compact")
	if !isCompactBoundary(msg) {
		t.Fatal("created CompactBoundary message not recognized by isCompactBoundary")
	}

	// 兼容历史中文标记
	legacy := Message{Role: RoleSystem, Content: []ContentBlock{{Type: ContentTypeText, Text: "[压缩边界]\n触发器: x"}}}
	if !isCompactBoundary(legacy) {
		t.Error("legacy chinese boundary not recognized — backward compatibility lost")
	}
}

// ============================================================
// PromptTooLong 哨兵错误
// ============================================================

func TestDefaultPromptTooLongDetector_SentinelWrapping(t *testing.T) {
	wrapped := fmt.Errorf("api call failed: %w", ErrPromptTooLong)
	if !DefaultPromptTooLongDetector(wrapped) {
		t.Error("wrapped ErrPromptTooLong should be detected via errors.Is")
	}

	if DefaultPromptTooLongDetector(errors.New("generic network error")) {
		t.Error("generic error should NOT be detected as PTL")
	}
}

func TestConfigPromptTooLongDetectorOverride(t *testing.T) {
	custom := func(err error) bool {
		return strings.Contains(err.Error(), "MY_SDK_LIMIT")
	}
	cfg := DefaultCompactionConfig().WithPromptTooLongDetector(custom)
	if !detectPromptTooLong(errors.New("MY_SDK_LIMIT reached"), cfg.PromptTooLongDetector) {
		t.Error("custom detector should fire for MY_SDK_LIMIT")
	}
	if detectPromptTooLong(errors.New("prompt is too long"), cfg.PromptTooLongDetector) {
		t.Error("custom detector should NOT defer to default when explicitly set")
	}
}
