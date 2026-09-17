ALTER TABLE wager_transactions
    DROP CONSTRAINT IF EXISTS wager_tx_reference_retry_attempts_non_negative;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS reference_next_retry_at,
    DROP COLUMN IF EXISTS reference_retry_attempts;

DROP INDEX IF EXISTS idx_wager_tx_idempotency_key;
