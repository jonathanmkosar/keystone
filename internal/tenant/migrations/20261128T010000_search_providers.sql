-- Search providers and the fallback (P7-06): a person's own search
-- providers in their order, each one's state, its searches and cost by
-- month, and what has been moved (the Tavily key of P7-03 becoming the
-- first own provider). Keys are not here: they stay in the vault, and an
-- entry names its key. The install's providers have the same tables in
-- data/admin/search.db (internal/tools/web/store.go).
CREATE TABLE search_providers (
	name          TEXT PRIMARY KEY,
	position      INTEGER NOT NULL,
	kind          TEXT NOT NULL,
	endpoint      TEXT NOT NULL DEFAULT '',
	key_name      TEXT NOT NULL DEFAULT '',
	monthly_quota INTEGER NOT NULL DEFAULT 0 CHECK (monthly_quota >= 0),
	free_quota    INTEGER NOT NULL DEFAULT 0 CHECK (free_quota >= 0),
	resets        TEXT NOT NULL DEFAULT '',
	cost_micros   INTEGER NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
	enabled       INTEGER NOT NULL DEFAULT 1,
	added_at      TEXT NOT NULL
);

CREATE TABLE search_states (
	name  TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	data  TEXT NOT NULL
);

CREATE TABLE search_counts (
	name        TEXT NOT NULL,
	month       TEXT NOT NULL,
	searches    INTEGER NOT NULL DEFAULT 0,
	cost_micros INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (name, month)
);

CREATE TABLE search_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
