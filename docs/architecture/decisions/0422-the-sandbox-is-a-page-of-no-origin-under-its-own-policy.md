# 0422. An artifact is drawn in a page of no origin, under its own policy, holding all it needs

November 25, 2026 · decided in P9-03

## The question

An artifact's HTML or React runs script the model wrote, perhaps from content it read. It must never reach the person's session, cookies or API, nothing may be fetched from outside, and the main page's policy, `default-src 'self'` (decisions 0150 and 0301), must not loosen. How is it drawn?

## The decision

- **A route of its own.** `GET /api/v1/artifacts/{artifact}/versions/{version}/sandbox` serves one page per version, signed in like every route, with a policy that replaces the server's for that answer alone: `default-src 'none'`, script only from the page itself (`'unsafe-inline'` for HTML, React and Mermaid, none for the rest), styles inline, images, fonts and media from `data:` and `blob:` only, no connection, frame, worker, manifest, form action or base, `frame-ancestors` Keystone's own address, and the `sandbox` directive with `artifacts.sandbox`'s permissions. `X-Frame-Options: SAMEORIGIN`.
- **An iframe with `sandbox`** whose permissions are the server's filtered to an allowed list in `ArtifactFrame.tsx`. Same-origin, top navigation and popups are never allowed: the setting refuses them and the frame drops them.
- **Everything in the page.** A React or Mermaid page holds its source as JSON and its renderer written in, made safe to sit inside a script element. SVG is an image from a `data:` address. Markdown and code are drawn in the app by its own reader and a code block, and never run anything.
- **An ETag and `private, no-cache`**, so a page that holds Mermaid's 5 MB is kept by the browser and asked about each time.

## Why

- The `sandbox` directive, with no `allow-same-origin`, makes the page one of no origin whether it is framed or opened on its own: its script reads no cookie and no storage, and a request it made would not be the person's. In the browser check its origin was `null`, `document.cookie` and `localStorage` threw, and a fetch of the session was refused by the policy.
- A page of no origin that fetches nothing has no question left about which address `'self'` means in it, whether the session cookie goes with its requests, or what Host a tunnel sends: it makes none. An earlier draft loaded the renderers from `/sandbox/` with `<script src>`; the desktop app's browser pane refuses every request a page of no origin makes, and writing them in removed the request.
- The main page's policy is untouched, and so is its frame rule: the main page's `default-src 'self'` also refuses the frame any navigation to another site.
- A separate domain for artifacts, as some services use, would need a second name and certificate on the owner's tunnel. The sandbox directive gives the same isolation on one address.

## What it costs

- A React page carries 423 kB and a Mermaid page 5.2 MB, sent once and then answered with 304 until the version, the theme, the renderer or the policy changes.
- `'unsafe-inline'` is in the sandbox's policy. It is the page's own script, in a page of no origin that can reach nothing; the main page never allows it.
- The desktop app's built-in browser draws the sandbox pages when opened on their own, but not inside the panel's frame: it refuses requests from sandboxed frames. Ordinary browsers have not been checked yet.
