// Command agent runs the personal AI assistant: Telegram bot, task tracker,
// calendar reminders and the daily AI-generated business summary.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // embed the IANA tz database so LoadLocation never depends on the OS/container image having it installed

	"fahriddin-ai/internal/ai"
	"fahriddin-ai/internal/calendar"
	"fahriddin-ai/internal/config"
	"fahriddin-ai/internal/db"
	"fahriddin-ai/internal/scheduler"
	"fahriddin-ai/internal/summary"
	"fahriddin-ai/internal/tasks"
	"fahriddin-ai/internal/telegram"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.Migrate(database, "migrations"); err != nil {
		return err
	}

	taskStore := tasks.NewStore(database)

	calClient, err := calendar.NewClient(ctx, cfg.GoogleServiceAccountJSON, cfg.GoogleCalendarID)
	if err != nil {
		log.Error("calendar: failed to initialize, continuing without calendar", "err", err)
		calClient = nil
	}

	aiClient := ai.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicModel)

	summaryBuilder := summary.NewBuilder(taskStore, calClient, aiClient, loc, log)

	bot, err := telegram.New(cfg.TelegramBotToken, cfg.TelegramOwnerID, taskStore, summaryBuilder, log)
	if err != nil {
		return err
	}

	sched := scheduler.New(database, bot, summaryBuilder, calClient, scheduler.Config{
		Location:            loc,
		SummaryHour:         cfg.SummaryHour,
		SummaryMinute:       cfg.SummaryMinute,
		ReminderLeadMinutes: cfg.ReminderLeadMin,
		ReminderIntervalMin: cfg.ReminderIntervalMn,
	}, log)

	if err := sched.Start(ctx); err != nil {
		return err
	}

	log.Info("agent started", "timezone", cfg.Timezone, "summary_time",
		time.Date(0, 1, 1, cfg.SummaryHour, cfg.SummaryMinute, 0, 0, loc).Format("15:04"))

	err = bot.Run(ctx)
	sched.Stop()
	if err != nil && err != context.Canceled {
		return err
	}
	return nil
}
