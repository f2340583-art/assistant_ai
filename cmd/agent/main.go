// Command agent runs the personal AI assistant: Telegram bot, task tracker,
// calendar reminders and the daily AI-generated business summary.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // embed the IANA tz database so LoadLocation never depends on the OS/container image having it installed

	"fahriddin-ai/internal/ai"
	"fahriddin-ai/internal/billz"
	"fahriddin-ai/internal/calendar"
	"fahriddin-ai/internal/config"
	"fahriddin-ai/internal/db"
	"fahriddin-ai/internal/scheduler"
	"fahriddin-ai/internal/stt"
	"fahriddin-ai/internal/summary"
	"fahriddin-ai/internal/tasks"
	"fahriddin-ai/internal/telegram"
	"fahriddin-ai/internal/webapp"
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

	if err := bootstrapAdminUser(ctx, database, cfg, log); err != nil {
		log.Error("webapp: failed to bootstrap admin user", "err", err)
	}

	taskStore := tasks.NewStore(database)

	var calClient *calendar.Client
	if cfg.GoogleServiceAccountJSON != "" {
		calClient, err = calendar.NewClient(ctx, cfg.GoogleServiceAccountJSON, cfg.GoogleCalendarID)
		if err != nil {
			log.Error("calendar: failed to initialize, continuing without calendar", "err", err)
			calClient = nil
		}
	} else {
		log.Warn("calendar: GOOGLE_SERVICE_ACCOUNT_JSON not set, running without calendar integration")
	}

	aiClient := ai.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicModel)

	var sttClient *stt.Client
	if cfg.GeminiAPIKey != "" {
		sttClient = stt.NewClient(cfg.GeminiAPIKey, cfg.GeminiModel)
	} else {
		log.Warn("stt: GEMINI_API_KEY not set, running without voice recognition")
	}

	var billzClient *billz.Client
	if cfg.BillzSecretToken != "" {
		billzClient, err = billz.NewClient(ctx, cfg.BillzSecretToken)
		if err != nil {
			log.Error("billz: failed to initialize, continuing without business dashboard", "err", err)
			billzClient = nil
		}
	} else {
		log.Warn("billz: BILLZ_SECRET_TOKEN not set, running without business dashboard")
	}

	summaryBuilder := summary.NewBuilder(taskStore, calClient, aiClient, loc, log)

	bot, err := telegram.New(cfg.TelegramBotToken, cfg.TelegramOwnerIDs, taskStore, summaryBuilder, aiClient, sttClient, loc, log)
	if err != nil {
		return err
	}

	if err := bot.RegisterMenuButton(cfg.WebAppURL); err != nil {
		log.Warn("telegram: failed to register Mini App menu button", "err", err)
	}

	webServer := webapp.NewServer(taskStore, summaryBuilder, billzClient, database, loc, cfg.TelegramBotToken, cfg.TelegramOwnerIDs, log)
	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: webServer.Handler(),
	}
	go func() {
		log.Info("webapp: listening", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("webapp: server error", "err", err)
		}
	}()

	sched := scheduler.New(database, bot, summaryBuilder, calClient, billzClient, scheduler.Config{
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil {
		log.Error("webapp: shutdown error", "err", shutdownErr)
	}
	sched.Stop()

	if err != nil && err != context.Canceled {
		return err
	}
	return nil
}

// bootstrapAdminUser creates the first browser-login account if the users
// table is still empty — otherwise the platform's /login page would have no
// way to ever be used. Uses ADMIN_USERNAME/ADMIN_PASSWORD if set, otherwise
// generates a random password and logs it once (visible only in the
// process's own log output, never stored in plaintext).
func bootstrapAdminUser(ctx context.Context, database *sql.DB, cfg *config.Config, log *slog.Logger) error {
	count, err := webapp.UserCount(ctx, database)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	username := cfg.AdminUsername
	if username == "" {
		username = "admin"
	}
	password := cfg.AdminPassword
	generated := password == ""
	if generated {
		raw := make([]byte, 9)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		password = hex.EncodeToString(raw)
	}

	if err := webapp.CreateUser(ctx, database, username, password, "Admin", "owner"); err != nil {
		return err
	}

	if generated {
		log.Warn("webapp: bootstrapped first admin account with a generated password — save it now, it will not be shown again",
			"username", username, "password", password)
	} else {
		log.Info("webapp: bootstrapped first admin account from ADMIN_USERNAME/ADMIN_PASSWORD", "username", username)
	}
	return nil
}
