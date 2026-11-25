# 0071. Screens in the interface have real paths, and the server falls back to the page

November 24, 2026 · decided in P4-01

## The question

The interface routes in the browser. Does a screen's address live in the path (`/chats`) or after a hash (`/#/chats`)?

## The decision

In the path. The server must answer a `GET` for any path that is not a file in `web/dist/` and does not start with `/api/` with `index.html`, so that reloading a screen or opening a link to one loads the app, which then shows the screen. The interface lane does not edit the server: the exact change is written in the journal entry for this item, for the integrator.

Until that fallback is in, moving between screens inside the app works, and reloading on any screen but the first answers 404.

## Why

- Addresses read as they do in the mockups and in Claude.ai, and a link to a chat can be shared and bookmarked as it is.
- The hash is left free for a place inside a screen.
- The fallback is a few lines in one handler, and P2-05 is rewriting that handler now.

## What it costs

- A mistyped address returns the app with status 200 and the app says the page was not found, so a 404 is no longer a 404 for a person's browser.
- Unknown paths under `/api/` must still answer 404 as JSON, or the app would read a page where it expected an answer.
