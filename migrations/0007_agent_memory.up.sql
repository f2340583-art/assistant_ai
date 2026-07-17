-- Short-term conversational memory for the tool-use agent (internal/agent),
-- keyed per Telegram chat_id so the two owners never share/collide history.
-- Only plain user/assistant TEXT turns are stored — no raw tool_use/tool_result
-- JSON — each new message starts a fresh tool-dispatch loop seeded with prior
-- plain-text context, not a replayed tool-call transcript.
CREATE TABLE agent_conversations (
    id         BIGSERIAL PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_conversations_chat_id_idx ON agent_conversations (chat_id, id);

-- "Self-learning" log: every agent invocation is recorded here (query, which
-- tools fired, success/failure) purely for manual developer review later —
-- no autonomous behavior reads from this table.
CREATE TABLE agent_query_log (
    id         BIGSERIAL PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    query_text TEXT NOT NULL,
    tools_used TEXT,       -- comma-joined tool names, NULL if none were called
    success    BOOLEAN NOT NULL,
    error_text TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_query_log_created_at_idx ON agent_query_log (created_at);
