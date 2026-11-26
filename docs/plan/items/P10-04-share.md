# P10-04. Share a chat

Status: provisional
Lane: chats + interface
Depends on: P4-03
Owner gate: none
Scope: A read-only link to one chat, revocable.
Proof: A shared link shows only that chat; a revoked link is refused.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (interface lane, November 26, 2026)

Branch `interface/p10-04-share`, cut from `reboot` at c91b356. New code goes in new files; every line changed in a shared file is listed in the journal.

1. **The rules, a new package `internal/share`**, a guard for fixed rules 1 and 3 that imports no setting (so not `internal/chat`, `internal/api` or `internal/uploads`, which reach the registry). A link is a row in `share_links` in the chat's own tenant database (migration `20261126T004100_share_links.sql`, gone with the chat by `ON DELETE CASCADE`). Its token is `ksh_`, the tenant's id, `_` and 32 random bytes as hex, the shape of the session token (decision 0007): the database keeps its SHA-256 only, the token is shown once when the link is made, and `audit.Scrub` learns its shape (one line) so no log holds it. `Resolve(install, token)` reads the tenant from the token, opens that one store and looks for the hash there, as `signin.Resolve` does; a forged or re-addressed token finds nothing. Making a link is refused for a chat held for sensitive content (`recall.Held`), an incognito chat, an empty chat, and past `share.max_links` open links; a link whose chat became held or incognito later is refused when it is opened, with a reason. `View` builds what a link shows from the record: the current branch now (`live`, the default), or the branch as it was when the link was made (`snapshot`, the message it ended at is kept); the person's words, images and file names, the replies' text, each tool step by its name only, artifacts as their source; never thinking, a tool's input or its result, or anything outside the chat.
2. **The routes, a new package `internal/shareapi`.** For the person, inside the audit wrapper: make a link (`POST /chats/{id}/shares`), a chat's links, all their links (`GET /shares`) and revoke (`DELETE /shares/{share}`, which keeps the row so the link answers 410). Two door routes, the only pages reachable without signing in: `GET /shared/{token}`, the read view, and `GET /shared/{token}/images/{image}`, an image of a message the view shows, under `default-src 'none'; sandbox`. They are given no store, read no cookie and set none; each call that finds a link is written in that tenant's audit log under the route's pattern and the link's id, never the token, and a view that cannot be recorded is not served. 404 for a link that is not there, 410 for one revoked or expired, 403 for one whose chat is held or incognito. Settings in a new `settings/share.json`: link lifetime, the most open links, whether images and artifacts show, and the default view. OpenAPI regenerated.
3. **The screens**, provisional on the mockups (only the Share button is drawn there): a Share button in the chat's header (one line in `ChatLinks.tsx`) opening a popover to make a link (now or as of now), copy it, and revoke this chat's links; Settings → Shared links listing every link with revoke; the read-only page at `/share/<token>`, drawn before the app asks who is signed in (`main.tsx`), built from the tokens and the chat's classes, which renders text and never HTML.
4. **The proof**: Go tests in both packages (only that chat, not another of the same person, nothing of another tenant; a forged and a re-addressed token; revoked and expired refused; held, incognito and empty refused; no cookie read or set; snapshot and live; no tool result or thinking in the view; images only of the view; every view recorded without the token), a standing check `share` in `tools/verify/share.go`, the two doors on the door list in `routes.go`, and the share door's probes added to the tenant isolation walk (one line in `isolation.go`). A browser check on a server of its own.
5. **The write-up**: `docs/architecture/share.md`, decisions from 0470 (the door and the token, live with a snapshot option, what a link shows, the refusals and their statuses), the journal, the status.

Left for later: a limit on how often the door may be tried from one address (with P10-05); copying a link again after it was made (only its hash is kept, so a new link is made instead); a title or note the person gives a link.


### Result (interface lane, November 26, 2026)

Provisional on the mockups for the screens only: there is a mockup of the Share button and none of the popover, Settings → Shared links or the shared page. The server side, the doors, the rules and the proof are complete.

- Built as planned: `internal/share` (the token, `Find`, `Usable`, `Create`, `List`, `Revoke`, `Read`), `internal/shareapi` (four routes and two doors), migration `20261126T004100_share_links.sql`, `settings/share.json`, one line in `audit.Scrub`, and the screens (`ShareControl.tsx`, `ShareSettings.tsx`, `Shared.tsx`, `share.ts`, `share.css`) with one line in `ChatLinks.tsx`, three in `Settings.tsx` and the shared page drawn by `main.tsx`. Decisions 0470 to 0473.
- Changed from the plan: `share.max_links` is a tenant-admin setting, not an install one, because no install value an admin changes is stored yet and a test could not set it; 0 makes no new link.
- The proof: a shared link shows only that chat, not another of the same person and nothing of another tenant (tests in both packages, the `share` check, and `shareProbes` in the tenant isolation walk, which now covers the two doors); a revoked link is refused with 410 and an expired one too; held and incognito chats are not shared and a link of a chat held since is refused with 403; the doors set no cookie and read none; every view is in its tenant's audit log and no log or file holds the token. A new standing check, `share`, makes `verify` 32 of 32. Seen in the desktop app's browser pane on a server of its own.
- On the owner's install every path is behind Cloudflare Access, so a link opens only for people his Access policy lets in; that is his to change, and nothing in Keystone depends on it.
- `web/dist/` is not committed: `npm ci && npm run build` in `web/app`.
- See `docs/journal/2026-11-26-interface-007.md` and `docs/architecture/share.md`.