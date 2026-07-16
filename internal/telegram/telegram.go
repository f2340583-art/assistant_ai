// Package telegram implements the bot interface. All interaction is
// free-form: typed text and voice messages are classified into intents by
// Claude (see internal/ai), with a handful of Uzbek quick-action buttons as
// shortcuts — there are no rigid slash commands to memorize.
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

	"fahriddin-ai/internal/ai"
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
• "Bugungi vazifalarni ko'rsat"
• "Xulosa ber"

Pastdagi tugmalardan ham foydalanishingiz mumkin.`

const helpText = `Men bunlarni qila olaman:

📅 Bugungi xulosa — vazifalar va taqvim asosida kunlik xulosa
📋 Vazifalarim — ochiq vazifalar ro'yxati
➕ Yangi vazifa — vazifa qo'shish (oddiy matn yoki ovoz bilan ham mumkin)
✅ Vazifani bajarilgan deb belgilash — shunchaki "N-raqamli vazifani bajardim" deng

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
	ai       *ai.Client
	stt      *stt.Client // optional: nil disables voice transcription
	loc      *time.Location
	log      *slog.Logger
}

func New(token string, ownerIDs []int64, taskStore *tasks.Store, summaryBuilder *summary.Builder, aiClient *ai.Client, sttClient *stt.Client, loc *time.Location, log *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("connect telegram bot: %w", err)
	}
	return &Bot{
		api: api, ownerIDs: ownerIDs, tasks: taskStore, summary: summaryBuilder,
		ai: aiClient, stt: sttClient, loc: loc, log: log,
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
	switch {
	case msg.Text == "/start":
		reply = welcomeText

	case msg.Voice != nil:
		reply = b.handleVoice(ctx, msg.Voice)

	default:
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return
		}
		reply = b.handleFreeText(ctx, text)
	}

	if reply == "" {
		return
	}
	out := tgbotapi.NewMessage(msg.Chat.ID, reply)
	out.ReplyMarkup = mainKeyboard
	if _, err := b.api.Send(out); err != nil {
		b.log.Error("telegram: failed to send reply", "err", err)
	}
}

func (b *Bot) handleVoice(ctx context.Context, voice *tgbotapi.Voice) string {
	if b.stt == nil {
		return "Ovozli xabarlarni hozircha tushuna olmayapman — matn bilan yozib yuboring."
	}

	url, err := b.api.GetFileDirectURL(voice.FileID)
	if err != nil {
		b.log.Error("telegram: get voice file url failed", "err", err)
		return "Ovozli xabarni yuklab bo'lmadi."
	}

	audio, err := downloadFile(ctx, url)
	if err != nil {
		b.log.Error("telegram: download voice file failed", "err", err)
		return "Ovozli xabarni yuklab bo'lmadi."
	}

	transcript, err := b.stt.Transcribe(ctx, audio)
	if err != nil {
		b.log.Error("telegram: transcribe failed", "err", err)
		return "Ovozli xabarni tushuna olmadim, matn bilan urinib ko'ring."
	}

	reply := b.handleFreeText(ctx, transcript)
	return fmt.Sprintf("🎤 Eshitdim: \"%s\"\n\n%s", transcript, reply)
}

// handleFreeText is the single entry point for both typed and
// voice-transcribed messages, plus quick-action button presses (which just
// send their own label as plain text).
func (b *Bot) handleFreeText(ctx context.Context, text string) string {
	switch text {
	case btnSummary:
		return b.summary.Generate(ctx)
	case btnTasks:
		return b.formatTaskList(ctx)
	case btnNewTask:
		return "Qanday vazifani qo'shay? Matnini yozib yuboring."
	case btnHelp:
		return helpText
	}

	intent, err := b.ai.ClassifyIntent(ctx, text, time.Now().In(b.loc))
	if err != nil {
		b.log.Error("telegram: classify intent failed", "err", err)
		reply, chatErr := b.ai.Chat(ctx, text)
		if chatErr != nil {
			return "Uzr, hozir tushuna olmadim. Birozdan keyin qayta urinib ko'ring."
		}
		return reply
	}

	switch intent.Type {
	case "add_task":
		if intent.TaskText == "" {
			return "Qanday vazifani qo'shay? Matnini yozib yuboring."
		}
		id, err := b.tasks.Add(ctx, intent.TaskText, intent.DueAt)
		if err != nil {
			b.log.Error("telegram: add task failed", "err", err)
			return "Vazifani qo'shib bo'lmadi, qaytadan urinib ko'ring."
		}
		reply := fmt.Sprintf("✅ Vazifa qo'shildi: #%d %s", id, intent.TaskText)
		if intent.DueAt != nil {
			reply += fmt.Sprintf(" (muddat: %s)", intent.DueAt.In(b.loc).Format("02.01 15:04"))
		}
		return reply

	case "list_tasks":
		return b.formatTaskList(ctx)

	case "complete_task":
		if intent.TaskID == nil {
			return "Qaysi vazifa raqamini bajarilgan deb belgilay? Raqamini yozing:\n\n" + b.formatTaskList(ctx)
		}
		if err := b.tasks.Complete(ctx, *intent.TaskID); err != nil {
			return fmt.Sprintf("#%d raqamli vazifa topilmadi yoki allaqachon bajarilgan.", *intent.TaskID)
		}
		return fmt.Sprintf("✅ #%d vazifa bajarilgan deb belgilandi.", *intent.TaskID)

	case "get_summary":
		return b.summary.Generate(ctx)

	case "help":
		return helpText

	default: // chitchat or anything unrecognized
		reply, err := b.ai.Chat(ctx, text)
		if err != nil {
			b.log.Error("telegram: chat fallback failed", "err", err)
			return "Uzr, hozir javob bera olmadim."
		}
		return reply
	}
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
