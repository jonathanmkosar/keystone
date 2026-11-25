# 0050. A model is a row in the tenant's database, holding the entry as checked JSON

November 24, 2026 · decided in P3-02

## The question

The plan says adding a model is a settings change. The settings registry holds one text value for one key, and a model is a dozen fields that belong together. Where does a tenant's list of models live, and in what shape?

## The decision

**A table of its own.** Each tenant's database has a `models` table, added by its own migration. `internal/tenantsettings` said this in advance: structured data a tenant holds does not go in `tenant_settings`.

**One row for each model, with the entry as one JSON value.** The row has the name, the entry and the time it last changed. There is no column for each field.

**Checked on the way in and on the way out.** `models.Check` runs before an entry is stored and again each time it is read. A row that fails is refused by `Get` and shown with its reason by `List`. Being in the table earns an entry nothing.

"A settings change" is read as: a change to stored data, made by the person or the admin, with no change to code and no rebuild.

## Why

- A setting per field, such as `models.example.endpoint`, would need keys that are not in the registry until someone adds a model. The registry is compiled in, and every key in it has a default and a screen.
- One long text setting holding a JSON list would be one value for all models. Two changes at once would lose one, and one bad entry would spoil the list.
- The three real adapters are not built, and each will want a field or two of its own: a certificate for the gateway, a version header. With the entry as JSON, a new field is a change to one struct and to `Check`. With columns it is a migration each time, in a folder another lane owns.
- Checking on read matters because of fixed rule 3. The class decides what a model may be sent. If a row were trusted because it is stored, anything that can write the table could mark a hosted model local. Read through `Check`, such a row is refused.

## What it costs

- The database cannot answer "which models are local" with a query. The registry is small, 50 entries at most by default, so `Usable` reads them all.
- When `Check` gets stricter, a stored entry can stop being usable. That is meant. The row is kept and listed with the reason, so it can be put right.
- The migration file sits in `internal/tenant/migrations`, because that is where the binary reads migrations from. The table belongs to `internal/models`.
