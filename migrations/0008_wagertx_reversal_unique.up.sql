CREATE UNIQUE INDEX wager_tx_successful_reversal_reference_unique
    ON wager_transactions (resolved_reference_id)
    WHERE kind IN ('REFUND', 'ROLLBACK') AND status = 'PROCESSED';
