CREATE TABLE wager_transactions (
    id                                  UUID PRIMARY KEY,
    kind                                TEXT NOT NULL,
    status                              TEXT NOT NULL,
    wallet_id                           UUID NOT NULL REFERENCES wallets (id),
    player_id                           UUID NOT NULL,
    amount_minor_units                  BIGINT NOT NULL,
    currency                            CHAR(3) NOT NULL,
    
    provider_id                         TEXT,
    external_transaction_id             TEXT,
    idempotency_key                     TEXT,
    payload_hash                        TEXT,
    round_id                            TEXT,
    game_id                             TEXT,
    reference_external_transaction_id   TEXT,

    resolved_reference_id               UUID REFERENCES wager_transactions (id),
    failure_code                        TEXT,
    financial_result_minor_units        BIGINT,
    financial_result_currency           CHAR(3),

    created_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wager_tx_kind_valid CHECK (
        kind IN ('BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK', 'OPENING')
    ),
    CONSTRAINT wager_tx_status_valid CHECK (
        status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')
    ),
    CONSTRAINT wager_tx_currency_format CHECK (currency ~ '^[A-Z]{3}$'),

    CONSTRAINT wager_tx_amount_by_kind CHECK (
        (kind = 'LOSS' AND amount_minor_units = 0)
        OR (kind <> 'LOSS' AND amount_minor_units > 0)
    ),

  
    CONSTRAINT wager_tx_kind_fields CHECK (
        (
            kind = 'OPENING'
            AND provider_id IS NULL
            AND external_transaction_id IS NULL
            AND idempotency_key IS NULL
            AND payload_hash IS NULL
            AND round_id IS NULL
            AND game_id IS NULL
            AND reference_external_transaction_id IS NULL
        )
        OR (
            kind <> 'OPENING'
            AND provider_id IS NOT NULL
            AND external_transaction_id IS NOT NULL
            AND idempotency_key IS NOT NULL
            AND payload_hash IS NOT NULL
            AND round_id IS NOT NULL
            AND game_id IS NOT NULL
        )
    ),


    CONSTRAINT wager_tx_reference_by_kind CHECK (
        (kind IN ('REFUND', 'ROLLBACK') AND reference_external_transaction_id IS NOT NULL)
        OR (kind NOT IN ('REFUND', 'ROLLBACK') AND reference_external_transaction_id IS NULL)
    ),

    CONSTRAINT wager_tx_provider_external_unique UNIQUE (provider_id, external_transaction_id)
);

CREATE UNIQUE INDEX wager_tx_one_opening_per_wallet
    ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';

CREATE INDEX idx_wager_tx_wallet_id ON wager_transactions (wallet_id);
CREATE INDEX idx_wager_tx_provider_id ON wager_transactions (provider_id) WHERE provider_id IS NOT NULL;
CREATE INDEX idx_wager_tx_status_pending_ref ON wager_transactions (status) WHERE status = 'PENDING_REFERENCE';