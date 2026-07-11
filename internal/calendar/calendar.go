// Package calendar reads events from Google Calendar via a service account.
package calendar

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// Event is a simplified calendar event used throughout the agent.
type Event struct {
	ID       string
	Title    string
	Start    time.Time
	End      time.Time
	Location string
}

// Client reads events from a single Google Calendar.
type Client struct {
	svc        *calendar.Service
	calendarID string
}

// NewClient builds a calendar client from service-account JSON credentials.
func NewClient(ctx context.Context, serviceAccountJSON, calendarID string) (*Client, error) {
	svc, err := calendar.NewService(ctx, option.WithCredentialsJSON([]byte(serviceAccountJSON)))
	if err != nil {
		return nil, fmt.Errorf("create calendar service: %w", err)
	}
	return &Client{svc: svc, calendarID: calendarID}, nil
}

// EventsBetween returns events starting in [from, to), ordered by start time.
// The caller's calendar must be shared with the service account's email
// (found in the "client_email" field of the service account JSON).
func (c *Client) EventsBetween(ctx context.Context, from, to time.Time) ([]Event, error) {
	call := c.svc.Events.List(c.calendarID).
		Context(ctx).
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime")

	resp, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	out := make([]Event, 0, len(resp.Items))
	for _, item := range resp.Items {
		start, err := parseEventTime(item.Start)
		if err != nil {
			continue
		}
		end, err := parseEventTime(item.End)
		if err != nil {
			end = start
		}
		out = append(out, Event{
			ID:       item.Id,
			Title:    item.Summary,
			Start:    start,
			End:      end,
			Location: item.Location,
		})
	}
	return out, nil
}

func parseEventTime(t *calendar.EventDateTime) (time.Time, error) {
	if t == nil {
		return time.Time{}, fmt.Errorf("nil event time")
	}
	if t.DateTime != "" {
		return time.Parse(time.RFC3339, t.DateTime)
	}
	if t.Date != "" {
		return time.Parse("2006-01-02", t.Date)
	}
	return time.Time{}, fmt.Errorf("event has neither dateTime nor date")
}
