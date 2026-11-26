-- Operations and Monitor (P11-03): where each of a person's limits stood
-- at each snapshot, so a limit has a trend. See
-- docs/architecture/operations.md. internal/operations owns this table.
--
-- key names the limit: cap, memory_reserve, disk, database, files or
-- artifacts. used and of are in the limit's unit: millionths of a US
-- dollar for the cap and the reserve, bytes for the rest. at is UTC, as
-- text that sorts in the order of time. The disk is the volume that holds
-- the data folder, the same for every person; it is kept here, in each
-- person's own database, so nothing is kept outside a tenant's folder.
-- Old rows are removed after operations.snapshot_keep.
CREATE TABLE operations_snapshots (
    at   TEXT    NOT NULL,
    key  TEXT    NOT NULL CHECK (key <> ''),
    used INTEGER NOT NULL,
    of   INTEGER NOT NULL,
    PRIMARY KEY (key, at)
) STRICT, WITHOUT ROWID;

CREATE INDEX operations_snapshots_at ON operations_snapshots(at);
