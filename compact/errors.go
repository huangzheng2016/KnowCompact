package compact

import (
	"errors"
	"strings"
)

// ErrPromptTooLong 通用的 prompt-too-long 哨兵错误。
//
// 用户实现 LLMClient 时，若能从 SDK 错误中识别出 PTL 类型，
// 推荐用 fmt.Errorf("...: %w", ErrPromptTooLong) 包裹，
// 这样 IsPromptTooLongError 会用 errors.Is 准确识别，
// 避免字符串匹配的误判。
var ErrPromptTooLong = errors.New("prompt is too long")

// PromptTooLongDetector 用户自定义的 PTL 错误判断函数。
//
// 不同 LLM SDK 返回的错误结构不同（Anthropic 用 *anthropic.Error，
// OpenAI 用 *openai.APIError，eino 透传原始错误等）。
// 通过此钩子，用户可以传入针对自家 SDK 的精确判定逻辑。
//
// nil 表示使用默认实现（DefaultPromptTooLongDetector）。
type PromptTooLongDetector func(err error) bool

// DefaultPromptTooLongDetector 默认 PTL 判定。
//
// 比旧实现更严格：
//  1. 先用 errors.Is 检查 ErrPromptTooLong 哨兵
//  2. 再按完整短语匹配（避免 "400" + "token" 这类宽松匹配）
//  3. 全部检查均在小写化后进行
func DefaultPromptTooLongDetector(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPromptTooLong) {
		return true
	}
	lower := strings.ToLower(err.Error())
	// 完整短语而非散落关键词，降低误判率
	phrases := []string{
		"prompt is too long",
		"prompt_too_long",
		"context_length_exceeded",
		"context length exceeded",
		"context window exceeded",
		"maximum context length",
		"request too large",
		"too many tokens in the messages",
		"token limit exceeded",
	}
	for _, p := range phrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// detectPromptTooLong 内部统一入口：优先用 config 中的 detector，
// 未设置时回退到默认实现。
func detectPromptTooLong(err error, detector PromptTooLongDetector) bool {
	if err == nil {
		return false
	}
	if detector != nil {
		return detector(err)
	}
	return DefaultPromptTooLongDetector(err)
}
