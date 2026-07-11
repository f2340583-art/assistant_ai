// Package telegram implements the bot interface: command handling and
// message delivery to the owner.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"fahriddin-ai/internal/summary"
	"fahriddin-ai/internal/tasks"
)

const helpText = `Я твой личный AI-ассистент. Доступные команды:

/summary — сводка на сейчас
/addtask <текст> — добавить задачу
/tasks — список открытых задач
/done <id> — отметить задачу выполненной
/help — это сообщение`

// Bot wraps the Telegram API and routes commands from the owner only.
type Bot struct {
	api     *tgbotapi.BotAPI
	ownerID int64
	tasks   *tasks.Store
	summary *summary.Builder
	log     *slog.Logger
}

func New(token string, ownerID int64, taskStore *tasks.Store, summaryBuilder *summary.Builder, log *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("connect telegram bot: %w", err)
	}
	return &Bot{api: api, ownerID: ownerID, tasks: taskStore, summary: summaryBuilder, log: log}, nil
}

// SendToOwner delivers a message to the owner chat (used for the scheduled
// daily summary and calendar reminders).
func (b *Bot) SendToOwner(text string) error {
	msg := tgbotapi.NewMessage(b.ownerID, text)
	_, err := b.api.Send(msg)
	return err
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
	if msg.From == nil || msg.From.ID != b.ownerID {
		b.log.Warn("telegram: ignoring message from non-owner", "from", msg.From)
		return
	}

	reply := b.dispatch(ctx, msg)
	if reply == "" {
		return
	}
	if err := b.SendToOwner(reply); err != nil {
		b.log.Error("telegram: failed to send reply", "err", err)
	}
}

func (b *Bot) dispatch(ctx context.Context, msg *tgbotapi.Message) string {
	text := strings.TrimSpace(msg.Text)
	switch {
	case text == "/start", text == "/help":
		return helpText

	case text == "/summary":
		return b.summary.Generate(ctx)

	case strings.HasPrefix(text, "/addtask"):
		desc := strings.TrimSpace(strings.TrimPrefix(text, "/addtask"))
		if desc == "" {
			return "Использование: /addtask <текст задачи>"
		}
		id, err := b.tasks.Add(ctx, desc, nil)
		if err != nil {
			b.log.Error("telegram: add task failed", "err", err)
			return "Не удалось добавить задачу, попробуй ещё раз."
		}
		return fmt.Sprintf("Задача #%d добавлена: %s", id, desc)

	case text == "/tasks":
		open, err := b.tasks.ListOpen(ctx)
		if err != nil {
			b.log.Error("telegram: list tasks failed", "err", err)
			return "Не удалось получить список задач."
		}
		if len(open) == 0 {
			return "Открытых задач нет."
		}
		var sb strings.Builder
		sb.WriteString("Открытые задачи:\n")
		for _, t := range open {
			sb.WriteString(fmt.Sprintf("#%d %s\n", t.ID, t.Description))
		}
		return sb.String()

	case strings.HasPrefix(text, "/done"):
		arg := strings.TrimSpace(strings.TrimPrefix(text, "/done"))
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return "Использование: /done <id задачи>"
		}
		if err := b.tasks.Complete(ctx, id); err != nil {
			return fmt.Sprintf("Задача #%d не найдена или уже выполнена.", id)
		}
		return fmt.Sprintf("Задача #%d отмечена как выполненная.", id)

	default:
		return "Не знаю такую команду. /help — список команд."
	}
}
