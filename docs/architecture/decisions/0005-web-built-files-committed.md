# 0005. The web interface is React and Vite, with its built files committed and embedded

November 24, 2026 · decided in P0-03

## The question

What is the web interface written in, and how does it get into an install that is one binary?

## The decision

TypeScript and React, built with Vite. The source is in `web/`. The built files are in `web/dist/`, and they are committed. A small Go file, `web/embed.go`, compiles `web/dist/` into the binary, and `keystone serve` serves it. This follows Waymarks (its decision 0005).

Building Keystone needs Go only. Node is needed only by someone changing the interface, who rebuilds `web/dist/` and commits the result with the source change.

Until P4-01 builds the real interface, `web/dist/` holds one placeholder page, written by hand.

The page is served with a content security policy that allows files from Keystone itself and nothing else. Fonts, scripts and styles are files in `web/dist/`, never links to another site.

## Why

- `rebuild` works from an empty machine with one tool installed, and `verify` stays offline.
- The binary is still one file. There is no folder of web files to keep beside it.
- What is running is what is in the repo: the served files are in the commit.

## What it costs

- Built files in the repo make larger diffs. P4-01 marks them as generated in `.gitattributes` so reviews skip them.
- The source and the built files can drift apart. P4-01 adds a standing check that rebuilds the interface where Node is present and fails if the result differs from what is committed.
- A change to the interface means a new binary. In development, Vite's own server runs beside Keystone and passes API calls through to it.
