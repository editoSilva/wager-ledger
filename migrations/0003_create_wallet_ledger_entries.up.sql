CREATE TABLE wallet_ledger_entries (
    id                         UUID PRIMARY KEY,
    wallet_id                  UUID NOT NULL REFERENCES wallets (id),
    transaction_id             UUID NOT NULL REFERENCES wager_transactions (id),
    direction                  TEXT NOT NULL,
    amount_minor_units         BIGINT NOT NULL,
    currency                   CHAR(3) NOT NULL,
    balance_before_minor_units BIGINT NOT NULL,
    balance_after_minor_units  BIGINT NOT NULL,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ledger_direction_valid CHECK (direction IN ('DEBIT', 'CREDIT')),
    CONSTRAINT ledger_amount_positive CHECK (amount_minor_units > 0),
    CONSTRAINT ledger_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ledger_balance_after_non_negative CHECK (balance_after_minor_units >= 0),

    CONSTRAINT ledger_balance_math CHECK (
        (direction = 'DEBIT' AND balance_after_minor_units = balance_before_minor_units - amount_minor_units)
        OR (direction = 'CREDIT' AND balance_after_minor_units = balance_before_minor_units + amount_minor_units)
    ),

    CONSTRAINT ledger_wallet_transaction_unique UNIQUE (wallet_id, transaction_id)
);

CREATE INDEX idx_ledger_wallet_id_created_at ON wallet_ledger_entries (wallet_id, created_at);

CREATE OR REPLACE FUNCTION reject_ledger_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries é append-only: % não é permitido (linha id=%)',
        TG_OP,
        COALESCE(OLD.id, NEW.id);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_wallet_ledger_entries_immutable
    BEFORE UPDATE OR DELETE ON wallet_ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION reject_ledger_mutation();