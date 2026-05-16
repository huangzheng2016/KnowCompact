package compact

import (
	"strings"
	"testing"
)

func TestPresetForModel(t *testing.T) {
	tests := []struct {
		modelName        string
		expectedWindow   int
		expectedNamePart string
	}{
		{"claude-opus-4-7-20250601", 200_000, "Opus"},
		{"claude-sonnet-4-6", 200_000, "Sonnet"},
		{"claude-haiku-4-5-20251001", 200_000, "Haiku"},
		{"gpt-4o", 128_000, "GPT-4o"},
		{"gpt-4o-mini", 128_000, "Mini"},
		{"gpt-4-turbo", 128_000, "Turbo"},
		{"deepseek-v3", 128_000, "DeepSeek"},
		{"qwen-max", 128_000, "Qwen"},
		{"unknown-model-123", 128_000, "Unknown"},
	}

	for _, tt := range tests {
		preset := PresetForModel(tt.modelName)
		if preset.ContextWindow != tt.expectedWindow {
			t.Errorf("PresetForModel(%q).ContextWindow = %d, want %d",
				tt.modelName, preset.ContextWindow, tt.expectedWindow)
		}
		if !strings.Contains(preset.Name, tt.expectedNamePart) {
			t.Errorf("PresetForModel(%q).Name = %q, want contains %q",
				tt.modelName, preset.Name, tt.expectedNamePart)
		}
	}
}

func TestModelPreset_AutoCompactThreshold(t *testing.T) {
	opus := PresetClaudeOpus4()
	// Opus: 200K - min(32K, 20K) - 13K = 200K - 20K - 13K = 167K
	expected := 200_000 - 20_000 - 13_000
	if opus.AutoCompactThreshold() != expected {
		t.Errorf("Opus threshold = %d, want %d", opus.AutoCompactThreshold(), expected)
	}

	gpt4o := PresetGPT4o()
	// GPT-4o: 128K - min(16384, 16K) - 10K = 128K - 16K - 10K = 102K
	expected = 128_000 - 16_000 - 10_000
	if gpt4o.AutoCompactThreshold() != expected {
		t.Errorf("GPT-4o threshold = %d, want %d", gpt4o.AutoCompactThreshold(), expected)
	}
}

func TestModelPreset_CustomPreset(t *testing.T) {
	preset := CustomPreset(50_000, 4_000).WithBuffer(5_000).WithName("my-model")
	if preset.ContextWindow != 50_000 {
		t.Errorf("ContextWindow = %d", preset.ContextWindow)
	}
	if preset.AutoCompactThreshold() != 41_000 {
		t.Errorf("threshold = %d, want 41000", preset.AutoCompactThreshold())
	}
	if preset.Name != "my-model" {
		t.Errorf("Name = %q", preset.Name)
	}
}

func TestModelPreset_EffectiveWindow(t *testing.T) {
	preset := CustomPreset(100_000, 8_000)
	effective := preset.EffectiveWindow()
	if effective != 92_000 {
		t.Errorf("effective = %d, want 92000", effective)
	}
}
