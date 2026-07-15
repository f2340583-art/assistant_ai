// Package config loads application configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the agent.
type Config struct {
	TelegramBotToken string
	TelegramOwnerID  int64

	DatabaseURL string

	GoogleServiceAccountJSON string // raw JSON content of the service account key
	GoogleCalendarID         string
	SpeechLanguageCode       string // BCP-47 code for Google Speech-to-Text, e.g. "uz-UZ"

	AnthropicAPIKey string
	AnthropicModel  string

	Timezone           string
	SummaryHour        int
	SummaryMinute      int
	ReminderLeadMin    int
	ReminderIntervalMn int

	Port      string // HTTP port for the Mini App server; Railway injects PORT itself
	WebAppURL string // public HTTPS URL of the Mini App; empty disables the Telegram menu button

	BillzSecretToken string // optional: enables the business section of the Mini App dashboard

	// AdminUsername/AdminPassword bootstrap the first browser-login account
	// when the users table is empty at startup. If unset, a random password
	// is generated and logged once instead (see cmd/agent/main.go).
	AdminUsername string
	AdminPassword string
}

// Load reads configuration from environment variables, optionally loading a
// .env file first (for local development; ignored if the file is absent).
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		DatabaseURL:        getEnv("DATABASE_URL", ""),
		GoogleCalendarID:   getEnv("GOOGLE_CALENDAR_ID", "primary"),
		SpeechLanguageCode: getEnv("SPEECH_LANGUAGE_CODE", "uz-UZ"),
		AnthropicModel:     getEnv("ANTHROPIC_MODEL", "claude-sonnet-5"),
		Timezone:           getEnv("TIMEZONE", "Asia/Tashkent"),
		ReminderLeadMin:    getEnvInt("REMINDER_LEAD_MINUTES", 30),
		ReminderIntervalMn: getEnvInt("REMINDER_CHECK_INTERVAL_MINUTES", 15),
		Port:               getEnv("PORT", "8080"),
		WebAppURL:          os.Getenv("WEBAPP_URL"),
		BillzSecretToken:   os.Getenv("BILLZ_SECRET_TOKEN"),
		AdminUsername:      os.Getenv("ADMIN_USERNAME"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
	}

	cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	ownerIDStr := os.Getenv("TELEGRAM_OWNER_ID")
	if ownerIDStr == "" {
		return nil, fmt.Errorf("TELEGRAM_OWNER_ID is required")
	}
	ownerID, err := strconv.ParseInt(ownerIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_OWNER_ID must be a valid integer: %w", err)
	}
	cfg.TelegramOwnerID = ownerID

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	cfg.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	if cfg.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required")
	}

	// Optional: without it the agent runs fine, just without calendar
	// events/reminders (see calendar client init in main.go).
	cfg.GoogleServiceAccountJSON = os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")

	summaryTime := getEnv("SUMMARY_TIME", "08:00")
	hour, minute, err := parseHHMM(summaryTime)
	if err != nil {
		return nil, fmt.Errorf("SUMMARY_TIME must be in HH:MM format: %w", err)
	}
	cfg.SummaryHour = hour
	cfg.SummaryMinute = minute

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func parseHHMM(s string) (hour, minute int, err error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	hour, err = strconv.Atoi(s[0:2])
	if err != nil {
		return 0, 0, err
	}
	minute, err = strconv.Atoi(s[3:5])
	if err != nil {
		return 0, 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("time out of range: %q", s)
	}
	return hour, minute, nil
}
