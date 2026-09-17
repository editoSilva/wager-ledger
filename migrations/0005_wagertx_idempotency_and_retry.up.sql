CREATE INDEX idx_wager_tx_idempotency_key
    ON wager_transactions (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

ALTER TABLE wager_transactions
    ADD COLUMN reference_retry_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN reference_next_retry_at TIMESTAMPTZ;

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_tx_reference_retry_attempts_non_negative
        CHECK (reference_retry_attempts >= 0);
