-- A tenant's own values for settings in the registry. A setting with no row
-- here has the registry's default. Keys under rules. are the four fixed
-- rules, which are not settings, so no row may name one.
-- internal/tenantsettings owns this table.
CREATE TABLE tenant_settings (
    key        TEXT PRIMARY KEY CHECK (key NOT GLOB 'rules.*'),
    value      TEXT NOT NULL,
    set_by     TEXT NOT NULL CHECK (set_by IN ('person', 'admin')),
    updated_at TEXT NOT NULL
) STRICT;
