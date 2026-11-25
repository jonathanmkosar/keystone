# Web interface

The React and TypeScript app is in `app/`, built with Vite from the design tokens in `design/`. Its built files are in `dist/` and are committed, so building Keystone needs Go only; `embed.go` compiles them into the binary. To change the interface: `cd web/app`, `npm install`, `npm run build`, and commit the source and `web/dist/` together. See `docs/architecture/web.md`.
