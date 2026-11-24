# Web interface

Folder: `web`

The web interface is compiled into the binary. `web/embed.go` embeds everything in `web/dist/`, and `web.Files()` hands it to the server with `index.html` at its root. `keystone serve` serves it (see [cli.md](cli.md)).

`web/dist/` is committed, so building Keystone needs Go only. Decision [0005](decisions/0005-web-built-files-committed.md) says why.

## What is there today

A placeholder: one page, one stylesheet and the mark. It says Keystone is running and uses the page and text colours from `design/README.md`, dark or light to match the system. It loads nothing from another site.

## What comes next

P4-01 builds the web shell in TypeScript and React with Vite, from the design tokens. Its build output replaces the placeholder in `web/dist/`. The Go side does not change.
