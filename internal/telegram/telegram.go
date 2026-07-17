// Package telegram implements the bot interface. All free-form interaction
// (typed text, voice messages, and anything beyond the quick-action
// buttons) goes through internal/agent's Claude tool-use agent — there are
// no rigid slash commands to memorize.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"fahriddin-ai/internal/agent"
	"fahriddin-ai/internal/stt"
	"fahriddin-ai/internal/summary"
	"fahriddin-ai/internal/tasks"
)

const (
	btnSummary = "📅 Bugungi xulosa"
	btnTasks   = "📋 Vazifalarim"
	btnNewTask = "➕ Yangi vazifa"
	btnHelp    = "❓ Yordam"
)

const welcomeText = `Assalomu alaykum! Men — sizning shaxsiy AI-yordamchingizman. 🤖

Menga oddiy tilda yozing yoki ovozli xabar yuboring, masalan:
• "Ertaga soat 15:00 da mijoz bilan uchrashuvni qo'sh"
• "Alisherning bu oy va o'tgan yilgi savdosini solishtir"
• "Xulosa ber"

Pastdagi tugmalardan ham foydalanishingiz mumkin.`

const helpText = `Men bunlarni qila olaman:

📅 Bugungi xulosa — vazifalar va taqvim asosida kunlik xulosa
📋 Vazifalarim — ochiq vazifalar ro'yxati
➕ Yangi vazifa — vazifa qo'shish (oddiy matn yoki ovoz bilan ham mumkin)
✅ Vazifani bajarilgan deb belgilash — shunchaki "N-raqamli vazifani bajardim" deng
📊 Billz bo'yicha savollar — savdo, sotuvchilar, tovarlar, ombor haqida oddiy
   tilda so'rang, kerak bo'lsa Excel fayl ham beraman

Buyruqlarni yodlash shart emas — menga oddiy tilda yoki ovozli xabar bilan yozavering.`

var mainKeyboard = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton(btnSummary),
		tgbotapi.NewKeyboardButton(btnTasks),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton(btnNewTask),
		tgbotapi.NewKeyboardButton(btnHelp),
	),
)

// Bot wraps the Telegram API and routes messages from allowed owners only.
type Bot struct {
	api      *tgbotapi.BotAPI
	ownerIDs []int64
	tasks    *tasks.Store
	summary  *summary.Builder
	agent    *agent.Agent
	stt      *stt.Client // optional: nil disables voice transcription
	loc      *time.Location
	log      *slog.Logger
}

func New(token string, ownerIDs []int64, taskStore *tasks.Store, summaryBuilder *summary.Builder, agentClient *agent.Agent, sttClient *stt.Client, loc *time.Location, log *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("connect telegram bot: %w", err)
	}
	return &Bot{
		api: api, ownerIDs: ownerIDs, tasks: taskStore, summary: summaryBuilder,
		agent: agentClient, stt: sttClient, loc: loc, log: log,
	}, nil
}

func (b *Bot) isOwner(id int64) bool {
	for _, ownerID := range b.ownerIDs {
		if ownerID == id {
			return true
		}
	}
	return false
}

// RegisterMenuButton points Telegram's chat menu button (the button next to
// the message input) at the Mini App. go-telegram-bot-api v5.5.1 has no
// web_app support in its typed structs, so this goes through the library's
// raw MakeRequest escape hatch directly against the Bot API. A no-op if
// webAppURL is empty (e.g. no public HTTPS URL configured yet).
func (b *Bot) RegisterMenuButton(webAppURL string) error {
	if webAppURL == "" {
		return nil
	}
	menuButton, err := json.Marshal(map[string]any{
		"type": "web_app",
		"text": "Ilova",
		"web_app": map[string]string{
			"url": webAppURL,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal menu button: %w", err)
	}
	for _, ownerID := range b.ownerIDs {
		_, err = b.api.MakeRequest("setChatMenuButton", tgbotapi.Params{
			"chat_id":     strconv.FormatInt(ownerID, 10),
			"menu_button": string(menuButton),
		})
		if err != nil {
			return fmt.Errorf("set chat menu button for %d: %w", ownerID, err)
		}
	}
	return nil
}

// SendToOwner delivers a message to every owner chat (used for the scheduled
// daily summary and calendar reminders), with the quick-action keyboard
// attached.
func (b *Bot) SendToOwner(text string) error {
	var firstErr error
	for _, ownerID := range b.ownerIDs {
		msg := tgbotapi.NewMessage(ownerID, text)
		msg.ReplyMarkup = mainKeyboard
		if _, err := b.api.Send(msg); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Run blocks, processing incoming updates until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return ctx.Err()
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(ctx, update.Message)
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || !b.isOwner(msg.From.ID) {
		b.log.Warn("telegram: ignoring message from non-owner", "from", msg.From)
		return
	}

	var reply string
	var attachment *agent.Attachment
	switch {
	case msg.Text == "/start":
		reply = welcomeText

	case msg.Voice != nil:
		reply, attachment = b.handleVoice(ctx, msg.Chat.ID, msg.Voice)

	default:
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return
		}
		reply, attachment = b.handleFreeText(ctx, msg.Chat.ID, text)
	}

	if reply != "" {
		out := tgbotapi.NewMessage(msg.Chat.ID, reply)
		out.ReplyMarkup = mainKeyboard
		if _, err := b.api.Send(out); err != nil {
			b.log.Error("telegram: failed to send reply", "err", err)
		}
	}
	if attachment != nil {
		doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FileBytes{
			Name:  attachment.Filename,
			Bytes: attachment.Bytes,
		})
		if _, err := b.api.Send(doc); err != nil {
			b.log.Error("telegram: failed to send attachment", "err", err)
		}
	}
}

func (b *Bot) handleVoice(ctx context.Context, chatID int64, voice *tgbotapi.Voice) (string, *agent.Attachment) {
	if b.stt == nil {
		return "Ovozli xabarlarni hozircha tushuna olmayapman — matn bilan yozib yuboring.", nil
	}

	url, err := b.api.GetFileDirectURL(voice.FileID)
	if err != nil {
		b.log.Error("telegram: get voice file url failed", "err", err)
		return "Ovozli xabarni yuklab bo'lmadi.", nil
	}

	audio, err := downloadFile(ctx, url)
	if err != nil {
		b.log.Error("telegram: download voice file failed", "err", err)
		return "Ovozli xabarni yuklab bo'lmadi.", nil
	}

	transcript, err := b.stt.Transcribe(ctx, audio)
	if err != nil {
		b.log.Error("telegram: transcribe failed", "err", err)
		return "Ovozli xabarni tushuna olmadim, matn bilan urinib ko'ring.", nil
	}

	reply, attachment := b.handleFreeText(ctx, chatID, transcript)
	return fmt.Sprintf("🎤 Eshitdim: \"%s\"\n\n%s", transcript, reply), attachment
}

// handleFreeText is the single entry point for both typed and
// voice-transcribed messages, plus quick-action button presses (which just
// send their own label as plain text). The 4 buttons are handled inline
// (deterministic, no AI call) — everything else goes through the tool-use
// agent, which decides for itself whether to answer directly, call a Billz
// report tool, manage tasks, or ask a clarifying question.
func (b *Bot) handleFreeText(ctx context.Context, chatID int64, text string) (string, *agent.Attachment) {
	switch text {
	case btnSummary:
		return b.summary.Generate(ctx), nil
	case btnTasks:
		return b.formatTaskList(ctx), nil
	case btnNewTask:
		return "Qanday vazifani qo'shay? Matnini yozib yuboring.", nil
	case btnHelp:
		return helpText, nil
	}

	result, err := b.agent.Handle(ctx, chatID, text)
	if err != nil {
		b.log.Error("telegram: agent handle failed", "err", err)
		return "Uzr, hozir javob bera olmadim. Birozdan keyin qayta urinib ko'ring.", nil
	}
	return result.Text, result.Attachment
}

func (b *Bot) formatTaskList(ctx context.Context) string {
	open, err := b.tasks.ListOpen(ctx)
	if err != nil {
		b.log.Error("telegram: list tasks failed", "err", err)
		return "Vazifalar ro'yxatini olib bo'lmadi."
	}
	if len(open) == 0 {
		return "Ochiq vazifalar yo'q. 🎉"
	}
	var sb strings.Builder
	sb.WriteString("Ochiq vazifalar:\n")
	for _, t := range open {
		line := fmt.Sprintf("#%d %s", t.ID, t.Description)
		if t.DueAt != nil {
			line += fmt.Sprintf(" (muddat: %s)", t.DueAt.In(b.loc).Format("02.01 15:04"))
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func downloadFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
