-- A member's conversations with the assistant that acts for them in this project (ADR-0024). A
-- conversation belongs to its member, and at most one of a member's conversations runs at a time. Its
-- worker is empty until it is placed and its run lane until its first start; each start names a run lane
-- of its own, so a worker's journal never sees one twice.
CREATE TABLE assistant_conversations (
  workspace_id       TEXT   NOT NULL,
  id                 TEXT   NOT NULL,
  member_id          TEXT   NOT NULL,
  name               TEXT   NOT NULL,
  worker_id          TEXT,
  desired_state      TEXT   NOT NULL,
  actual_state       TEXT   NOT NULL,
  run_lane           TEXT,
  current_generation BIGINT NOT NULL,
  stopped_reason     TEXT,
  created_at         BIGINT NOT NULL,
  last_active_at     BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT conversation_name CHECK (name <> ''),
  CONSTRAINT conversation_desired CHECK (desired_state IN ('running', 'stopped')),
  CONSTRAINT conversation_actual CHECK (actual_state IN ('starting', 'running', 'working', 'stopping', 'stopped', 'failed'))
);

CREATE INDEX assistant_conversations_member ON assistant_conversations (workspace_id, member_id, last_active_at);
CREATE UNIQUE INDEX assistant_conversations_running ON assistant_conversations (workspace_id, member_id) WHERE desired_state = 'running';
CREATE INDEX assistant_conversations_run_lane ON assistant_conversations (workspace_id, run_lane);

-- What a conversation holds, in order. A body is the text of a member's message or a reply, and lives
-- only here and in the messages table, so deleting the conversation removes it; the entry's event names
-- its conversation, ID, seq, and kind and nothing it says.
CREATE TABLE conversation_entries (
  workspace_id    TEXT   NOT NULL,
  conversation_id TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  seq             BIGINT NOT NULL,
  kind            TEXT   NOT NULL,
  body            TEXT,
  data            TEXT,
  before_clear    BIGINT NOT NULL DEFAULT 0,
  recorded_at     BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, conversation_id, id),
  CONSTRAINT entry_kind CHECK (kind IN ('member', 'reply', 'action', 'marker')),
  CONSTRAINT entry_seq CHECK (seq >= 1),
  CONSTRAINT entry_before_clear CHECK (before_clear IN (0, 1))
);

CREATE UNIQUE INDEX conversation_entries_seq ON conversation_entries (workspace_id, conversation_id, seq);

-- A message's target kind: a lane, a worker's orchestrator, or the assistant of a conversation. to_lane
-- holds the target's ID for every kind: a lane's ID, a worker's ID, or a conversation's ID.
ALTER TABLE messages ADD COLUMN to_kind TEXT NOT NULL DEFAULT 'lane';

CREATE INDEX messages_target ON messages (workspace_id, to_kind, to_lane, state);
