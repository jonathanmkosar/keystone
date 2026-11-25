# 0072. The interface names the design's fonts and does not bundle them yet

November 24, 2026 · decided in P4-01

## The question

The mockups are set in IBM Plex Sans, Fraunces, Source Serif 4 and IBM Plex Mono, loaded from another site. Decision 0005 says fonts are files in `web/dist/`. Does the first shell carry the font files?

## The decision

Not yet. The stylesheet names the four fonts first in each stack and falls back to system fonts. Where the fonts are installed the interface matches the mockups, and elsewhere it is set in the system's own faces. Nothing is loaded from another site either way.

## Why

- The owner wants Keystone running in a browser first. Choosing, licensing and subsetting four font families is its own piece of work.
- The fallback keeps the layout whole: sizes, weights and spacing are the design's.
- The fonts are one change later: files in `web/app/public/` and four `@font-face` rules, with no screen touched.

## What it costs

- The wordmark and the greeting are in Georgia or the system serif on a machine without Fraunces, which is not the look that was approved.
- The proof "the shell matches its mockups" cannot fully pass until the fonts are bundled.
