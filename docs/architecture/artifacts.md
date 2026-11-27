# Artifacts

Folders: `internal/artifacts`, the renderers in `web/app/src/sandbox/` with `web/app/vite.sandbox.config.ts`, and on the screen `web/app/src` (`artifacts.ts`, `ArtifactCard.tsx`, `ArtifactPanel.tsx`, `ArtifactFrame.tsx`, `ArtifactPage.tsx`, `artifacts.css`). Item P9-03.

The documents a model makes in a chat: code, HTML pages, React components, SVG pictures, Mermaid diagrams and Markdown. Each is kept with every version it has had in the tenant's `artifacts/` folder, opens in a panel beside its chat, is drawn in a sandboxed frame that can never reach the person's session, and can be compared, restored, downloaded and opened alone at its publish link.

Decisions [0420](decisions/0420-artifact-tools-run-without-asking.md) to [0423](decisions/0423-the-publish-link-is-private-to-its-person.md) say why it is built this way. The panel is provisional until the workspace mockups (P1-06) are approved.

## The store

`internal/artifacts`, migration `20261125T221947_artifacts.sql`.

- **An artifact belongs to its chat.** A row in `artifacts`: a random id of 16 hex characters, the chat, its type (`code`, `html`, `react`, `svg`, `mermaid`, `markdown`), a code artifact's language, and the newest version's title and number.
- **Every version is kept.** A row in `artifact_versions` for each, numbered from 1, with its title, size, SHA-256, who wrote it (`model`, or `restore` with the version it came from), whether it is sensitive, the request id it was written for, and when. Its content is a file in the tenant's artifacts folder, `<chat>/<artifact>/v<n>`, written through `Store.Artifacts()`, an `os.Root`, under another name first and then renamed. A version is never changed: an update and a restore each write a new one. `Content` refuses a file whose bytes do not match its row's digest.
- **Deleting the chat removes them**: the rows go by `ON DELETE CASCADE`, and `chat.Delete` removes the chat's folder in the artifacts folder (one call there), so an incognito chat's artifacts go with it.
- `artifacts.versions_kept`, zero as shipped, keeps every version. Set, the oldest go past that number, and the newest never does.

| Function | Does |
| --- | --- |
| `Create(ctx, store, o, chatID, Draft, requestID)` | A new artifact, as version 1. |
| `Update(ctx, store, o, chatID, id, Change, requestID)` | A new version of an artifact of that chat: the whole new content, or one exact replacement (`OldText` that appears once, by `NewText`), and a title. An artifact of another chat is not found. The same content again is refused. |
| `Restore(ctx, store, o, id, version, requestID)` | An older version written again as the newest. The newest itself is refused. |
| `Get`, `List`, `ListAll`, `Versions`, `Content` | Read, each from this tenant's store only. |
| `Diff(older, newer)`, `Counts` | Two texts compared line by line with Myers's algorithm; versions more than 2,000 changed lines apart are shown as one taken out and the other put in. |
| `FileOf(artifact, version)` | The file name and type a version downloads as. |

The package reads no setting: `Options` (and `ReadOptions`, which `app.ReadConfig` calls) are passed in. It imports `net/http` for its routes and opens no connection. It is not a guard of a fixed rule in `verify`'s list, since it imports the chat record, which reads settings: tenant isolation is the store's (`internal/tenant`) and the sensitive mark is recall's (`recall.Held`), both guards.

## Sensitive content

Fixed rule 3. A version written while its chat is held for content from a sensitive space ([knowledge-recall.md](knowledge-recall.md)) is marked sensitive, and every artifact of a held chat reads as sensitive, written before the hold or after, since what it is changed into may draw on that content. The panel and the publish link show a pill and the notice `artifacts.SensitiveNotice`; the model is told when it makes or changes one. An artifact's content reaches no model call but those of its own chat, which is held already: the tools act on the chat they are called from, and `read_chat` from another chat names tool calls and leaves their input out.

## The tools

`artifacts.Add`, called in `app.Tools` (`internal/app/tools.go`). Each acts on the store and the chat of the turn that calls it and on nothing else.

| Tool | Input | Gives |
| --- | --- | --- |
| `create_artifact` | `type`, `title`, `content`, and `language` for code | "Made the artifact “Ferry times” (artifact <id>, version 1, type html). …" |
| `update_artifact` | `artifact_id` (its id, or its exact title in this chat), and `content`, or `old_text` and `new_text`; `title` to rename | "Wrote the artifact … (artifact <id>, version 2, …)". |
| `read_artifact` | `artifact_id` and `version`, or nothing | The chat's artifacts, or one version's content. |

They run through `Registry.Run`, so each call is recorded in the audit log before and after (fixed rule 4) and streams as a tool step. A refusal is an error result with the reason, written for the model; anything else says only that Keystone could not keep or read it, so no path reaches a model.

**They run without asking** (decision [0420](decisions/0420-artifact-tools-run-without-asking.md)): `ReadOnly` and `Inside` are set, so the approval rule runs them under the default `always`, after outside content too. Fixed rule 2 guards side effects outside Keystone; an artifact is Keystone's own content in the person's own folder, every version is kept, and its frame can fetch nothing. A person can still set each to ask or never on Settings → Approvals, which says "Writes only to your artifacts in Keystone, keeping every version."

The chat screen reads "(artifact <id>, version <n>" and "the artifact “title”" out of a result to draw the card, so those words are kept.

## The routes

| Route | What it does |
| --- | --- |
| `GET /api/v1/chats/{id}/artifacts` | A chat's artifacts, the oldest first. |
| `GET /api/v1/artifacts` | Every artifact of the person, the one changed last first: the Artifacts screen. |
| `GET /api/v1/artifacts/{artifact}` | `OneReply`: the artifact, its versions, `sandbox` (the frame's permissions) and `notice` when it is sensitive. |
| `GET /api/v1/artifacts/{artifact}/compare?from=&to=` | `CompareReply`: the lines of two versions, `to` the newest when left out. 400 for anything that is not two version numbers. |
| `GET /api/v1/artifacts/{artifact}/versions/{version}` | One version with its content. |
| `POST /api/v1/artifacts/{artifact}/versions/{version}/restore` | 201 with the new version. 400 `refused` for the newest. The audit entry names the request, and so does the version. |
| `GET /api/v1/artifacts/{artifact}/versions/{version}/download` | The content as an attachment named by its title (`Ferry times.html`, `Tide table.py`, and "(version 2)" for one that is not the newest), with its type, under `default-src 'none'; sandbox`, so nothing is ever drawn from it. |
| `GET /api/v1/artifacts/{artifact}/versions/{version}/sandbox?theme=` | The sandbox page: below. |

Each acts on the store the API shell resolved from the session, so another tenant's artifact is not found (404), and an id that is not 16 hex characters names nothing.

## The sandbox

Decisions [0421](decisions/0421-renderers-are-built-for-the-sandbox-alone.md) and [0422](decisions/0422-the-sandbox-is-a-page-of-no-origin-under-its-own-policy.md).

**The page.** The sandbox route serves one page per version, built by `SandboxPage`:

| Type | Served as |
| --- | --- |
| `html` | The page as written; a fragment is put in a page. |
| `svg` | An image from a `data:` address, so the SVG's own script never runs and its markup is never in a page's DOM. |
| `react` | The source as JSON (which escapes `<`, `>` and `&`), and the React renderer written into the page. |
| `mermaid` | The same, with the Mermaid renderer. |
| `markdown`, `code` | The text, escaped. The panel draws these two in the app instead. |

**Its policy**, `artifacts.Policy`, in place of the server's `default-src 'self'` for this answer alone:

    default-src 'none'; script-src 'unsafe-inline' (html, react, mermaid) or 'none'; style-src 'unsafe-inline';
    img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none';
    worker-src 'none'; manifest-src 'none'; form-action 'none'; base-uri 'none';
    frame-ancestors 'self' http://<host> https://<host>; sandbox allow-scripts allow-forms allow-modals

Nothing is fetched or sent, not even from Keystone: everything a page needs is in it. The `sandbox` directive makes it a page of no origin even when it is opened on its own, so its script reads no cookie, no storage and no API: in the browser check, `self.origin` was `null`, `document.cookie` and `localStorage` threw, and a fetch of the session was refused by the policy. `X-Frame-Options: SAMEORIGIN` and `frame-ancestors` let only Keystone's own pages frame it. A Host header that is not a name or an address and a port is left out of the policy.

**Its cache.** A page holds its renderer, megabytes for Mermaid, so it carries an ETag (the version's digest, its title, the theme, the renderer's digest and the policy) and `Cache-Control: private, no-cache`: the browser keeps it and asks each time, so a page of a deleted chat is never shown from its copy.

**The frame.** In the panel, HTML, React, SVG and Mermaid are drawn in an `iframe` whose `sandbox` is the server's permissions filtered to `frameAllowed` in `ArtifactFrame.tsx`; same-origin, top navigation and popups are never among them, and `artifacts.Options.Check` refuses them in the setting. The main page's own policy, `default-src 'self'`, also refuses the frame any navigation away from Keystone. The frame has `referrerpolicy="no-referrer"`, and the page's ground is the browser's Canvas, since an HTML artifact expects a page of its own.

**The renderers**, decision [0421](decisions/0421-renderers-are-built-for-the-sandbox-alone.md): `web/app/src/sandbox/react.ts` turns a React artifact into JavaScript with Sucrase (JSX, TypeScript and imports, the automatic runtime), runs it as a script of its own, and draws its default export under an error boundary; it may import `react` and `react-dom`, and anything else says it cannot be loaded. `web/app/src/sandbox/mermaid.ts` draws a diagram with Mermaid at its `strict` security level, in the page's theme. Each is built as one classic script, with nothing split out, into `web/dist/sandbox/react.js` (423 kB) and `mermaid.js` (5.2 MB) by `vite.sandbox.config.ts`, never into the main bundle, and the server writes it into the page with `Inline`, which writes each `<` of `</script`, `<script` and `<!--` as `\x3C`. A build without them says so in the page and shows the source.

Since P9-04 the Mermaid renderer also draws a diagram of a reply: on a page whose body says `data-ks-inline` it reads no artifact, and waits for the page that frames it to hand it a diagram's source. That page and its messages are in [inline-visuals.md](inline-visuals.md).

## The screen

Provisional until the workspace mockups (P1-06): there is no artifacts mockup, so it is built from the tokens and the patterns of the chat screen and the inspector, with the Artifacts icon of the sidebar in `design/screens`.

- **The card** (`ArtifactCard.tsx`): a call of `create_artifact` or `update_artifact` that did not fail is drawn in the reply's steps as a card, with the icon, the title, the type and the version ("HTML page · version 2"), and "Open beside the chat". It is drawn from the record, so it is the same after a reload, and pulses while the tool runs. A failed call keeps its row, with words in `steps.ts`.
- **The panel** (`ArtifactPanel.tsx`): beside the chat on a screen 1,100 pixels wide or more, where the conversation and the composer make room for it; over the chat below that. The title, the type and a sensitive pill; the notice; a version picker (newest first, with "restored from n"); Preview, Source and Compare; Restore this version (for an older one), Download, Copy, Copy link and Open alone; who wrote the version, when, and its size. Compare shows the version against the one before it, or one picked, line by line with both line numbers. Restore makes the version the newest and says so. The panel reads the artifact again when a turn of the chat ends, and follows the newest version unless one was picked. Escape closes it. One line in `Chat.tsx` draws it.
- **Markdown** is drawn with the app's own reader (`Markdown.tsx`), and **code** as a code block with its language and a copy button: neither runs anything, so neither needs the frame.
- **The publish page** (`ArtifactPage.tsx`), `/artifacts/<id>`, `?version=n` for one version: the artifact alone, outside the app's frame, with the note "Only you can open this page, signed in to this Keystone." `App.tsx` draws it in place of the shell when the address matches.
- **The Artifacts screen**, `/artifacts`: every artifact, its type and version, a sensitive pill, its chat, and when it changed; the title opens its publish page. It replaces the placeholder.
- `ApprovalSettings.tsx` says what the two writing tools do; `Icon.tsx` has download, link and open.

Its calls are in `artifacts.ts`: the eight routes above, through `api.ts`'s `call`.

## The publish link

Decision [0423](decisions/0423-the-publish-link-is-private-to-its-person.md). "Publish" here means a stable address on this Keystone that shows the artifact alone, the newest version or one named, which the person can bookmark, open in a tab of its own or on their phone through their own way in. It is read through the API with the person's session, so it opens only for its own person, signed in: anyone else is shown sign-in, and another person on the same Keystone is told it is not here. Nothing is ever public. Sharing with someone else is P10's share links.

## Settings

In `settings/artifacts.json`, lane `artifacts`.

| Setting | Scope | Default | What it does |
| --- | --- | --- | --- |
| `artifacts.max_bytes` | install | 1048576 | The largest one version may be. |
| `artifacts.versions_kept` | install | 0 | How many of an artifact's newest versions are kept; zero keeps every one. |
| `artifacts.title_max_chars` | install | 120 | The longest title. |
| `artifacts.sandbox` | install | `allow-scripts allow-forms allow-modals` | What the frame may do, from `allow-scripts`, `allow-forms`, `allow-modals`, `allow-downloads` and `allow-pointer-lock`. Same-origin, top navigation and popups are refused whatever it says. |

## The test tenant

`fixtures/test-tenant/turns/artifacts.json`: "Make one artifact of each kind for the harbour trip." makes six, one of each type, in one step; "Make the first ferry leave at ten." changes the ferry times by their title, as version 2.

## Checked by

- `go test ./internal/artifacts`:
  - the store: version 1 in its file; every version kept and an update a new one, by whole content or one replacement, with each refusal (text not there, there twice, both kinds of change, nothing changed); restore as a new version, the older one unchanged; versions kept; refusals with reasons, leaving nothing in the folder; another tenant reads nothing and path-like ids are refused; deleting the chat removes its folder and rows; an artifact of a held chat is sensitive, written before or after the hold; the setting never gives the frame same-origin, top navigation or popups;
  - **the sandbox, a test per type**: each type's policy fetches and sends nothing, gives no origin and lets only Keystone frame it, and a crafted Host does not reach it; HTML as written and a fragment in a page; SVG as an image with its markup out of the page; React and Mermaid with their source as JSON that cannot end its element, and their renderer in the page; a build without the renderers says so; Markdown and code escaped; a renderer that holds `</script>` or `<!--` is written safely;
  - the routes, through the API shell: list, one, a version, compare, restore, download with its name, type and policy; the sandbox's policy, SAMEORIGIN, and its ETag answered with 304; another person gets 404 from every route; a sensitive artifact's notice;
  - **compare**: the shortest comparison, rebuilding both sides, with line numbers in order, and versions far apart still true;
  - the tools through the registry, recorded before and after, by id and by title, refusing another chat and another person; under the approval rule they run without asking, after outside content too, and ask or are refused when the person sets them to.
- `verify`, a new standing check, **`artifacts`** ([tools.md](tools.md)): on the real server with an invented person and a model the check answers for, one turn makes one artifact of each type and changes the HTML one, without an approval, with every call in the audit log before and after; each type's sandbox page and policy; compare and restore through the routes, the restore in the audit log; the download's name; 404 for another person; the sensitive notice; deleting the chat removes the folder. From the source: the frame's permissions are all allowed ones, no artifact file names same-origin, top navigation or popups, only the sandbox's entries import Mermaid or Sucrase, and the main page's policy is still `default-src 'self'`. `internal/artifacts` has its `componentDocs` line and its `offline` line in the same file.
- In a browser (the desktop app's built-in browser pane), against the server built from this branch on port 7981 with a data folder in the worktree, as the test tenant: the recorded turn made six cards that streamed in; each opened the panel beside the chat; the update by title made version 2; Compare showed one line out and one in; version 1 restored as version 3, and the comparison of 2 and 3 followed; Markdown drawn by the app's reader and Python as a code block; the publish page alone; the Artifacts screen; light and dark; at phone width the panel covers the chat. The sandbox pages, opened on their own: the HTML page's button counted, the React counter went from 6 to 4, the Mermaid route drew in the dark theme, the buoy drew as an image, and the page's origin was `null` with cookie and storage refused and a fetch of the session blocked by the policy. **The frame inside the panel stayed empty in that pane**: it refuses every request from a sandboxed frame or a page of no origin (`ERR_BLOCKED_BY_CLIENT`), which is the pane's own filter, not the page's; the frame's attribute and address were read and are as above. It has not been seen in Chrome, Edge or Safari: Claude in Chrome was not connected.

## Provisional

- **The panel, the card, the publish page and the Artifacts screen** wait on the workspace mockups (P1-06).
- **The frame drawn inside the app** has not been seen in an ordinary browser (see above).

## Not here yet

- Artifacts in a held chat are marked and kept; nothing yet keeps one from being opened on a screen another person can see.
- Editing an artifact by hand in the panel; deleting one artifact (they go with their chat).
- Libraries for React artifacts beyond React itself; Tailwind; a theme the artifact can read.
- Sharing an artifact with someone else (P10).
