# 0190. The settings routes act as the person, in a package of their own

November 25, 2026 · decided in P11-01

## The question

The settings screens need routes for four things that already have packages: settings (`internal/tenantsettings`), secrets (the vault in the tenant store), models (`internal/models`) and the profile (`internal/knowledge/personmap`). Where do the routes go, who do they act as when there is no admin role yet, and what may a secrets route return?

## The decision

**The routes are one new package, `internal/settingsapi`.** It reads the two settings the packages underneath need, `models.max_per_tenant` and `knowledge.profile_max_chars`, and passes the values in.

**Every route acts as the person, on the session's own tenant.** A setting is changed through `tenantsettings` with the role `person`, so only a setting with the `tenant` scope can be changed. A person adds, changes and removes their own models.

**Every setting is listed, and the fixed rules are listed apart.** The list holds the settings a person may not change too, marked as such. The four fixed rules are in a list of their own with no value. A write to a rule's key is refused with a code of its own.

**A secret's value goes in and never comes out.** There is no route that returns one. A refusal on these routes never repeats what it was sent.

## Why

- The tenant store, the audit log, the model registry and the profile each guard a fixed rule, and the `fixed rules` check fails if one of them imports the settings. A route in one of those packages would have to read a setting there. A package above them can.
- One package for the four keeps what the settings screens call in one place, with one doc and one test file, and adds one line to `internal/app/app.go`.
- With no admin role, "the person, on their own tenant" is the one caller the API can be sure of. `tenantsettings` already says what that caller may change, so the routes add no rule of their own.
- A person who can see that a setting exists and is the admin's to change knows who to ask. None of the settings holds a secret: a key is a secret in the vault, and a setting only names it.
- A rule shown beside the settings, with no control, says that it exists and cannot be switched off. A rule left off the screen says nothing.
- A key that can be read back can leak from the page, from a log of the answer, or to whoever sits down at an open browser. A key that is wrong is stored again, which costs a paste.

## What it costs

- A person can mark their own model `trusted`, which lets it read their sensitive spaces. The owner's open question on this recommends the admin only. When there is an admin role, the three routes that write a model get a check, and this record is revisited.
- A key cannot be checked by eye after it is stored. The screen can say that a model's key is stored. It cannot say that it is the right one until a chat uses it.
- The name of a secret's shape and the vault's size limit are written twice, here and in `internal/secrets`, so a refusal can be told from a failure. If the vault's rule changes, the vault still decides, and this package's message may be out of date.
- An `install` setting is shown with its default, because nothing stores an admin's change yet. When something does, the list must read it.
