package compact

import (
	"context"
	"testing"
)

func TestLightModelFor(t *testing.T) {
	tests := []struct {
		mainModel string
		expected  string
	}{
		{"claude-opus-4-7-20250601", "claude-sonnet-4-6"},
		{"claude-sonnet-4-6", "claude-haiku-4-5"},
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5"},
		{"gpt-4o", "gpt-4o-mini"},
		{"gpt-4o-mini", "gpt-4o-mini"},
		{"gpt-4-turbo", "gpt-4o-mini"},
		{"deepseek-v3", "deepseek-v3"},
		{"unknown-model", "unknown-model"},
	}

	for _, tt := range tests {
		got := LightModelFor(tt.mainModel)
		if got != tt.expected {
			t.Errorf("LightModelFor(%q) = %q, want %q", tt.mainModel, got, tt.expected)
		}
	}
}

func TestCompactionModelConfig_ResolveModel(t *testing.T) {
	// StrategySameModel
	cfg := CompactionModelConfig{
		Strategy:  StrategySameModel,
		MainModel: "claude-opus-4-7",
	}
	if got := cfg.ResolveModel(); got != "claude-opus-4-7" {
		t.Errorf("same model strategy: got %q, want claude-opus-4-7", got)
	}

	// StrategyLightModel
	cfg = CompactionModelConfig{
		Strategy:  StrategyLightModel,
		MainModel: "claude-opus-4-7",
	}
	if got := cfg.ResolveModel(); got != "claude-sonnet-4-6" {
		t.Errorf("light model strategy: got %q, want claude-sonnet-4-6", got)
	}

	// StrategyCustomModel
	cfg = CompactionModelConfig{
		Strategy:    StrategyCustomModel,
		CustomModel: "my-custom-model",
	}
	if got := cfg.ResolveModel(); got != "my-custom-model" {
		t.Errorf("custom model strategy: got %q, want my-custom-model", got)
	}
}

func TestLLMSummarizer(t *testing.T) {
	client := &mockLLMClient{response: "<analysis>test</analysis>\n<summary>\n1. Primary Request: test\n</summary>"}
	s := NewLLMSummarizer(client, "claude-haiku-4-5")

	if s.ModelName() != "claude-haiku-4-5" {
		t.Errorf("ModelName = %q", s.ModelName())
	}

	summary, err := s.GenerateSummary(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary != client.response {
		t.Errorf("summary = %q, want %q", summary, client.response)
	}
}

func TestLLMSummarizer_WithPrompt(t *testing.T) {
	client := &mockLLMClient{}
	s := NewLLMSummarizer(client, "test-model").WithPrompt("custom prompt")
	if s.compactPrompt != "custom prompt" {
		t.Error("WithPrompt did not set prompt")
	}
}

type mockLLMClient struct {
	response string
}

func (m *mockLLMClient) Chat(ctx context.Context, prompt string, messages []Message) (string, error) {
	if m.response != "" {
		return m.response, nil
	}
	return "<summary>mock</summary>", nil
}

func TestEstimateCompactionCost(t *testing.T) {
	in, out := EstimateCompactionCost(0)
	if in != 100_000 {
		t.Errorf("default input estimate = %d", in)
	}
	if out != 20_000 {
		t.Errorf("output estimate = %d", out)
	}

	in, out = EstimateCompactionCost(50_000)
	if in != 50_000 {
		t.Errorf("explicit input = %d", in)
	}
}

func TestFormatCompactionCost(t *testing.T) {
	s := FormatCompactionCost("haiku", 50_000, 5_000)
	if s == "" {
		t.Error("empty cost string")
	}
}
