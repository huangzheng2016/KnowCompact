package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// openaiClient 是一个轻量的 OpenAI API 兼容客户端.
// 实现了 model.BaseChatModel 接口，可直接用于 eino ChatModelAgent.
type openaiClient struct {
	apiKey     string
	baseURL    string
	modelName  string
	httpClient *http.Client
}

// newOpenAIClient 创建 OpenAI 兼容客户端.
func newOpenAIClient(apiKey, baseURL, modelName string) model.BaseChatModel {
	return &openaiClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		modelName:  modelName,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Generate 发送同步聊天请求.
func (c *openaiClient) Generate(
	ctx context.Context,
	input []*schema.Message,
	opts ...model.Option,
) (*schema.Message, error) {
	reqBody := c.buildRequest(input)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return c.parseResponse(respBody)
}

// Stream 发送流式聊天请求（简化实现：同步后模拟流）.
func (c *openaiClient) Stream(
	ctx context.Context,
	input []*schema.Message,
	opts ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	msg, err := c.Generate(ctx, input, opts...)
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

// buildRequest 构造 OpenAI Chat Completions 请求.
func (c *openaiClient) buildRequest(msgs []*schema.Message) map[string]any {
	var messages []map[string]any
	for _, m := range msgs {
		msg := map[string]any{
			"role":    string(m.Role),
			"content": m.Content,
		}
		// kimi-k2.6 开启 thinking 时，assistant 的 tool call 消息需要 reasoning_content
		if m.Role == schema.Assistant && m.ReasoningContent != "" {
			msg["reasoning_content"] = m.ReasoningContent
		}
		if len(m.ToolCalls) > 0 {
			var toolCalls []map[string]any
			for _, tc := range m.ToolCalls {
				toolCalls = append(toolCalls, map[string]any{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				})
			}
			msg["tool_calls"] = toolCalls
			// kimi-k2.6: 有 tool_calls 的 assistant 消息必须有 reasoning_content 字段
			if m.Role == schema.Assistant && m.ReasoningContent == "" {
				msg["reasoning_content"] = ""
			}
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		messages = append(messages, msg)
	}

	return map[string]any{
		"model":    c.modelName,
		"messages": messages,
		"max_tokens": 4096,
	}
}

// parseResponse 解析 OpenAI Chat Completions 响应.
func (c *openaiClient) parseResponse(body []byte) (*schema.Message, error) {
	var result struct {
		Choices []struct {
			Message struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := result.Choices[0].Message
	msg := &schema.Message{
		Role:    schema.RoleType(choice.Role),
		Content: choice.Content,
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     result.Usage.PromptTokens,
				CompletionTokens: result.Usage.CompletionTokens,
			},
		},
	}

	for _, tc := range choice.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	return msg, nil
}

// 接口检查
var _ model.BaseChatModel = (*openaiClient)(nil)
