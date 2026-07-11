// Package summary orchestrates building the daily briefing message from
// tasks and calendar data, then handing it to Claude for a readable writeup.
package summary

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"fahriddin-ai/internal/ai"
	"fahriddin-ai/internal/calendar"
	"fahriddin-ai/internal/tasks"
)

// Builder assembles the daily summary from all available data sources.
type Builder struct {
	tasks    *tasks.Store
	calendar *calendar.Client
	ai       *ai.Client
	loc      *time.Location
	log      *slog.Logger
}

func NewBuilder(taskStore *tasks.Store, cal *calendar.Client, aiClient *ai.Client, loc *time.Location, log *slog.Logger) *Builder {
	return &Builder{tasks: taskStore, calendar: cal, ai: aiClient, loc: loc, log: log}
}

// Generate always returns a usable message. If a data source or the AI call
// fails, it degrades gracefully instead of returning nothing.
func (b *Builder) Generate(ctx context.Context) string {
	now := time.Now().In(b.loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, b.loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	openTasks, err := b.tasks.ListOpen(ctx)
	if err != nil {
		b.log.Warn("summary: failed to load tasks", "err", err)
		openTasks = nil
	}

	var events []calendar.Event
	if b.calendar != nil {
		events, err = b.calendar.EventsBetween(ctx, dayStart, dayEnd)
		if err != nil {
			b.log.Warn("summary: failed to load calendar events", "err", err)
			events = nil
		}
	}

	raw := buildRawContext(now, openTasks, events)

	if b.ai != nil {
		text, err := b.ai.GenerateSummary(ctx, raw)
		if err == nil && strings.TrimSpace(text) != "" {
			return text
		}
		b.log.Warn("summary: claude generation failed, falling back to plain formatting", "err", err)
	}

	return plainFallback(now, openTasks, events)
}

func buildRawContext(now time.Time, openTasks []tasks.Task, events []calendar.Event) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Сегодня: %s\n\n", now.Format("2006-01-02 (Monday)")))

	sb.WriteString("Открытые задачи:\n")
	if len(openTasks) == 0 {
		sb.WriteString("- нет открытых задач\n")
	} else {
		for _, t := range openTasks {
			line := fmt.Sprintf("- [#%d] %s", t.ID, t.Description)
			if t.DueAt != nil {
				line += fmt.Sprintf(" (срок: %s)", t.DueAt.In(now.Location()).Format("02.01 15:04"))
			}
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\nСобытия календаря сегодня:\n")
	if len(events) == 0 {
		sb.WriteString("- событий нет\n")
	} else {
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", e.Start.In(now.Location()).Format("15:04"), e.Title))
		}
	}

	sb.WriteString("\nСоставь короткую дружелюбную сводку на русском на основе этих данных.")
	return sb.String()
}

func plainFallback(now time.Time, openTasks []tasks.Task, events []calendar.Event) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Сводка на %s\n\n", now.Format("02.01.2006")))

	sb.WriteString("Задачи:\n")
	if len(openTasks) == 0 {
		sb.WriteString("нет открытых задач\n")
	} else {
		for _, t := range openTasks {
			sb.WriteString(fmt.Sprintf("- #%d %s\n", t.ID, t.Description))
		}
	}

	sb.WriteString("\nКалендарь:\n")
	if len(events) == 0 {
		sb.WriteString("событий нет\n")
	} else {
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("- %s %s\n", e.Start.In(now.Location()).Format("15:04"), e.Title))
		}
	}

	return sb.String()
}
