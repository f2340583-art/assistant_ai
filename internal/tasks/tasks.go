// Package tasks implements the built-in task tracker.
package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Task is a single to-do item.
type Task struct {
	ID          int64
	Description string
	DueAt       *time.Time
	Done        bool
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// Store persists tasks in Postgres.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Add creates a new open task and returns its ID.
func (s *Store) Add(ctx context.Context, description string, dueAt *time.Time) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO tasks (description, due_at) VALUES ($1, $2) RETURNING id`,
		description, dueAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("add task: %w", err)
	}
	return id, nil
}

// ListOpen returns all tasks that are not yet done, oldest first.
func (s *Store) ListOpen(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, due_at, done, created_at, completed_at
		 FROM tasks WHERE done = FALSE ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list open tasks: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Description, &t.DueAt, &t.Done, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Complete marks a task as done. It returns sql.ErrNoRows if the task
// doesn't exist or is already done.
func (s *Store) Complete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET done = TRUE, completed_at = now() WHERE id = $1 AND done = FALSE`,
		id,
	)
	if err != nil {
		return fmt.Errorf("complete task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete task: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Delete permanently removes a task (e.g. one added by mistake), unlike
// Complete which just marks it done. Returns sql.ErrNoRows if the task
// doesn't exist.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
