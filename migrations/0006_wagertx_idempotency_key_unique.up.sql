CREATE UNIQUE INDEX wager_tx_idempotency_key_unique
    ON wager_transactions (idempotency_key)
    WHERE idempotency_key IS NOT NULL;
