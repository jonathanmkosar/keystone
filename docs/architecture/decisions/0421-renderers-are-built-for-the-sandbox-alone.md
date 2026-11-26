# 0421. Mermaid and Sucrase are dependencies, built for the sandbox alone

November 25, 2026 · decided in P9-03

## The question

A React artifact is JSX, which a browser does not run, and a Mermaid artifact is a diagram's text, which needs Mermaid's renderer. The app must work offline, so nothing may come from a CDN. Which packages, and where do they go?

## The decision

Two npm packages from the public registry, with the lockfile updated: **`sucrase`** 3.35.1 (MIT), a small transform that turns JSX, TypeScript and ES imports into JavaScript, and **`mermaid`** 12.3.0 (MIT), three weeks old when it was taken. Neither has an install script, and neither is fetched at run time. React and React DOM are the app's own.

They are built by a second Vite configuration, `web/app/vite.sandbox.config.ts`, into two classic scripts with nothing split out, `web/dist/sandbox/react.js` and `web/dist/sandbox/mermaid.js`, after the main build: `npm run build` runs all three. The main page never imports them (the `artifacts` check in `verify` fails if a file outside `src/sandbox/` does), so the main bundle and the main page's policy are unchanged. The server reads each file from the build compiled into the binary and writes it into the sandbox page of a React or Mermaid artifact (decision 0422).

## Why

- **Sucrase** over Babel's standalone build (about 3 MB) and esbuild's WebAssembly build (about 10 MB, and WebAssembly needs another policy source): it does what a component needs in about 270 kB.
- **Mermaid** is the only renderer of its own syntax. It is large (5.2 MB as one script, 1.5 MB compressed) because it holds every kind of diagram, its layout engines and KaTeX; it is fetched only when a Mermaid artifact is shown, and the browser keeps the page (decision 0422). It is used at its strict security level.
- **Classic scripts with nothing split out**, because a page of no origin can run a script written into it but should fetch nothing, and a module or a chunk would be a fetch.

## What it costs

- About 150 packages in `node_modules` for whoever builds the interface, almost all Mermaid's. The Go build still needs only Go, since `web/dist/` is committed.
- `web/dist/` grows by 5.6 MB, and so does the binary.
- A React artifact may import only `react` and `react-dom`; libraries such as chart or icon packages are not there. The tool's description says so.
