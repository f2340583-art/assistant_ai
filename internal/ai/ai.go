// Package ai wraps the Anthropic Claude API to turn raw business data into
// a readable daily summary, always in Uzbek (Latin script). General
// conversation and free-form request handling live in internal/agent's
// tool-use agent instead.
package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const noMarkdownRule = `Javobni oddiy matn sifatida yoz — Telegram xabari
markdown formatlashni (**qalin**, __tagiz__, # sarlavha, - ro'yxat belgisi va
h.k.) ko'rsatmaydi, shuning uchun bunday belgilardan mutlaqo foydalanma.
Kerak bo'lsa emoji yoki oddiy tire (-) bilan ro'yxat qilishing mumkin.`

const summarySystemPrompt = `Sen — biznes egasining shaxsiy AI-yordamchisan. Vazifang —
berilgan ma'lumotlar (vazifalar, taqvim tadbirlari va h.k.) asosida qisqa,
foydali kunlik xulosa tuzish. Faqat o'zbek tilida, lotin alifbosida yoz.
Aniq va samimiy yoz, suvsiz, emojilarni me'yorida ishlat. Agar biror
ma'lumot bo'lmasa — uni o'ylab topma, shunchaki tilga olma.

` + noMarkdownRule

// Client generates natural-language text via the Claude API, always in
// Uzbek (Latin script).
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
			{Text: summarySystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(rawContext)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("claude request: %w", err)
	}
	text := extractText(msg)
	if text == "" {
		return "", fmt.Errorf("claude returned no text content")
	}
	return text, nil
}

func extractText(msg *anthropic.Message) string {
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return stripMarkdown(sb.String())
}

// stripMarkdown is a safety net in case the model uses markdown syntax
// despite being told not to — Telegram messages are sent as plain text, so
// leftover "**"/"__"/"# " would otherwise show up literally.
func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, "#")
		if trimmed != line {
			lines[i] = strings.TrimSpace(trimmed)
		}
	}
	return strings.Join(lines, "\n")
}
