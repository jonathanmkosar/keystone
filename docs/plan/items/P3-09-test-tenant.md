# P3-09. Test tenant

Status: done
Lane: core
Depends on: P2-01, P3-03
Owner gate: none
Scope: Invented fixtures for a made-up person: chats, files, images, a built-in knowledge store with sensitive and ordinary spaces, recorded model turns, use cases and expected results. Every fixture marked as test data. A loader used by rebuild. Later items add their own use cases.
Proof: `rebuild` creates the test tenant; adding a use case means adding a fixture and its expected result; the test-tenant check is added to verify.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

The owner's priority is a fresh checkout that can be used, so this pass builds the person, the sign-in, the chats, the recorded turns, the use cases and the loader. Files, images and the knowledge store have no code to hold them yet (P9-02 and P6-02), and their fixtures come with those items.

- Fixtures in `fixtures/test-tenant/`, every file marked `"test_data": true`:
  - `tenant.json`: the invented person's name and an invented password that says it is a test value.
  - `turns/*.json`: recorded model turns, as fake provider scripts, with a catch-all.
  - `chats/*.json`: a title, a star, and the person's messages only. The loader replays each message through the fake provider and records the reply, so a chat holds what the recorded turn says. One chat has a tool call and one has an edited message, so there is a branch to step through.
  - `cases/*.json`: use cases with their expected results. Three kinds today: a turn (ask, and what the reply holds), a search (query, and the chats found) and a chat (title, and what its current branch holds).
  - `fixture.go`: compiles the folder into the binary, so the server finds the scripts without the repo.
- A new package, `internal/testtenant`: `Read` and `Shipped` (the fixtures, checked), `Load` (the loader), `Find` and `IsTest` (which tenant is the test tenant), `Provider` (the fake provider with the test tenant's scripts) and `RunCases`.
- The mark: a row in the tenant's own `tenant_meta`, written when the loader makes the tenant. The name alone marks nothing. A decision record says why.
- Safe to run twice: the loader finds the marked tenant, sets the password only when the fixture's password does not sign in, and records each chat it made in `tenant_meta` with a hash of the fixtures, so a chat is made once and made again only when the fixtures change. Chats the person made by hand are never touched.
- `rebuild` gets a step, `the test tenant`, after the data folder. A setting, `test_tenant.load`, switches it off for an install that should not hold one.
- `go run ./tools/tenant test` loads it into any data folder.
- A new standing check in `verify`, `test tenant`: in a throwaway data folder, beside an ordinary tenant, the test tenant loads twice with the same result, signs in with the fixture's password, is the only tenant marked, every fixture file is marked as test data, and every use case gives its expected result.
- Proof: `rebuild` creates the test tenant on a fresh checkout; a use case is one entry in a file under `cases/`; `verify` has the `test tenant` check.
- Docs: `docs/architecture/test-tenant.md`, a row in the overview, a decision record on the mark and the published password, `tools.md`, the fixture README.

### Result

Done on November 24, 2026.

- `fixtures/test-tenant/` holds Test Person One: `tenant.json` with the name and an invented password, four files of recorded turns, three chats and seven use cases. Every file is marked `"test_data": true`.
- `internal/testtenant` holds the loader. `Load` is safe to run twice: it finds the tenant by its mark, leaves a password that works alone, and makes only the chats that are missing or whose fixtures changed.
- The mark is the row `test_tenant` in the tenant's own `tenant_meta`. `IsTest(store)` reads it and `Find(install)` looks for it. A person with the same name is not the test tenant. Decision 0080.
- A chat fixture holds the person's messages only, and the loader plays them through the recorded turns. `Provider` returns the fake provider with those turns.
- `rebuild` has the step `the test tenant`. `go run ./tools/tenant test` does the same by hand. `test_tenant.load` switches the step off.
- Proof: `go run ./tools/rebuild` on a checkout with no data folder made the test tenant with three chats and was green. A use case is one entry in a file under `fixtures/test-tenant/cases/`. `verify` has the new standing check `test tenant` and is 13 of 13.
- Short of the scope: no files, no images and no knowledge store. Uploads are P9-02 and the built-in store is P6-02, and each adds its fixtures, a step in `Load` and a kind of use case.
- Not joined yet: nothing gives the test tenant the fake provider on a route. P3-02 and P4-03 do that with `IsTest` and `Provider`.
- One question for the owner, in the journal entry: whether the install he uses every day should hold the test tenant.
- Doc: `docs/architecture/test-tenant.md`. Journal: `docs/journal/2026-11-24-core-009.md`.

