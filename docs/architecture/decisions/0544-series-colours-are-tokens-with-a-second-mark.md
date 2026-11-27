# 0544. A chart's series colours are the design's tokens, each with a second mark, until the design has a chart palette

November 26, 2026 · decided in P9-04

## The question

A chart with several series needs colours that can be told apart, in the light theme and the dark, by people who see colour differently. Every colour in the app is a token from `design/tokens-draft.json`, and the tokens have no chart palette: they are grounds, inks, the copper accent, and one colour each for links, success and errors. There is no mockup of a chart. What colours does a series get?

## The decision

- **Six series colours, all existing tokens, in a fixed order**: `accent`, `link`, `strong`, `ok`, `ink`, `err` (classes `tone-0` to `tone-5` in `visuals.css`). A series keeps its colour by its place in the block. Past six, set only by raising `visuals.chart_max_series`, they repeat.
- **Colour is never the only thing that tells two series apart.**
  - Lines have a pattern each: solid, dashed, dotted, and so on (`dash-0` to `dash-5`).
  - Points of a scatter chart have a shape each: circle, square, diamond, two triangles, bar.
  - Bars stand in the legend's order inside each group, with a gap between them.
  - A pie's slices are named in the legend with their share, and parted by a gap in the ground's colour.
  - There is always a legend for two series or more, a note on the pointer with every series' name and value, and the data as a table one click away.
- **Text is never in a series' colour**: labels, values and legends are in the ink tokens, with a coloured mark beside them.
- **Mermaid is given the same tokens**: `mermaidTheme` in `visuals.ts` maps them to its `base` theme (boxes in `soft` with an `accent` rule, lines in `strong`, text in `ink`), so a diagram wears the theme too.
- **This is provisional.** The question for the owner is whether the design system (P1-02) should add a chart palette, chosen with the workspace mockups (P1-06) and checked for colour-blind separation.

## Why

- **No colour is written by hand in the app**, and a palette invented here would be a design decision made in the wrong lane. The tokens are what the design has approved so far, drafts as they are.
- **The order was chosen by measuring.** The six were run through a palette check (adjacent pairs under simulated colour-blindness, contrast against the card's ground, in both themes). In this order every adjacent pair is apart for the three common kinds of colour-blindness (worst pair 10.7, light theme), and every colour has at least 3:1 against the ground. `link` beside `ok` and `ok` beside `err` did not pass, so they are not neighbours.
- **It still fails two checks, which is why the second mark is not optional.** The tokens are muted on purpose: four of the six read as nearly grey to the check, and in the light theme `strong` and `ok` are closer than full colour vision should have to tell apart (14.2, where 15 is the floor). A pattern, a shape, a place and a name carry what the colours cannot.
- `ok` and `err` are the design's colours for success and failure, and are used here as plain series colours. With six tokens to choose from there is no way round that; they are fourth and sixth, so a chart of three series uses neither.

## What it costs

- Charts are quieter and less distinct than a real chart palette would make them, most of all with five or six series.
- A reader may take the green and the red for good and bad.
- When the design adds chart colours, `visuals.css` changes six lines and this record is replaced.
