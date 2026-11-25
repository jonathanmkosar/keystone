# P4-01. Web shell

Status: provisional
Lane: interface
Depends on: P2-05
Owner gate: Approval of the design system (P1-02) and the chat mockups (P1-04).
Built without the gate: Built from the draft tokens and draft mockups, marked provisional.
Scope: React app from the design system tokens, embedded in the binary: sign-in, sidebar frame, theme switch, routing.
Proof: The shell matches its mockups light and dark; built files are committed and the binary serves them.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result


### Plan

Started before P2-05 is done, against the API it is expected to have. Everything that rests on a guessed route is provisional.

What will change:

- `web/app/`: the source, a React, Vite and TypeScript app. Its only packages are react, react-dom, react-router, vite, its React plugin and typescript.
- `web/dist/`: the built files, replacing the placeholder page. Committed, and compiled in by `web/embed.go`, which does not change.
- The frame, from `design/tokens-draft.json` as CSS variables: a sidebar with the mark and wordmark, a main area with a header, laid out as in `MainDark.html` and `MainLight.html`.
- A theme switch with three choices: dark, light, or follow the system. The choice is kept in the browser.
- Routes in the browser for sign-in, the home screen and a page-not-found screen.
- A sign-in screen that lists the people on the install. The person picks their own and types a password. This is the answer `STATE.md` assumes for the open sign-in question.
- One module, `web/app/src/api.ts`, holds every server call: list people, sign in, sign out, who am I. The paths in it are guesses, marked provisional.
- A "server not ready" state. When the API answers 404 or cannot be reached, the shell still loads and says so, with a way to try again.

The contract, guessed (JSON in and out, the session in a cookie the page cannot read):

| Call | Request | Answer |
| --- | --- | --- |
| `GET /api/people` | | 200 `{"people":[{"id","name"}]}` |
| `POST /api/sign-in` | `{"person","password"}` | 200 `{"person":{"id","name"}}`, 401 refused, 429 wait |
| `POST /api/sign-out` | | 204 |
| `GET /api/me` | | 200 `{"person":{"id","name"}}`, 401 not signed in |

How it will be proven:

- `go run ./tools/rebuild` is green and its "it serves the page" step fetches the new `index.html`.
- `go run ./tools/verify` is green.
- The page is loaded in a browser from the built binary: with no API it shows the "server not ready" state, dark and light.
- The server sends `Content-Security-Policy: default-src 'self'`, so the app uses no inline script and no inline style attribute, and loads nothing from another site.

Not in this pass: bundled font files (the font stacks name the design's fonts and fall back to system fonts), passkeys in the browser, a standing check that the built files match the source, and tests of the app itself.

### Result

Provisional. The shell is built and the binary serves it: the frame, the theme switch, real-path routing, the sign-in screen and the "server not ready" state. It waits on three things.

- The owner's approval of the design system (P1-02) and the chat mockups (P1-04): the tokens are a draft.
- The API shell (P2-05): the four paths in `web/app/src/api.ts` are guesses, and the server needs the fallback to `index.html` for client-side routes. Both are written out in `docs/journal/2026-11-24-interface-001.md`.
- Proof against a real server: sign-in, sign-out and the signed-in frame have only been seen in the "not ready" state.

Font files are not bundled yet (decision 0072), and the standing check that the built files match the source is still to be added to `tools/verify` (decision 0005).
