ALTER TABLE wager_transactions
    DROP CONSTRAINT wager_tx_reference_by_kind;

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_tx_reference_by_kind CHECK (
        (kind IN ('REFUND', 'ROLLBACK') AND reference_external_transaction_id IS NOT NULL)
        OR (kind = 'WIN')
        OR (kind NOT IN ('REFUND', 'ROLLBACK', 'WIN') AND reference_external_transaction_id IS NULL)
    );
