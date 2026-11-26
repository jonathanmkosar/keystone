# P9-03. Artifacts

Status: provisional
Lane: agents + interface
Depends on: P7-01
Owner gate: Approval of the workspace mockups (P1-06), for the panel only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Side panel for code, HTML, React, SVG, Mermaid and markdown; sandboxed rendering; versions; download; local publish link.
Proof: Each artifact type renders in the sandbox; versions can be compared and restored.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (interface lane, November 25, 2026)

Branch `interface/p9-03-artifacts`, cut from `reboot` at 8106f15. Built on the tool loop (P7-01), whose only open part is the agent mockups. New code goes in new files; every line changed in a shared file is listed in the journal.

1. **The store**, a new package `internal/artifacts`. An artifact belongs to one chat and has a type (`code` with a language, `html`, `react`, `svg`, `mermaid`, `markdown`), a title and numbered versions. Rows in `artifacts` and `artifact_versions` (migration `20261125T221947_artifacts.sql`); each version's content is a file in the tenant's `artifacts/` folder, `<chat>/<artifact>/v<n>`, through `Store.Artifacts()` (an `os.Root`, so no path leaves it). Deleting the chat removes its folder (two lines in `chat.Delete`) and the rows go with it. Every version is kept unless `artifacts.versions_kept` says otherwise. A version written while the chat is held for sensitive content is marked sensitive, and so is every artifact of a held chat. The package reads no setting.
2. **The tools**: `create_artifact`, `update_artifact` (the whole new content, or one exact replacement; always a new version) and `read_artifact` (this chat's artifacts only), added in `app.Tools`. They run through the registry, so each call is recorded and streams as a tool step. They change nothing outside Keystone, so they run without asking by default: a decision record.
3. **The routes**: a chat's artifacts, all of the person's, one with its versions, one version with its content, a line diff of two versions, restore (an older version as a new one), download (its file name and type), and the sandbox document. The **publish link** is `/artifacts/<id>`: the app's page that shows the artifact alone, read through the API, so only its own signed-in tenant ever sees it, never the public: a decision record.
4. **The sandbox**: the sandbox route serves one document per version with a policy of its own (`default-src 'none'`, nothing fetched or sent, no form action, framed only by Keystone, and the `sandbox` directive) and the main page's policy stays as it is. HTML as written; SVG as an image; Mermaid and React with renderers built as classic scripts into `web/dist/sandbox/` by a second Vite build, never in the main bundle (`mermaid` and `sucrase` as dependencies: a decision record); markdown and code as escaped text. In the panel, markdown is drawn by the app's Markdown reader and code as a code block with its language; the rest in an `iframe` whose `sandbox` never allows same-origin.
5. **The panel**, provisional on the workspace mockups (P1-06): a card in the reply opens it beside the chat, with a version picker, preview and source, a compare view, restore, download, copy and the publish link; the publish page; the Artifacts screen lists them.
6. **The proof**: Go tests of what the sandbox serves for each type, of compare and restore, of tenant isolation and paths, of the sensitive mark, and of the tools through the registry; a `verify` standing check, `artifacts`; a browser check of each type.
7. **Settings** in a new `settings/artifacts.json`: the largest artifact, versions kept, the longest title, and the sandbox's permissions.

Left for later: artifacts in the test tenant's fixture; editing an artifact by hand in the panel; sharing outside Keystone (P10).

### Result (interface lane, November 25, 2026)

Provisional on the workspace mockups (P1-06) for the panel; the server side, the sandbox and the proof are complete.

- Built as planned: `internal/artifacts` (store, tools, routes, the sandbox page, the line diff), migration `20261125T221947_artifacts.sql`, `settings/artifacts.json`, one call in `chat.Delete`, and the screen (`ArtifactCard.tsx`, `ArtifactPanel.tsx`, `ArtifactFrame.tsx`, `ArtifactPage.tsx`, `artifacts.ts`, `artifacts.css`) with the renderers in `src/sandbox/` built by `vite.sandbox.config.ts`. Decisions 0420 to 0423.
- Changed from the plan: the renderers are written into the sandbox page, not loaded from `/sandbox/`, so a page of no origin fetches nothing at all (decision 0422); `update_artifact` and `read_artifact` also take an artifact's exact title; the test tenant has a recorded turn that makes one artifact of each type and one that changes it, which the plan had left for later.
- The proof: each type renders in the sandbox (a test per type of what the sandbox page serves and its policy, in `sandbox_test.go`, and each type drawn in the browser); versions are compared and restored (`diff_test.go`, the routes' and the store's tests, and in the browser); the sandbox cannot read the session (its policy's `sandbox` directive and the frame's attribute without same-origin, in tests and the check; in the browser its origin was `null` and cookie, storage and a fetch of the session were refused). A new standing check, `artifacts`, makes `verify` 27 of 27.
- Provisional: the panel, the card, the publish page and the Artifacts screen (no mockup); and the frame drawn inside the app, which the desktop app's browser pane refuses (it blocks every request from a sandboxed frame) and which has not been seen in Chrome, Edge or Safari.
- `web/dist/` is not committed: `npm ci && npm run build` in `web/app` builds the page and both renderers.
- See `docs/journal/2026-11-25-interface-006.md` and `docs/architecture/artifacts.md`.

