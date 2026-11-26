# Share links

Folders: `internal/share`, `internal/shareapi`, `web/app/src` (`Shared.tsx`, `ShareControl.tsx`, `ShareSettings.tsx`, `share.ts`, `share.css`)

A share link is a read-only link to one chat that someone who has not signed in can open (P10-04). It is the only kind of page Keystone shows without a session. The person makes one from the chat's header, copies it once, lists their links and turns any off.

- `internal/share` is the rules: the token and its hash, finding a link's one tenant from the token, the refusals, and what a link shows. It is a guard of fixed rules 1 and 3 and reads no setting; what may change is passed in by the caller.
- `internal/shareapi` is the routes: four for the signed-in person, inside the audit wrapper, and two door routes. It reads the share settings for the tenant a call is for.
- The web interface draws the Share button and its popover, Settings → Shared links, and the shared page at `/share/<token>`.

## A link and its token

A link is a row in `share_links` in the chat's own tenant database (migration `20261126T004100_share_links.sql`), and goes with its chat by `ON DELETE CASCADE`.

The token is `ksh_`, the tenant's id, an underscore, and 32 random bytes as 64 hex characters: the shape of a session token, with its own prefix (decision [0470](decisions/0470-a-share-link-is-a-door-that-finds-its-tenant-from-its-token.md)). The database keeps its SHA-256 and never the token. The token is in the answer that makes the link, once, and cannot be had again: a lost link is turned off and made again. A link also has an id of 16 hex characters, which names it in lists and in the audit log and opens nothing.

The token is in the link's address, `/share/<token>`, and in the door routes' paths. It is kept out of every log Keystone writes:

- `audit.Scrub` removes anything shaped like a share token, as it does a session token. The server's log line for each API call, which holds the path, goes through it.
- A door writes its audit entry with the route's pattern, `GET /api/v1/shared/{token}`, as the action, and names the token as a secret to `audit.Append`, so even a stray copy is removed.
- `Referrer-Policy: no-referrer` is on every page, so a link followed from a shared chat does not carry the address away.

The tenant's id can be read from the token, as from a session token. It is not a secret (decision 0007).

## Finding a link

`share.Find(install, token)` is the whole path, as `signin.Resolve` is for a session:

1. Read the tenant's id from the token. A token that is not the shape is not found.
2. The id must be a tenant on the install.
3. Open that one tenant's store, and look there for the SHA-256 of the whole token.

No other store is opened, and nothing else in a request (a header, a query parameter, a cookie) is read to choose one. A token re-addressed to another tenant changes its hash and points at a database that never held it. A link row copied into another tenant's database is not found by the original token, which still names its own tenant. A token that finds nothing is 404 and leaves nothing in any tenant's audit log; the server's log notes it.

`share.Usable(ctx, store, link)` then says whether the link shows its chat now. It refuses, in this order:

| Refusal | Status | Code | Why |
| --- | --- | --- | --- |
| The person turned it off | 410 | `revoked` | The row is kept, so the link says so. |
| Its time ran out | 410 | `expired` | `share.link_lifetime` from when it was made. |
| The chat is held for content from a sensitive space | 403 | `sensitive` | Fixed rule 3: a held chat is never shown through a link. Read again on every call, so a chat held after the link was made is refused. |
| The chat is incognito | 403 | `incognito` | An incognito chat keeps no record and is never shared. |

Decision [0473](decisions/0473-refusals-say-why-and-a-revoked-link-answers-410.md) says why these statuses.

## Making a link

`share.Create` refuses, with 409 and a reason the popover shows:

- a chat held for content from a sensitive space (`sensitive`), checked with `recall.Held`;
- an incognito chat (`incognito`), checked with `memory.Incognito`; a flag that cannot be read refuses too;
- a chat with no messages yet (`empty`);
- a person with `share.max_links` open links already (`too_many`). A link turned off or expired does not count. 0 means no new link.

A chat that is not the person's is 404, as it is everywhere.

## What a link shows

`share.Read` builds a `View` from the chat record and from nothing else (decision [0472](decisions/0472-a-link-shows-words-images-and-names-and-never-a-tools-result.md)):

- the chat's title, whether the link is live or a snapshot, when it was made, and when the last message shown was written;
- the person's messages: their words, their images (by id, when `share.images` is on) and the names of their files;
- the replies: their words, each tool step by the tool's name and whether it failed, an artifact version a step made shown as its source (when `share.artifacts` is on), and Keystone's note when a reply was stopped, broke off, reached its length limit, or was declined. A model's messages in a row are one reply; a background task's note is a reply of its own.

Never: the model's thinking, what a tool was sent, what a tool gave back (a search of the person's other chats or of their knowledge store would show them), the person's name, the model, other chats, the sidebar, settings or memory.

**Live or snapshot** (decision [0471](decisions/0471-a-link-shows-the-chat-as-it-is-now-or-as-it-was.md)). A live link shows the chat's current branch when it is opened, as the chat screen does, later messages included. A snapshot shows the branch up to the last message there was when the link was made: the row keeps that message's id, and the view walks up from it by each message's parent. Messages are never changed or removed, only the whole chat, so a snapshot stays as it was. `share.default_view` chooses when the person does not; it ships as live.

**Images** come through the second door, `GET /api/v1/shared/{token}/images/{image}`, only for an image one of the person's messages on the shown branch holds, only when it is an upload of that chat whose type was known by its bytes as an image, and only while `share.images` is on. It is served inline under `default-src 'none'; sandbox`, as the upload content route serves one, with `Cache-Control: no-store`.

**Artifacts** are shown as their source in a code block and never run: no sandbox page, no frame (decision 0472). The artifact must be of the link's chat; the guard checks the `artifacts` row before asking for the content.

## The doors

The two door routes are listed in `tools/verify/routes.go`. They are given no store by the API shell and read no cookie: each finds its tenant with `share.Find` and closes the store when it is done. They set no cookie.

Every call that finds a link is written in that tenant's audit log: kind `api`, the route's pattern as the action, `share:<link id>` as the actor, the status and the outcome (`ok`, or `refused` for 4xx). A view that cannot be recorded is not served (`not_recorded`, 500). A view that is served also counts on the link's row (`views`, `viewed_at`), which the person's list shows.

The JSON door answers under the main page's policy. The shared page is served by `cmd/keystone` like every page, under `default-src 'self'`.

## The routes

| Route | Session | What it does |
| --- | --- | --- |
| `POST /api/v1/chats/{id}/shares` | needed | Make a link. Body `{"view": "live" | "snapshot"}`, which may be left out. 201 with `link`, `token` and `path`. |
| `GET /api/v1/chats/{id}/shares` | needed | The chat's links, the newest first, with each one's state. |
| `GET /api/v1/shares` | needed | Every link of the person, the newest first. |
| `DELETE /api/v1/shares/{share}` | needed | Turn a link off for good. Twice changes nothing. |
| `GET /api/v1/shared/{token}` | door | The view. 404, 410 or 403 as above. |
| `GET /api/v1/shared/{token}/images/{image}` | door | An image the view shows. |

A link's state, in the lists: `open`, `revoked`, `expired`, or `refused` when its chat is now held or incognito, with a reason for a person.

## The screens

Provisional: only the Share button has a mockup (`MainDark.html`, `MainLight.html`).

| Part | File | What it does |
| --- | --- | --- |
| The button | `src/ShareControl.tsx` | **Share**, an outlined button at the right of a chat's header, after Hand off, drawn by one line in `ChatLinks.tsx`. Not in an incognito chat. |
| The popover | `src/ShareControl.tsx`, `src/share.css` | What a link shows to whom, the choice between the chat as it is when opened and as it is now (the person's `share.default_view` chosen first), **Make a link**, the address with **Copy link** and the sentence that it is shown this once, the server's reason when a link is refused, this chat's open links with **Turn off**, and a link to all of them. Escape and a click outside close it. |
| Settings → Shared links | `src/ShareSettings.tsx` | Every link: its chat, what it shows, when it was made, how often and when it was last opened, until when it opens, its state, and **Turn off**. Three lines in `Settings.tsx`. |
| The shared page | `src/Shared.tsx` | `/share/<token>`, drawn by `main.tsx` in place of the app, before anything asks who is signed in. It reads the view with `credentials: "omit"`, so the browser sends no session cookie with it, and imports nothing from `api.ts`. The mark and a sentence saying what it is; the title and when; the person's messages as bubbles with their images and file names; the replies with their tool steps as words, their text through `Markdown.tsx` (React elements, never HTML), artifacts as source in a code block, and Copy; the refusals in words. |
| Calls | `src/share.ts` | `makeLink`, `chatLinks`, `allLinks`, `revokeLink`, through `api.ts`'s `call`. |

## Behind Cloudflare Access

On the owner's install every path is behind Cloudflare Access, so a share link opens only for people his Access policy lets in. Opening links to others is the owner's to change in Access (for example a bypass rule for `/share/*` and `/api/v1/shared/*`), and nothing in Keystone depends on it: Keystone treats every caller of the doors as someone who has not signed in.

## Settings

In `settings/share.json`, lane `share`. The guard reads none of them; `shareapi.ReadConfig` reads them for the tenant a call is for.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `share.link_lifetime` | 720h | tenant | How long a new link opens. 0s: until it is turned off. |
| `share.max_links` | 50 | tenant-admin | The most open links a person may have. 0: no new link. |
| `share.images` | true | tenant | Whether a shared chat shows the person's images. |
| `share.artifacts` | true | tenant | Whether a shared chat shows artifacts as their source. |
| `share.default_view` | live | tenant | What a new link shows when the person does not choose: live or snapshot. |

## Checked by

- `go test ./internal/share`: a link shows its own chat and nothing of another chat or tenant; forged, re-addressed and copied tokens find nothing; the token is in no file of the data folder; revoked and expired links are refused; held, incognito and empty chats are not shared, and a link of a chat held or made incognito since is refused; the most open links; a snapshot keeps its branch and a live link follows the current one; no thinking, tool input or result in a view, and only the view's images; an artifact only of its own chat; notes and task replies.
- `go test ./internal/shareapi`: through the API shell, a link shows only its chat with no session and with another person's, sets no cookie, writes each view in its tenant's log under the route and the link and nowhere else, and no log holds the token; another person cannot list or turn off a link; each refusal's status and code; images only of the view, under their policy, and none when switched off; artifacts as source; the admin's most.
- The `tenant isolation` check (`shareProbes` in `tools/verify/share.go`): the doors are on the list, and the share door finds its tenant from the token alone.
- The `share` check (`tools/verify/share.go`), the item's proof on the real server: only that chat, every view recorded and the token nowhere, no cookie, revoked and expired refused, snapshot and live, held and incognito; and from the source, the shared page reads with credentials omitted, sets no HTML, and is drawn before the session check.
- In the desktop app's browser pane, on November 26, 2026, against the server built from this branch on `127.0.0.1:7984` with a data folder inside the worktree (removed afterwards), as the test tenant: Share on the lighthouse walk made a live link; opened in another tab it showed the chat alone, with one request (`GET /api/v1/shared/…`, 200) and no console message, in light at desktop width and in dark at phone width. A chat of the recorded web search and artifact turns, shared as a snapshot, showed the search step by name with "failed" and the six artifacts as source; a message sent afterwards did not appear in it. Turned off from Settings → Shared links, it read "This link is closed". The popover at phone width in dark. The server's log held `/api/v1/shared/[removed]` and the link's id, never the token.

## Provisional

- The screens: there is a mockup of the Share button and none of the popover, the Shared links screen or the shared page. Chosen without one: the popover's place and words, the two choices' wording, the address shown once with Copy link, the list's rows, the shared page's header and footer, tool steps as words without a way to open them, artifacts as a code block with a copy button.
- A failed tool step reads as done with a "failed" tag ("Searched the web", failed), because the page is told only the tool's name and whether it failed.

## Not here yet

- A limit on how often the doors may be tried from one address, with phone access from outside (P10-05).
- Copying a link again after it was made: only its hash is kept.
- A name or a note the person gives a link, and a list of who opened it beyond the count (the audit log has each call).
- Search engines are not told to stay away (`noindex`): the doors are reachable only through the owner's Access today.
