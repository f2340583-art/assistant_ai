// Package summary orchestrates building the daily briefing message from
// tasks and calendar data, then handing it to Claude for a readable writeup.
package summary

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
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

// Tile is one stat block on the Mini App dashboard, e.g. an open-task count
// or (later) a financial figure. Deliberately generic so new tile types
// (Phase 2: sales, Instagram reach, etc.) slot in without a frontend change.
type Tile struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Icon  string `json:"icon"`
}

// fetchData pulls the raw inputs (open tasks, today's calendar events) that
// both the narrative and the dashboard tiles are built from.
func (b *Builder) fetchData(ctx context.Context) (now time.Time, openTasks []tasks.Task, events []calendar.Event) {
	now = time.Now().In(b.loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, b.loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	var err error
	openTasks, err = b.tasks.ListOpen(ctx)
	if err != nil {
		b.log.Warn("summary: failed to load tasks", "err", err)
		openTasks = nil
	}

	if b.calendar != nil {
		events, err = b.calendar.EventsBetween(ctx, dayStart, dayEnd)
		if err != nil {
			b.log.Warn("summary: failed to load calendar events", "err", err)
			events = nil
		}
	}
	return now, openTasks, events
}

// Generate always returns a usable message. If a data source or the AI call
// fails, it degrades gracefully instead of returning nothing.
func (b *Builder) Generate(ctx context.Context) string {
	now, openTasks, events := b.fetchData(ctx)
	return b.narrative(ctx, now, openTasks, events)
}

// Tiles computes the dashboard stat blocks. Cheap (no AI call) so the Mini
// App can refresh them on every load.
func (b *Builder) Tiles(ctx context.Context) []Tile {
	now, openTasks, events := b.fetchData(ctx)
	return buildTiles(now, openTasks, events)
}

func (b *Builder) narrative(ctx context.Context, now time.Time, openTasks []tasks.Task, events []calendar.Event) string {
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

func buildTiles(now time.Time, openTasks []tasks.Task, events []calendar.Event) []Tile {
	tiles := []Tile{
		{Label: "Ochiq vazifalar", Value: strconv.Itoa(len(openTasks)), Icon: "tasks"},
		{Label: "Bugungi tadbirlar", Value: strconv.Itoa(len(events)), Icon: "calendar"},
	}

	overdue := 0
	for _, t := range openTasks {
		if t.DueAt != nil && t.DueAt.Before(now) {
			overdue++
		}
	}
	if overdue > 0 {
		tiles = append(tiles, Tile{Label: "Muddati o'tgan", Value: strconv.Itoa(overdue), Icon: "warning"})
	}

	if next := nextEvent(events, now); next != nil {
		tiles = append(tiles, Tile{
			Label: "Keyingi tadbir",
			Value: fmt.Sprintf("%s — %s", next.Start.In(now.Location()).Format("15:04"), next.Title),
			Icon:  "clock",
		})
	}

	return tiles
}

func nextEvent(events []calendar.Event, now time.Time) *calendar.Event {
	for i := range events {
		if events[i].Start.After(now) {
			return &events[i]
		}
	}
	return nil
}

func buildRawContext(now time.Time, openTasks []tasks.Task, events []calendar.Event) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Bugun: %s\n\n", now.Format("2006-01-02 (Monday)")))

	sb.WriteString("Ochiq vazifalar:\n")
	sb.WriteString(formatTasksGrouped(openTasks, now.Location()))

	sb.WriteString("\nBugungi taqvim tadbirlari:\n")
	if len(events) == 0 {
		sb.WriteString("- tadbirlar yo'q\n")
	} else {
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", e.Start.In(now.Location()).Format("15:04"), e.Title))
		}
	}

	sb.WriteString("\nShu ma'lumotlar asosida qisqa, samimiy xulosa tuz (o'zbek tilida, lotin alifbosida).")
	return sb.String()
}

func plainFallback(now time.Time, openTasks []tasks.Task, events []calendar.Event) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s uchun xulosa\n\n", now.Format("02.01.2006")))

	sb.WriteString("Vazifalar:\n")
	sb.WriteString(formatTasksGrouped(openTasks, now.Location()))

	sb.WriteString("\nTaqvim:\n")
	if len(events) == 0 {
		sb.WriteString("tadbirlar yo'q\n")
	} else {
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("- %s %s\n", e.Start.In(now.Location()).Format("15:04"), e.Title))
		}
	}

	return sb.String()
}

// formatTasksGrouped renders open tasks with a due time first, sorted
// chronologically and grouped under an hour heading whenever the hour
// changes, followed by tasks with no due time under a separate heading.
// Shared by buildRawContext (fed to Claude) and plainFallback (shipped
// as-is when Claude/network fails), so both need correctly organized
// input — plainFallback has no AI massaging to fix it up afterward.
func formatTasksGrouped(openTasks []tasks.Task, loc *time.Location) string {
	if len(openTasks) == 0 {
		return "- ochiq vazifalar yo'q\n"
	}

	var timed, untimed []tasks.Task
	for _, t := range openTasks {
		if t.DueAt != nil {
			timed = append(timed, t)
		} else {
			untimed = append(untimed, t)
		}
	}
	sort.SliceStable(timed, func(i, j int) bool { return timed[i].DueAt.Before(*timed[j].DueAt) })

	var sb strings.Builder
	lastHour := ""
	for _, t := range timed {
		hour := t.DueAt.In(loc).Format("02.01 15:00")
		if hour != lastHour {
			sb.WriteString(hour + ":\n")
			lastHour = hour
		}
		sb.WriteString(fmt.Sprintf("- [#%d] %s\n", t.ID, t.Description))
	}

	if len(untimed) > 0 {
		sb.WriteString("Vaqti belgilanmagan vazifalar:\n")
		for _, t := range untimed {
			sb.WriteString(fmt.Sprintf("- [#%d] %s\n", t.ID, t.Description))
		}
	}
	return sb.String()
}
