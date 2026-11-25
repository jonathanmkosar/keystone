# 0301. The reading and interface fonts are bundled, Latin only

November 25, 2026 · decided by the owner, after decision 0300

## The question

Decision 0072 named the design's fonts and bundled none, so every device drew its own fallbacks: Georgia at 18px with a heavy bold on most machines. Decision 0300 made the fallbacks read better. The owner then agreed to fetch the fonts themselves. Which, how much, and from where?

## The decision

- **Source Serif 4** (replies) and **IBM Plex Sans** (the interface and reply headings) are bundled. Fraunces, used for the wordmark and the greeting, and IBM Plex Mono, used for code, are not, and keep their fallbacks.
- **Variable files with the weight axis, Latin and Latin Extended only:** Source Serif 4 upright and italic, IBM Plex Sans upright. Six WOFF2 files, about 270 KB, in `web/app/src/fonts/`, with their `@font-face` rules in `web/app/src/fonts.css` and each `unicode-range` as published, so a browser loads only what a page uses.
- **From Fontsource**, which packages the official releases of Adobe's Source Serif 4 and IBM's Plex. The files were copied out of the packages (versions in the header of `fonts.css`) and the packages were not kept as dependencies, so the build needs no network.
- **Served by Keystone itself**, compiled into the binary with the rest of `web/dist/`. The content security policy stays `default-src 'self'`: nothing is loaded from another site.
- **The licence travels with the files:** both fonts are under the SIL Open Font License 1.1, and its text is in `web/app/src/fonts/OFL-*.txt`.

This replaces decision 0072 for these two fonts. Decision 0300's sizes stay: replies at 17px on 28px lines, headings in the sans at semibold. With the real fonts, bold inside a reply is a true semibold.

## Why

- With the fonts present, every device draws the screen the design drew, and weight 600 is a real semibold instead of a fallback's bold.
- Only Latin and Latin Extended: Keystone's text is English, and the full families would be several times the size for alphabets no one here reads. A character outside the range falls back to a system font, line by line.
- Copying six files keeps the build offline and the dependency list short; the version and source are recorded beside them.

## What it costs

- About 270 KB more in the binary and in a first page load, cached after.
- A new version of either font is a manual copy. The header of `fonts.css` says where they came from.
- Greek, Cyrillic and Vietnamese text in a reply is drawn in a fallback serif.
