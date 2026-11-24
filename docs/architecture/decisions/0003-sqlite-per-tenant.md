# 0003. One SQLite database for each tenant, through modernc.org/sqlite

November 24, 2026 · decided in P0-03

## The question

Where does a tenant's data live, and which driver reads it?

## The decision

Each tenant has its own folder, and in it one SQLite database, `keystone.db`. It is the tenant's source of truth: chats, messages, branches, summaries, projects, settings, the model registry, jobs, schedules, usage and the audit log. There is no shared database that holds rows from more than one tenant.

The driver is `modernc.org/sqlite`, the same one Waymarks uses (its decision 0004). It is SQLite translated to Go, so it needs no C compiler. Full-text search uses SQLite's own FTS5.

Migrations are files named by date and time. They move forward only, and each tenant's database records which ones it has applied.

## Why

- Isolation by construction. A session opens one tenant's folder, so a query cannot return another tenant's rows, whatever the query says. This is the first of the four fixed rules.
- A backup of one tenant is a copy of its folder.
- With no C compiler, `go build` works the same on Windows, macOS and Linux, and the binary cross-compiles.
- Following Waymarks means one driver to learn and one set of habits.

## What it costs

- The translated driver is slower than the C library for heavy writes. One person's chats are far below where that matters.
- Nothing can be asked across tenants in one query. Operations pages that total across tenants read each database in turn.
- Each open tenant holds its own file handles. Databases are opened when a session needs them.
- `modernc.org/sqlite` is the first outside module. It is added in P2-01, on a machine that can download modules.
