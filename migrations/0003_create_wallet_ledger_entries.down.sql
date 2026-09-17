DROP TRIGGER IF EXISTS trg_wallet_ledger_entries_immutable ON wallet_ledger_entries;
DROP FUNCTION IF EXISTS reject_ledger_mutation();
DROP TABLE IF EXISTS wallet_ledger_entries;