-- A SearXNG search provider marked trusted (P7-07): its endpoint may be
-- called though it is not on this machine or the home network. Only an
-- install entry may be marked, so a person's own are always 0; the column
-- is here because a person's providers and the install's are read by the
-- same code. data/admin/search.db gains the same column when it is opened
-- (internal/tools/web/store_trusted.go).
ALTER TABLE search_providers ADD COLUMN trusted INTEGER NOT NULL DEFAULT 0;
