package eino

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/huangzheng2016/KnowCompact/compact"
)

// ============================================================
// Message 双向转换
// ============================================================

func TestFromSchemaMessages_BasicRoles(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.User, Content: "hello"},
		{Role: schema.Assistant, Content: "world"},
	}
	out := FromSchemaMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Role != compact.RoleUser || out[1].Role != compact.RoleAssistant {
		t.Errorf("unexpected roles: %+v", out)
	}
	if out[0].Content[0].Text != "hello" {
		t.Errorf("text payload lost: %+v", out[0].Content)
	}
}

func TestFromSchemaMessages_ToolUseAndResult(t *testing.T) {
	msgs := []*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "Read",
						Arguments: `{"file_path":"a.go"}`,
					},
				},
			},
		},
		{
			Role:       schema.Tool,
			ToolCallID: "call-1",
			ToolName:   "Read",
			Content:    "file contents",
		},
	}
	out := FromSchemaMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	// Assistant tool_use
	if out[0].Content[0].Type != compact.ContentTypeToolUse {
		t.Errorf("expected tool_use block, got %v", out[0].Content[0].Type)
	}
	if out[0].Content[0].ToolUseID != "call-1" {
		t.Errorf("tool_use_id mismatch: %q", out[0].Content[0].ToolUseID)
	}
	// Tool result —— FromSchemaMessages 会同时生成 text + tool_result 块（前者来自
	// schema.Message.Content，后者来自 ToolCallID 路径）。
	if out[1].Role != compact.RoleTool {
		t.Errorf("expected tool role, got %s", out[1].Role)
	}
	foundResult := false
	for _, b := range out[1].Content {
		if b.Type == compact.ContentTypeToolResult && b.ToolOutput == "file contents" && b.ToolUseID == "call-1" {
			foundResult = true
		}
	}
	if !foundResult {
		t.Errorf("tool_result block missing or wrong: %+v", out[1].Content)
	}
}

func TestRoundTrip_FromSchemaThenTo(t *testing.T) {
	original := []*schema.Message{
		{Role: schema.User, Content: "do X"},
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{ID: "t1", Function: schema.FunctionCall{Name: "Bash", Arguments: `{"cmd":"ls"}`}},
			},
		},
		{Role: schema.Tool, ToolCallID: "t1", ToolName: "Bash", Content: "out"},
		{Role: schema.Assistant, Content: "done"},
	}
	mid := FromSchemaMessages(original)
	round := ToSchemaMessages(mid)
	if len(round) != len(original) {
		t.Fatalf("count mismatch: original=%d round=%d", len(original), len(round))
	}
	if round[0].Content != "do X" {
		t.Errorf("user content lost: %q", round[0].Content)
	}
	if len(round[1].ToolCalls) != 1 || round[1].ToolCalls[0].ID != "t1" {
		t.Errorf("tool_call lost in round trip: %+v", round[1].ToolCalls)
	}
	if round[2].ToolCallID != "t1" || round[2].Content != "out" {
		t.Errorf("tool result lost in round trip: %+v", round[2])
	}
}

func TestFromSchemaMessages_PreservesReasoning(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.Assistant, ReasoningContent: "thinking out loud", Content: "answer"},
	}
	out := FromSchemaMessages(msgs)
	hasThink := false
	hasText := false
	for _, b := range out[0].Content {
		if b.Type == compact.ContentTypeThinking && b.Thinking == "thinking out loud" {
			hasThink = true
		}
		if b.Type == compact.ContentTypeText && b.Text == "answer" {
			hasText = true
		}
	}
	if !hasThink || !hasText {
		t.Errorf("reasoning or text lost: %+v", out[0].Content)
	}
}

// ============================================================
// Compactor / Middleware 构造
// ============================================================

// stubChatModel 不实际调用，仅用于满足 model.BaseChatModel 接口构造路径.
type stubChatModel struct{}

func (s *stubChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...any) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: "stub"}, nil
}

func (s *stubChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...any) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestNewDefaultCompactorWithEinoModel_UnknownModelLogs(t *testing.T) {
	var buf strings.Builder
	logger := compact.NewStdLogger(compact.LogLevelDebug, &buf)
	cfg := compact.DefaultCompactionConfig().WithLogger(logger)

	// 使用一个肯定不匹配任何预设的名字
	compactor := NewDefaultCompactorWithEinoModel(cfg, nil, "totally-fictional-model-xyz")
	if compactor == nil {
		t.Fatal("compactor should not be nil even for unknown model")
	}
	if !strings.Contains(buf.String(), "unknown model preset") {
		t.Errorf("expected warning about unknown preset, got:\n%s", buf.String())
	}
}

func TestNewDefaultCompactorWithEinoModel_KnownModelNoWarn(t *testing.T) {
	var buf strings.Builder
	logger := compact.NewStdLogger(compact.LogLevelDebug, &buf)
	cfg := compact.DefaultCompactionConfig().WithLogger(logger)
	_ = NewDefaultCompactorWithEinoModel(cfg, nil, "claude-opus-4-7")
	if strings.Contains(buf.String(), "unknown model preset") {
		t.Errorf("opus should be a known preset; unexpected warn:\n%s", buf.String())
	}
}
