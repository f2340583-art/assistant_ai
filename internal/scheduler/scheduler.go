// Package scheduler drives the daily summary job and periodic calendar
// reminder checks, both pinned to an explicit timezone rather than the
// host/container's local time.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"fahriddin-ai/internal/calendar"
	"fahriddin-ai/internal/summary"
)

// Sender delivers a message to the bot owner.
type Sender interface {
	SendToOwner(text string) error
}

type Scheduler struct {
	cron *cron.Cron
	db   *sql.DB
	loc  *time.Location
	log  *slog.Logger

	sender   Sender
	summary  *summary.Builder
	calendar *calendar.Client

	summaryHour, summaryMinute int
	reminderLeadMin            int
	reminderIntervalMin        int
}

type Config struct {
	Location            *time.Location
	SummaryHour         int
	SummaryMinute       int
	ReminderLeadMinutes int
	ReminderIntervalMin int
}

func New(db *sql.DB, sender Sender, summaryBuilder *summary.Builder, cal *calendar.Client, cfg Config, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:                cron.New(cron.WithLocation(cfg.Location)),
		db:                  db,
		loc:                 cfg.Location,
		log:                 log,
		sender:              sender,
		summary:             summaryBuilder,
		calendar:            cal,
		summaryHour:         cfg.SummaryHour,
		summaryMinute:       cfg.SummaryMinute,
		reminderLeadMin:     cfg.ReminderLeadMinutes,
		reminderIntervalMin: cfg.ReminderIntervalMin,
	}
}

// Start registers the cron jobs, runs a startup catch-up check for a missed
// daily summary, and begins the scheduler loop.
func (s *Scheduler) Start(ctx context.Context) error {
	summarySpec := fmt.Sprintf("%d %d * * *", s.summaryMinute, s.summaryHour)
	if _, err := s.cron.AddFunc(summarySpec, func() { s.runDailySummary(ctx) }); err != nil {
		return fmt.Errorf("schedule daily summary: %w", err)
	}

	reminderSpec := fmt.Sprintf("*/%d * * * *", s.reminderIntervalMin)
	if _, err := s.cron.AddFunc(reminderSpec, func() { s.checkReminders(ctx) }); err != nil {
		return fmt.Errorf("schedule reminder check: %w", err)
	}

	s.cron.Start()
	s.catchUpMissedSummary(ctx)
	return nil
}

func (s *Scheduler) Stop() {
	<-s.cron.Stop().Done()
}

// catchUpMissedSummary sends today's summary immediately if it was supposed
// to have already run (e.g. the process restarted shortly after 08:00) but
// hasn't yet, per summary_log.
func (s *Scheduler) catchUpMissedSummary(ctx context.Context) {
	now := time.Now().In(s.loc)
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), s.summaryHour, s.summaryMinute, 0, 0, s.loc)
	if now.Before(scheduled) {
		return
	}

	sent, err := s.summaryAlreadySentToday(ctx, now)
	if err != nil {
		s.log.Error("scheduler: catch-up check failed", "err", err)
		return
	}
	if sent {
		return
	}

	s.log.Info("scheduler: sending catch-up daily summary after restart")
	s.runDailySummary(ctx)
}

func (s *Scheduler) runDailySummary(ctx context.Context) {
	text := s.summary.Generate(ctx)
	status := "ok"
	if err := s.sender.SendToOwner(text); err != nil {
		s.log.Error("scheduler: failed to send daily summary", "err", err)
		status = "send_failed"
	}
	if err := s.recordSummarySent(ctx, status); err != nil {
		s.log.Error("scheduler: failed to record summary run", "err", err)
	}
}

func (s *Scheduler) summaryAlreadySentToday(ctx context.Context, now time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM summary_log WHERE run_date = $1)`,
		now.Format("2006-01-02"),
	).Scan(&exists)
	return exists, err
}

func (s *Scheduler) recordSummarySent(ctx context.Context, status string) error {
	now := time.Now().In(s.loc)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO summary_log (run_date, status) VALUES ($1, $2)
		 ON CONFLICT (run_date) DO UPDATE SET sent_at = now(), status = EXCLUDED.status`,
		now.Format("2006-01-02"), status,
	)
	return err
}

// checkReminders looks for calendar events starting within the reminder lead
// window and notifies the owner once per event.
func (s *Scheduler) checkReminders(ctx context.Context) {
	if s.calendar == nil {
		return
	}

	now := time.Now().In(s.loc)
	windowEnd := now.Add(time.Duration(s.reminderLeadMin) * time.Minute)

	events, err := s.calendar.EventsBetween(ctx, now, windowEnd)
	if err != nil {
		s.log.Warn("scheduler: failed to fetch events for reminders", "err", err)
		return
	}

	for _, e := range events {
		sent, err := s.reminderAlreadySent(ctx, e.ID)
		if err != nil {
			s.log.Error("scheduler: reminder dedup check failed", "err", err)
			continue
		}
		if sent {
			continue
		}

		msg := fmt.Sprintf("Напоминание: \"%s\" в %s", e.Title, e.Start.In(s.loc).Format("15:04"))
		if err := s.sender.SendToOwner(msg); err != nil {
			s.log.Error("scheduler: failed to send reminder", "err", err)
			continue
		}
		if err := s.markReminderSent(ctx, e); err != nil {
			s.log.Error("scheduler: failed to record reminder", "err", err)
		}
	}
}

func (s *Scheduler) reminderAlreadySent(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM reminder_log WHERE event_id = $1)`,
		eventID,
	).Scan(&exists)
	return exists, err
}

func (s *Scheduler) markReminderSent(ctx context.Context, e calendar.Event) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reminder_log (event_id, event_start) VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING`,
		e.ID, e.Start,
	)
	return err
}
