# 0070. The design tokens become CSS variables when the interface is built

November 24, 2026 · decided in P4-01

## The question

`design/tokens-draft.json` holds every colour for both themes. How do those values reach the interface without being typed again in a stylesheet?

## The decision

A small plugin in `web/app/vite.config.ts` reads the tokens file at build time and serves it as the module `virtual:tokens.css`: one CSS variable for each token, named as in the file (`--page`, `--accent`, `--c1`). Dark is on `:root`. Light applies under `prefers-color-scheme: light` when the person has not chosen, and under `data-theme="light"` when they have. The app's own stylesheet uses the variables only.

## Why

- The tokens file stays the one source. A colour changed there changes the interface at the next build, and no value is copied by hand.
- The output is an ordinary stylesheet file, which the server's content security policy allows. Injecting a style element at run time would be refused.
- No package is added: the plugin is a few lines in the build configuration.

## What it costs

- The tokens are fixed when the interface is built, not when it is served. A tenant's own theme (P11-01) will need another way in.
- The interface cannot be built without the `design/` folder beside `web/`.
