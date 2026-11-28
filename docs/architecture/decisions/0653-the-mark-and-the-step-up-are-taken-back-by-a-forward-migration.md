# 0653. The mark and the step-up are removed, and a forward migration takes back what the step-up added

November 27, 2026 · decided in P15-01 · replaces decision [0641](0641-the-mark-is-read-from-disk-at-each-admin-call.md)

## The question

The first pass is merged and its two migrations have run on the owner's live install: `20261127T160000_admin_step_up.sql` added `admin_until` to a person's sessions, and `20261127T160100_step_up_challenges.sql` widened the passkey prompts' purposes with `step-up`. His `keystone.json` may hold `"admin": true` on a tenant. How is all of that taken away without breaking an install that has it?

## The decision

- **Migrations are never edited or deleted once they have run.** The two stay as they are. A new one, `20261127T170000_admin_step_up_removed.sql`, drops `admin_until` and makes the prompts table again with the two purposes it had before, carrying over the prompts in flight and dropping any `step-up` prompt, which nothing can answer now.
- **The mark goes from the code.** `tenant.Tenant` has no `Admin` field, `Install.SetAdmin`, `IsAdmin` and `Marks` are gone, `save` is one function again, and the backup no longer zeroes a mark. `internal/tenant` is as it was before the first pass, with `Dir()` added in a file of its own.
- **An old file still loads.** `keystone.json` is read with `encoding/json`, which passes over a field it does not know, so a tenant with `"admin": true` loads, and the next save of the registry drops the field. A backup made by the first pass's binary, whose manifest says `"admin": false`, restores the same way. A test holds both.
- **`tools/tenant admin`** is gone, with its `ADMIN` column in `list`. The admin isolation check fails if `tenant.Tenant` ever gains a field named for an admin again.
- **`last_sign_in` stays** in each person's `tenant_meta` (decision 0646): it is a number the admin's list of people shows. `signin.LastSignIn` moved to a file of its own.

## Why

- A migration that ran is history on someone's disk; changing it would leave installs that ran the old text and installs that ran the new one with different schemas under one name.
- Nothing reads a mark any more, so a mark left in the file would be a value that looks like it means something and does not. Dropping it on the next save is quiet and needs no step from the owner.

## What it costs

- The schema history holds three admin migrations where one would do. They are short and say what they are.
- Until something saves the registry, an old `keystone.json` keeps the dead field; it is read by nothing.
