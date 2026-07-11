// Package ai wraps the Anthropic Claude API used to turn raw business data
// into a readable narrative summary.
package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const systemPrompt = `Ты — личный AI-ассистент владельца бизнеса. Твоя задача —
составлять короткую, полезную ежедневную сводку на русском языке на основе
предоставленных данных (задачи, события календаря и т.д.). Пиши по делу,
дружелюбно, без воды, используй смайлы умеренно. Если каких-то данных нет —
не выдумывай, просто не упоминай их.`

// Client generates natural-language summaries via the Claude API.
type Client struct {
	client anthropic.Client
	model  anthropic.Model
}

func NewClient(apiKey, model string) *Client {
	return &Client{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:  anthropic.Model(model),
	}
}

// GenerateSummary asks Claude to turn the given raw context into a daily
// briefing message ready to send in Telegram.
func (c *Client) GenerateSummary(ctx context.Context, rawContext string) (string, error) {
	msg, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(rawContext)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("claude request: %w", err)
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("claude returned no text content")
	}
	return sb.String(), nil
}
