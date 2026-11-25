-- The first tables in a tenant's database. tenant_meta says whose database
-- this is. Later items add their own tables in their own migrations.
CREATE TABLE tenant_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
