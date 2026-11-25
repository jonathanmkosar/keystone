# Web interface

Folder: `web`

The web interface is a React and TypeScript app, built with Vite and compiled into the binary. The source is in `web/app/`. The built files are in `web/dist/`. `web/embed.go` embeds everything in `web/dist/`, and `web.Files()` hands it to the server with `index.html` at its root. `keystone serve` serves it (see [cli.md](cli.md)).

`web/dist/` is committed, so building Keystone needs Go only. Decision [0005](decisions/0005-web-built-files-committed.md) says why. Node is needed only by someone changing the interface.

## Building it

    cd web/app
    npm install
    npm run build

`npm run build` checks the types and then writes `web/dist/`, emptying it first. Commit the source change and the new `web/dist/` together, then run `go run ./tools/rebuild` so the binary carries them. `npm run dev` runs Vite's own server and passes every `/api` call to Keystone on `127.0.0.1:7900`, so the app can be worked on against a running `keystone serve`.

The packages are react, react-dom and react-router, with vite, its React plugin and typescript to build. There is no component kit.

## What is there today

The web shell (P4-01): the frame every later screen sits in.

| Part | File | What it does |
| --- | --- | --- |
| Entry | `src/main.tsx` | Mounts the app under the router. |
| Session and routes | `src/App.tsx` | Asks the server who is signed in, then shows one of four states: loading, the server is not ready, sign-in, or the shell with its routes. |
| Server calls | `src/api.ts` | Every call to the Keystone server. See "The API it expects". |
| Sign-in | `src/SignIn.tsx` | Lists the people on the install. The person picks their own and types a password. Also the "server is not ready" screen. |
| Frame | `src/Shell.tsx` | The sidebar, the header and the main area. |
| Screens | `src/pages.tsx` | The home screen, the placeholder for screens later items build, and the page-not-found screen. |
| Theme | `src/theme.ts`, `src/ThemeSwitch.tsx`, `public/theme.js` | The choice of dark, light or follow the system. |
| Mark and icons | `src/Mark.tsx`, `src/Icon.tsx` | The mark and the line icons, drawn as in the mockups. |
| Styles | `src/styles.css`, `vite.config.ts` | The layout, and the plugin that turns the design tokens into CSS variables. |

### Routes

Addresses are real paths (decision [0071](decisions/0071-client-side-routes-are-real-paths.md)).

| Path | Screen |
| --- | --- |
| `/` | Home: the mark, a greeting by time of day and the person's name. |
| `/signin` | Goes to `/` when signed in. Signed out, every path shows sign-in and keeps its address. |
| `/search`, `/chats`, `/projects`, `/artifacts`, `/agents`, `/scheduled` | A placeholder saying the screen is not built yet. |
| anything else | Page not found, with a link back to the start. |

### The frame

Laid out as in `MainDark.html` and `MainLight.html`. The sidebar is 272 pixels wide and holds the mark and wordmark, a button to close it, New chat, the six places, an empty Recents list, the signed-in person with a sign-out button, and the theme switch. The main area has a header with the screen's title, and the content below it.

Below 760 pixels wide the sidebar becomes a drawer over the page, opened from the header and closed by its own button, by the dimmed area beside it, or by moving to another screen.

### Colours and the theme

Every colour is a CSS variable generated from `design/tokens-draft.json` when the interface is built (decision [0070](decisions/0070-design-tokens-become-css-at-build-time.md)). No colour value is written in the app's stylesheet or components.

The theme switch offers dark, light and system. Dark leads, as in the design. The choice is kept in the browser under `keystone.theme`. `public/theme.js` applies it before the page paints, so a person who chose light on a dark system sees no dark flash; it is a separate file because the server's content security policy allows no inline script.

### Fonts

The stylesheet names IBM Plex Sans, Fraunces, Source Serif 4 and IBM Plex Mono and falls back to system fonts (decision [0072](decisions/0072-fonts-are-named-not-bundled.md)). No font file is bundled yet and nothing is loaded from another site.

## The API it expects

Every server call is in `src/api.ts`. The API shell (P2-05) was being built at the same time, so all four paths are guesses, marked provisional in that file. The integrator wires them to the real routes by changing that file only.

| Call | Request | Answer |
| --- | --- | --- |
| `GET /api/people` | | 200 `{"people":[{"id","name"}]}`. Needs no session. |
| `POST /api/sign-in` | `{"person","password"}` | 200 `{"person":{"id","name"}}` and the session cookie; 401 refused; 429 wait |
| `POST /api/sign-out` | | 204 |
| `GET /api/me` | | 200 `{"person":{"id","name"}}`; 401 not signed in |

`person` in the sign-in request is the tenant id of the person picked from the list. The session is an HttpOnly cookie the server sets; the page never sees or stores a token.

How the app reads an answer:

- 404 or 405 on any call, a network error, or a 200 that is not JSON: the server is not ready. The app shows that state with a "Try again" button. A 200 that is not JSON matters because the server's fallback, once added, would answer an unknown `/api/` path with the page itself.
- 401 on `me`: signed out, show sign-in. 401 on sign-in: wrong password.
- 429 on sign-in: too many wrong passwords, wait and try again.

## The server change this needs

`cmd/keystone/serve.go` serves `web/dist/` with a plain file server today. For client-side routes it must answer a `GET` or `HEAD` for a path that is not a file in `web/dist/` and does not start with `/api/` with `index.html` and status 200. Unknown paths under `/api/` must stay 404 with a JSON body. The interface lane did not edit the server; the change is described in the journal entry for P4-01.

Until then, navigation inside the app works, and reloading on any screen but `/` answers 404.

## What comes next

- P4-04 builds the chat screen inside the main area.
- Passkey sign-in in the browser, once `signin.passkeys` is proven (see [signin.md](signin.md)).
- A standing check that rebuilds `web/dist/` where Node is present and fails if the result differs from what is committed (decision 0005).
- Bundled font files.
