
CREATE TABLE wallets (
    id                   UUID PRIMARY KEY,
    player_id            UUID NOT NULL,
    currency             CHAR(3) NOT NULL,
    balance_minor_units  BIGINT NOT NULL,
    version              BIGINT NOT NULL DEFAULT 1,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallets_balance_non_negative CHECK (balance_minor_units >= 0),
    CONSTRAINT wallets_version_positive CHECK (version >= 1),
    CONSTRAINT wallets_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT wallets_player_currency_unique UNIQUE (player_id, currency)
);

CREATE INDEX idx_wallets_player_id ON wallets (player_id);