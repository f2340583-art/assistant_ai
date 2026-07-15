// Package ai wraps the Anthropic Claude API: it turns raw business data into
// a readable summary, classifies free-form/voice messages into structured
// intents, and handles general conversational fallback — all in Uzbek
// (Latin script).
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
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

const chatSystemPrompt = `Sen — shaxsiy AI-yordamchisan, uzoq muddatda "Jarvis"
darajasidagi yordamchiga aylanish maqsadida yaratilgansan. Foydalanuvchi bilan
o'zbek tilida, lotin alifbosida, samimiy va qisqa gaplash. Agar savol
biznes/vazifalar/taqvim bilan bog'liq bo'lmasa ham, oddiy suhbatdosh sifatida
javob ber.

` + noMarkdownRule

const classifySystemPrompt = `Foydalanuvchi shaxsiy AI-yordamchiga Telegram
orqali (matn yoki ovozli xabar matnga aylantirilgan holda) yozmoqda. Xabarni
tahlil qilib, unda nima so'ralayotganini classify_intent vositasi orqali
qaytar. Intent turlari:
- add_task: yangi vazifa qo'shishni so'rasa (task_text ga vazifa matnini yoz)
- list_tasks: ochiq vazifalar ro'yxatini so'rasa
- complete_task: biror vazifani bajarilgan deb belgilashni so'rasa (agar
  raqami aniq aytilgan bo'lsa task_id ga yoz)
- get_summary: kunlik xulosa/holat haqida so'rasa
- help: bot nima qila olishini so'rasa
- chitchat: yuqoridagilarning hech biriga to'g'ri kelmasa (oddiy suhbat,
  savol-javob va h.k.)
Agar vazifada muddat/vaqt aytilgan bo'lsa (masalan "ertaga soat 15:00 da"),
uni due_at ga ISO 8601 formatida yoz, aks holda bo'sh qoldir.`

const classifyToolName = "classify_intent"

var classifyTool = anthropic.ToolUnionParam{
	OfTool: &anthropic.ToolParam{
		Name:        classifyToolName,
		Description: param.NewOpt("Foydalanuvchi xabaridan niyat va parametrlarni ajratib olish."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"intent": map[string]any{
					"type":        "string",
					"enum":        []string{"add_task", "list_tasks", "complete_task", "get_summary", "help", "chitchat"},
					"description": "Foydalanuvchi nimani xohlayotgani",
				},
				"task_text": map[string]any{
					"type":        "string",
					"description": "add_task uchun vazifa matni (agar bo'lsa)",
				},
				"task_id": map[string]any{
					"type":        "integer",
					"description": "complete_task uchun vazifa raqami (agar aniq aytilgan bo'lsa)",
				},
				"due_at": map[string]any{
					"type":        "string",
					"description": "Vazifa muddati, ISO 8601 (masalan 2026-07-15T15:00:00+05:00). Aytilmagan bo'lsa bo'sh qoldiring.",
				},
			},
			Required: []string{"intent"},
		},
	},
}

// Intent is the structured result of classifying a free-form/voice message.
type Intent struct {
	Type     string
	TaskText string
	TaskID   *int64
	DueAt    *time.Time
}

type intentRaw struct {
	Intent   string `json:"intent"`
	TaskText string `json:"task_text"`
	TaskID   *int64 `json:"task_id"`
	DueAt    string `json:"due_at"`
}

// Client generates natural-language text and structured intents via the
// Claude API, always in Uzbek (Latin script).
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

// Chat is a general conversational fallback for messages that don't map to
// a structured task/calendar/summary intent.
func (c *Client) Chat(ctx context.Context, text string) (string, error) {
	msg, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: chatSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(text)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("claude chat request: %w", err)
	}
	reply := extractText(msg)
	if reply == "" {
		return "", fmt.Errorf("claude returned no text content")
	}
	return reply, nil
}

// ClassifyIntent turns a free-form (typed or voice-transcribed) message into
// a structured Intent using forced tool-use, so the result is always
// well-formed JSON rather than prose.
func (c *Client) ClassifyIntent(ctx context.Context, text string, now time.Time) (Intent, error) {
	sys := fmt.Sprintf("%s\n\nHozirgi sana va vaqt: %s (Asia/Tashkent).",
		classifySystemPrompt, now.Format("2006-01-02 15:04, Monday"))

	msg, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 512,
		System: []anthropic.TextBlockParam{
			{Text: sys},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(text)),
		},
		Tools:      []anthropic.ToolUnionParam{classifyTool},
		ToolChoice: anthropic.ToolChoiceParamOfTool(classifyToolName),
	})
	if err != nil {
		return Intent{}, fmt.Errorf("classify intent: %w", err)
	}

	for _, block := range msg.Content {
		if block.Type != "tool_use" || block.Name != classifyToolName {
			continue
		}
		var raw intentRaw
		if err := json.Unmarshal(block.Input, &raw); err != nil {
			return Intent{}, fmt.Errorf("parse intent: %w", err)
		}
		intent := Intent{
			Type:     raw.Intent,
			TaskText: strings.TrimSpace(raw.TaskText),
			TaskID:   raw.TaskID,
		}
		if raw.DueAt != "" {
			if t, err := time.Parse(time.RFC3339, raw.DueAt); err == nil {
				intent.DueAt = &t
			}
		}
		return intent, nil
	}
	return Intent{}, fmt.Errorf("claude did not return a classify_intent tool call")
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
