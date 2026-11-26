# 0432. The usage screen's bars are inline SVG from the tokens, with no chart library

November 25, 2026 · decided in P11-02

## The question

Settings → Usage shows the month's spend against the cap as a bar, and a small bar of each line's cost in the breakdowns. The page is served under a content security policy of `default-src 'self'`, which refuses a `style` attribute, and the settings screens have none. Should the bars come from a chart library, from a `progress` or `meter` element, or be drawn by hand?

## The decision

Each bar is an inline SVG with a `viewBox` of 1000 by 10 stretched to its box: a track rectangle and a filled rectangle whose width is an attribute, and for the cap a tick at the warning threshold. The fills are classes in `usage.css` that read the tokens: `--ok` under the threshold, `--accent` past it, `--err` at the cap, on `--sunk`. The cap's bar is an image with a sentence for a screen reader; the small bars are hidden from it, because the numbers beside them say the same. No package was added.

## Why

- A width set as an SVG attribute is not a style, so the policy stays as it is.
- Two kinds of bar are all the screen needs. A chart library would be the largest thing on the page and would need a decision record of its own, a check that it loads nothing from another site, and its colours mapped to the tokens.
- `progress` and `meter` are styled differently by each browser engine and cannot carry the threshold's tick. The inspector's `progress` bar was fine for one value and no mark.

## What it costs

- A chart over time, such as spend by day, would be drawn by hand the same way, or would bring the library question back.
- The bars have no hover. The numbers are in the table beside them.
