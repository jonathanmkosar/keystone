-- Minimal data.db schema used by the core test suite only.
-- Mirrors the shape tests expect (accounts, transactions, budgets, documents,
-- document_chunks, semantic_index). Do not install as a real stone.

CREATE TABLE IF NOT EXISTS accounts (
    id            TEXT PRIMARY KEY,
    institution   TEXT NOT NULL,
    name          TEXT NOT NULL,
    type          TEXT,
    currency      TEXT DEFAULT 'USD',
    balance       NUMERIC,
    last_synced   INTEGER
);

CREATE TABLE IF NOT EXISTS transactions (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    posted      INTEGER NOT NULL,
    amount      NUMERIC NOT NULL,
    payee       TEXT,
    category    TEXT,
    memo        TEXT,
    raw         JSON
);

CREATE INDEX IF NOT EXISTS idx_tx_account_posted ON transactions(account_id, posted DESC);
CREATE INDEX IF NOT EXISTS idx_tx_category       ON transactions(category);
CREATE INDEX IF NOT EXISTS idx_tx_posted         ON transactions(posted DESC);

CREATE TABLE IF NOT EXISTS budgets (
    category TEXT NOT NULL,
    month    INTEGER NOT NULL,
    year     INTEGER NOT NULL,
    amount   NUMERIC NOT NULL,
    PRIMARY KEY (category, month, year)
);

CREATE TABLE IF NOT EXISTS documents (
    source_ref   TEXT PRIMARY KEY,
    kind         TEXT,
    year         INTEGER,
    ingested_at  INTEGER NOT NULL,
    content      TEXT,
    redacted     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS document_chunks (
    source_ref   TEXT NOT NULL REFERENCES documents(source_ref) ON DELETE CASCADE,
    chunk_index  INTEGER NOT NULL,
    content      TEXT NOT NULL,
    PRIMARY KEY (source_ref, chunk_index)
);

CREATE VIRTUAL TABLE IF NOT EXISTS semantic_index USING vec0(
    embedding    float[1536],
    +source_type TEXT,
    +source_ref  TEXT
);
