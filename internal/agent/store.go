package agent

import (
	"context"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// loadHistory reads the last limit turns for chatID (oldest first) and
// converts them into plain user/assistant text messages — no raw
// tool_use/tool_result JSON is replayed, since each new message starts a
// fresh tool-dispatch loop seeded with prior plain-text context, not a
// replayed tool-call transcript.
func (a *Agent) loadHistory(ctx context.Context, chatID int64, limit int) ([]anthropic.MessageParam, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT role, content FROM (
			SELECT role, content, id FROM agent_conversations
			WHERE chat_id = $1 ORDER BY id DESC LIMIT $2
		) recent ORDER BY id ASC`,
		chatID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []anthropic.MessageParam
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return nil, err
		}
		if role == "assistant" {
			history = append(history, anthropic.NewAssistantMessage(anthropic.NewTextBlock(content)))
		} else {
			history = append(history, anthropic.NewUserMessage(anthropic.NewTextBlock(content)))
		}
	}
	return history, rows.Err()
}

func (a *Agent) saveTurn(ctx context.Context, chatID int64, role, content string) error {
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO agent_conversations (chat_id, role, content) VALUES ($1, $2, $3)`,
		chatID, role, content,
	)
	return err
}

// logQuery records one agent invocation for later manual review — not
// read by any autonomous behavior, purely a developer-facing log of what
// users actually asked for and whether the agent could handle it.
func (a *Agent) logQuery(ctx context.Context, chatID int64, queryText string, toolsUsed []string, success bool, errText string) {
	var toolsCol *string
	if len(toolsUsed) > 0 {
		joined := strings.Join(toolsUsed, ",")
		toolsCol = &joined
	}
	var errCol *string
	if errText != "" {
		errCol = &errText
	}
	if _, err := a.db.ExecContext(ctx,
		`INSERT INTO agent_query_log (chat_id, query_text, tools_used, success, error_text) VALUES ($1, $2, $3, $4, $5)`,
		chatID, queryText, toolsCol, success, errCol,
	); err != nil {
		a.log.Warn("agent: log query failed", "err", err)
	}
}
