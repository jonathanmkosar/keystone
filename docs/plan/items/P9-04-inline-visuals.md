# P9-04. Inline visuals

Status: provisional
Lane: interface
Depends on: P9-03
Owner gate: Approval of the workspace mockups (P1-06).
Built without the gate: Built from the draft mockups, marked provisional.
Scope: Charts and diagrams rendered in the reply.
Proof: Test-tenant chart and diagram replies render as in their mockups.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result


### Plan (interface lane, November 26, 2026)

Branch `interface/p9-04-inline-visuals`, cut from `reboot` at cfa1d15. Built on artifacts (P9-03), whose open part is its mockups and one look in an ordinary browser. There is no mockup of a chart or a diagram in a reply, so the screen is built from the tokens and ends provisional on the workspace mockups (P1-06). New code goes in new files; every line changed in a shared file is listed in the journal.

1. **The two formats.** A fenced `mermaid` block is a diagram. A fenced `chart` block is a chart: a small JSON object with a `type` among `bar`, `line`, `area`, `pie` and `scatter`, a `title`, `x` and `y` with their titles (and `x.labels`), and named `series` of numbers (pairs for scatter). It is decided in a decision record, kept small, and checked before anything is drawn: the type, the counts against settings, every number finite, every label a short string.
2. **Charts are drawn by the app's own code**, as inline SVG built from the checked data: no chart library, no markup or script from the model. `web/app/src/chart.ts` (no imports) reads and checks the format, lays the chart out as plain data (bars, lines, areas, slices, points, ticks, legend), and writes its summary and its table. `Visuals.tsx` draws that as SVG in the theme's colours, with a sentence that sums it up and the data as a table one click away.
3. **Diagrams are drawn in a sandboxed frame of the artifact sandbox's kind**: a page of no origin, served by a new route with the sandbox's policy and the Mermaid renderer the binary already holds, at Mermaid's strict level. The page is the same for every diagram; the app hands it the block's source and the theme's colours (read from the tokens) with `postMessage`, and the frame says when it has drawn or why it could not. Nothing a model writes runs in the app's own page, and the main page's policy stays `default-src 'self'`. A decision record says why charts and diagrams differ. A new package `internal/visuals` holds the route, and one for saving a diagram as a Mermaid artifact of its chat.
4. **In the reply.** `Markdown.tsx` marks whether a fenced block was closed and lets a scope draw it; only the conversation gives that scope, so a shared page (P10-04), a task's note, a sub-agent's result and an artifact keep showing these blocks as text. A block that is still being written shows as a placeholder of the height it will draw at, and draws once it is closed, so the page does not jump. A block that does not read, is too large, or whose frame does not answer shows as its source with a plain note. Each visual has its source one click away, opens larger over the page, and a diagram can be saved as an artifact.
5. **The model is told** about the two formats in the standing instructions, through a setting, after the standing text and before anything that changes from call to call, so the standing start stays cacheable. Switched off, nothing is said and nothing is drawn.
6. **Settings** in a new `settings/visuals.json`: whether visuals draw, the instruction text, the largest block drawn, the most series and points of a chart, the longest label, the heights, and how long a diagram's frame is waited for.
7. **The proof**: recorded test-tenant turns whose replies hold a chart and a diagram (invented data) and a chat that shows them; tests of the chart format's checks and of what the page builds for each type (`node --test`, decision 0501); Go tests of the frame's page, its policy and the two routes, and of the standing instructions; a `verify` standing check, `inline visuals`; a browser check in light and dark and at phone width.

Left for later: charts and diagrams inside a Markdown artifact or a research report; saving a chart as an artifact; a chart's values on hover beyond the browser's own tooltip.

### Result (interface lane, November 26, 2026)

Provisional on the workspace mockups (P1-06) for the look; the formats, the drawing, the diagram's frame, the instructions, the settings and the proof are complete.

- Built as planned: `web/app/src/chart.ts` (the chart format, its checks, its layout, its summary and table), `visuals.ts` (which blocks draw, the settings, the frame's messages), `InlineVisuals.tsx` and `visuals.css` (the screen; the plan called it `Visuals.tsx`, a name Windows does not tell from `visuals.ts`), a waiting mode in `sandbox/mermaid.ts`, a drawer for fenced blocks in `Markdown.tsx` that only `Conversation.tsx` gives; `internal/visuals` (the diagram's page and its policy, and saving a diagram as an artifact); `internal/turn/visuals.go` (the instructions in the standing text); `settings/visuals.json`; the test tenant's three recorded turns and a fourth chat. Decisions 0540 to 0544.
- Changed from the plan: the diagram's page names its one script by its digest, where the plan said the sandbox's policy as it is, so no inline script but Keystone's renderer can run there; a diagram's frame never tells the page a size, and is a set height with the diagram fitted inside, which is what keeps the page from jumping (decision 0543); the pointer's note on a chart is drawn by the app, which the plan had left for later; series colours are six existing tokens with a second mark each, since the tokens have no chart palette (decision 0544). No dependency was added.
- The proof: test-tenant chart and diagram replies render. The recorded turns hold a chart and a diagram (`go test ./internal/turn`, and every recorded chart block is checked by the page's own reader in `chart.test.ts`); the format's checks and what the page builds for each type are 27 tests under `npm test`; the page, its policy and the two routes are in `go test ./internal/visuals`. A new standing check, `inline visuals`, makes `verify` 39 of 39. In a browser: every chart type in light and dark and at phone width, the placeholder and the drawn visual the same height while a reply streamed, a block that does not read as its source with a note, a shared page as text only; and both diagrams drawn inside their frames in the reply in headless Edge, in dark and light.
- Provisional: the look of all of it (no mockup draws a chart or a diagram in a reply), and the series colours until the design has a chart palette. A diagram in a reply has been seen in headless Edge only: the desktop app's browser pane refuses a sandboxed frame's request, and Chrome was not connected.
- `web/dist/` is not committed: `npm ci && npm run build` in `web/app` builds the page and both renderers.
- See `docs/journal/2026-11-26-interface-009.md` and `docs/architecture/inline-visuals.md`.
