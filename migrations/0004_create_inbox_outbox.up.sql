CREATE TABLE inbox_messages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    consumer_name  TEXT NOT NULL,
    message_id     TEXT NOT NULL,
    message_hash   TEXT NOT NULL,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,

    CONSTRAINT inbox_consumer_message_unique UNIQUE (consumer_name, message_id)
);

CREATE INDEX idx_inbox_pending ON inbox_messages (received_at) WHERE completed_at IS NULL;

CREATE TABLE outbox_events (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id     UUID NOT NULL,
    event_type       TEXT NOT NULL,
    payload          JSONB NOT NULL,
    correlation_id   UUID,
    causation_id     UUID,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts         INT NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at     TIMESTAMPTZ,
    locked_by        TEXT,
    locked_at        TIMESTAMPTZ,

    CONSTRAINT outbox_attempts_non_negative CHECK (attempts >= 0)
);

CREATE INDEX idx_outbox_pending ON outbox_events (next_attempt_at)
    WHERE published_at IS NULL;

CREATE INDEX idx_outbox_aggregate_id ON outbox_events (aggregate_id);