# 0540. A chart in a reply is a small JSON object in a fenced `chart` block

November 26, 2026 · decided in P9-04

## The question

A model should be able to put a chart in a reply. What does it write? It could write SVG, a chart library's own configuration (Vega-Lite, Chart.js), Mermaid's chart types, or a format of Keystone's own. Whatever it is, the page has to check it before it draws anything, a model has to get it right from a paragraph of instructions, and a block that is wrong has to fail plainly.

## The decision

A chart is a fenced code block with the language `chart`, holding one JSON object:

    {
      "type": "bar",
      "title": "Kayak hires by month",
      "x": { "title": "Month", "labels": ["Apr", "May", "Jun"] },
      "y": { "title": "Hires" },
      "series": [
        { "name": "Single kayaks", "values": [42, 68, 95] },
        { "name": "Double kayaks", "values": [18, 30, 44] }
      ]
    }

| Part | Rule |
| --- | --- |
| `type` | One of `bar`, `line`, `area`, `pie`, `scatter`. |
| `title` | Text, needed. |
| `x.title`, `y.title` | Text, optional. |
| `x.labels` | The names along the horizontal axis, or a pie's slices. Needed for every type but scatter. A number is read as its digits, so `[2021, 2022]` works. |
| `series` | One or more, each with a `name` no other has. |
| `series[].values` | For bar, line, area and pie: one finite number for each label. |
| `series[].points` | For scatter, in place of values: `[x, y]` pairs of finite numbers. |
| A pie | One series, no value below zero, and a sum above zero. |

Limits are settings, never constants: `visuals.chart_max_series` (also the most slices of a pie), `visuals.chart_max_points`, `visuals.chart_label_max_chars` for every text, and `visuals.max_block_bytes` for the whole block. Keys the format does not name are ignored. Anything else that is not exactly this is refused with one plain sentence ("The series "North pier" has 3 values for 4 labels."), and the page shows the block's source with that sentence.

`web/app/src/chart.ts` reads and checks it (`parseChart`), and is the only thing that does.

## Why

- **Small enough to say in a paragraph.** The whole format fits in `visuals.instructions`, which every call carries. A library's configuration language would be a far longer instruction or a guess, and each of its features is something to check.
- **Data, not drawing.** The block holds numbers and labels. How they are drawn, in which colours and at what size is the page's business, so a chart follows the theme and the phone's width with nothing from the model, and nothing a model writes is ever markup, a colour or a style.
- **Not SVG.** Model-written SVG would have to be drawn as an image or in a sandbox (as an SVG artifact is), could not follow the theme, and has no data to show as a table.
- **Not Mermaid's charts.** Mermaid can draw pie and xy charts, but they would run through the diagram's frame, megabytes of renderer for six bars, with no table and no summary.
- **Strict on what it names, loose on what it does not.** A wrong type or a missing value is refused, because a chart that silently drops a series misleads. An extra key is ignored, because models add them (`"color"`, `"unit"`) and nothing is lost.
- **Numbers as labels are accepted** because years and weeks are the commonest labels and models write them bare.

## What it costs

- Five chart types, one vertical axis, no stacking, no gaps in a line, no time axis, no logarithmic scale. Each is a later change to the format, its checks and its instructions together.
- A model that knows a library's format may write that instead; it is shown as its source with the reason.
- The format is Keystone's own, so a chart copied out of a reply is JSON, not a picture. The table one click away is the portable form.
