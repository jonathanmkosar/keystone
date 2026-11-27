# Inline visuals

Folders: `internal/visuals`, `internal/turn/visuals.go`, and in the web interface `web/app/src` (`chart.ts`, `visuals.ts`, `InlineVisuals.tsx`, `visuals.css`, a mode of `sandbox/mermaid.ts`, a few lines of `Markdown.tsx` and `Conversation.tsx`) with `web/app/test/chart.test.ts` and `visuals.test.ts`. Item P9-04.

Charts and diagrams in a reply. A model writes a chart as a fenced `chart` block of JSON and a diagram as a fenced `mermaid` block; the chat screen draws each in the theme's colours, at a set height, with its source one click away, and opens it larger over the page. Nothing a model writes runs in the app's own page: a chart is drawn by the app from checked numbers and labels, and a diagram by Mermaid inside a sandboxed frame of no origin.

Decisions [0540](decisions/0540-a-chart-is-a-small-json-object-in-a-fenced-chart-block.md) to [0544](decisions/0544-series-colours-are-tokens-with-a-second-mark.md) say why it is built this way. There is no mockup of a chart or a diagram in a reply, so the look is provisional until the workspace mockups (P1-06) are approved.

## The two formats

| Block | Is | Drawn by |
| --- | --- | --- |
| ` ```mermaid ` | A diagram, in Mermaid's own languages: flowcharts, sequence diagrams and the rest. | Mermaid, in a sandboxed frame. |
| ` ```chart ` | A chart: one JSON object, below. | The app's own code, as SVG. |

Any other fenced block is a code block, as before.

**The chart format** (decision [0540](decisions/0540-a-chart-is-a-small-json-object-in-a-fenced-chart-block.md)):

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
| `type` | `bar`, `line`, `area`, `pie` or `scatter`. |
| `title` | Text, needed. |
| `x.title`, `y.title` | Text, optional. |
| `x.labels` | The names along the horizontal axis, or a pie's slices; needed for every type but scatter. A number is read as its digits. |
| `series` | One or more, each with a `name` no other has. |
| `series[].values` | For bar, line, area and pie: one finite number for each label. |
| `series[].points` | For scatter: `[x, y]` pairs of finite numbers. |
| A pie | One series, no value below zero, a sum above zero, and no more slices than `visuals.chart_max_series`. |

Keys the format does not name are ignored. Anything else is refused with one sentence, and the block is shown as its source with that sentence.

## What the model is told

`internal/turn/visuals.go`, one line in `internal/turn/context.go`. Decision [0542](decisions/0542-the-model-is-told-about-visuals-in-the-standing-instructions.md).

While the person's `visuals.draw` is on, the words of `visuals.instructions` are added to the standing text of every call, after a blank line and before the search-first rule: when a visual helps, and the two formats, in about 140 words. They are the same in every call, so the standing start stays one a provider can keep. Off, or with the words emptied, the model is told nothing.

## In the reply

`web/app/src/InlineVisuals.tsx`, `visuals.ts`, `visuals.css`.

- **`Markdown.tsx`** records whether a fenced block's closing fence was found, and offers each block to a drawer if a screen gave one (`FenceDrawer`); with none, a block is a code block. **Only the conversation gives one**: `Conversation.tsx` wraps a reply's text in `<InlineVisuals streaming=…>`. A shared page (`Shared.tsx`), a task's note, a sub-agent's result and a Markdown artifact give none, so there a chart or a diagram stays its source as text. `verify` holds that.
- **`planBlock`** in `visuals.ts` decides what a block is drawn as:

| The block | Is drawn as |
| --- | --- |
| Not `chart` or `mermaid`, or `visuals.draw` is off, or the settings could not be read | A code block. |
| No closing fence yet, while the reply streams | A placeholder: the visual's heading with "being written", and an empty place of the height it will draw at. |
| No closing fence, and the reply has ended | Its source, with "The reply ended before this chart was finished." |
| Larger than `visuals.max_block_bytes`, or empty | Its source, with the reason. |
| A whole `chart` block | The chart, or its source with the format's sentence when it is not a chart. |
| A whole `mermaid` block | The diagram's frame, or its source with a note when Mermaid cannot read it or the frame does not answer within `visuals.diagram_wait`. |

- **Nothing jumps** (decision [0543](decisions/0543-a-visual-holds-its-place-at-a-set-height.md)): a chart is `visuals.chart_height` tall and a diagram's frame `visuals.diagram_height`, the placeholder is the same height as what replaces it, and the frame never changes height: the diagram is fitted inside it. Heights are attributes, since the page's policy refuses a style attribute.
- **A block that does not draw is never an error screen**: a line with the reason, "This chart could not be drawn. Its type is "radar"; a chart is one of bar, line, area, pie, scatter. Its source is below.", and the source as a code block with its copy button.
- **The frame of a visual** has a heading (what it is, its title) and buttons: **Data** (a chart: a sentence that sums it up, and its numbers as a table), **Source** (the block as a code block), **Save** (a diagram: keeps it as a Mermaid artifact of the chat and opens it beside the chat; then "Open artifact"), and **Larger**, which lifts the box over the page at the size of the window, holds its place in the reply, and is closed by the button, Escape or the ground around it. At phone width the buttons show their pictures and keep their names for a screen reader.
- **The settings** are read through `GET /api/v1/settings` when a chat is opened, and kept between chats.

## Charts

`web/app/src/chart.ts`, which imports nothing.

| Function | Does |
| --- | --- |
| `parseChart(text, limits)` | Reads and checks a block: a `Chart`, or the sentence that says why not. |
| `layoutChart(chart, width, height)` | The chart as plain shapes in a box: bars, lines, areas, slices and points as paths and places, ticks with round numbers, axis titles, the legend set in rows, and the places a pointer can rest with what it is told there. |
| `tipFor(target, scene)` | Where the pointer's note goes so that it stays inside the drawing. |
| `chartSummary(chart)`, `chartTable(chart)` | The chart in a sentence or two, and as a table. |
| `niceScale(lo, hi)` | A scale with round ends and steps. |

`InlineVisuals.tsx` measures the width it has and draws the layout as SVG elements built by React. A label is only ever a text node; no markup is set from text anywhere.

- **Bars** stand on zero, at most 24 pixels wide, round at the value's end, with a gap between neighbours; one below zero hangs from the line. **Lines** are 2 pixels with a point at each value when there is room. **Areas** are a line with a light wash down to zero, laid over each other, not stacked. **A pie** starts at twelve o'clock and goes round in the block's order, each share in the legend. **Scatter** has a scale on each axis.
- Bar and area scales hold zero; a line's and a scatter's need not. An axis that reaches ten thousand writes its numbers compactly (10K).
- **Accessible**: the SVG is an image whose name is the title and the summary; the marks inside are hidden from a screen reader; the Data panel is a real table with row and column headings. Resting the pointer on a label's band, a point or a slice shows a note with every series' name and value.
- **Colours** (decision [0544](decisions/0544-series-colours-are-tokens-with-a-second-mark.md)): the tokens `accent`, `link`, `strong`, `ok`, `ink`, `err` in that order, and a second mark for each series: a line's pattern, a point's shape, a bar's place, a slice's name. Text is in the ink tokens. The tokens have no chart palette; this is provisional.

## Diagrams

Decision [0541](decisions/0541-charts-are-drawn-by-the-app-and-diagrams-in-a-frame-of-no-origin.md). `internal/visuals`, `web/app/src/sandbox/mermaid.ts`.

**The page.** `GET /api/v1/visuals/diagram?theme=dark|light` serves one page, the same for every diagram, built by `visuals.DiagramPage`: a transparent ground, an empty root, and the Mermaid renderer of the artifact sandbox ([artifacts.md](artifacts.md)) written into it as its one script, with `data-ks-inline` on its body. It holds no diagram and nothing of any person. A build whose renderer does not know the messages (`visuals.CanDraw`) gets a page that says so and holds no script. It carries an ETag and `Cache-Control: private, no-cache`, so megabytes of renderer are sent once.

**Its policy**, `visuals.Policy`: the artifact sandbox's for a Mermaid artifact, with the script named by its digest and the frame's permissions cut to one:

    default-src 'none'; script-src 'sha256-<the page's one script>'; style-src 'unsafe-inline';
    img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none';
    worker-src 'none'; manifest-src 'none'; form-action 'none'; base-uri 'none';
    frame-ancestors 'self' http://<host> https://<host>; sandbox allow-scripts

Nothing is fetched or sent, no script runs but Keystone's renderer, the page has no origin (no cookie, no storage, no API), and only Keystone's own pages may frame it. The main page's policy, `default-src 'self'`, is not touched, and it refuses the frame any address that is not Keystone's.

**The frame.** `<iframe sandbox="allow-scripts" referrerpolicy="no-referrer">`, made when the diagram is within 600 pixels of the window, and made again when the theme changes.

**The messages**, in `visuals.ts`, read with the same code on both sides. Each carries the channel `keystone-diagram`.

| From | Message | Read as |
| --- | --- | --- |
| The frame | `ready` | The renderer is listening. The page sends the diagram. |
| The page | `draw`: the block's source, the theme, thirteen colours from the tokens | `readDrawMessage`: only from the frame's parent; the theme is one of two, and each colour must be a hex colour or the message is dropped. |
| The frame | `drawn` | The page takes away "Drawing the diagram". |
| The frame | `problem`, with Mermaid's words | `readFrameMessage`: only from this diagram's own frame; the words are cut to 600 characters and the first line of them is shown, as text, in the note above the source. |

The renderer draws at Mermaid's `strict` level with its `base` theme and the tokens (`mermaidTheme`): boxes in `soft` with an `accent` rule, lines in `strong`, text in `ink`. The page's own style fits the drawing inside the frame.

**Saving one.** `POST /api/v1/chats/{id}/visuals/diagrams` with `{"title","content"}` makes a Mermaid artifact of the chat, as version 1, through `artifacts.Create`: 201 with the artifact and the version; 400 `refused` with the reason (empty, larger than `artifacts.max_bytes`, a title too long); 404 for a chat that is not the person's. A chat held for sensitive content marks it sensitive, as any artifact of it is. The title is the diagram's own (its front matter's `title`, or its `accTitle`), or "Diagram".

The package reads no setting: `visuals.Options` holds the artifact options `app.ReadConfig` read. It imports `net/http` for its routes and opens no connection.

## Settings

In `settings/visuals.json`, lane `visuals`.

| Setting | Scope | Default | What it does |
| --- | --- | --- | --- |
| `visuals.draw` | tenant | true | Whether charts and diagrams are drawn, and whether the model is told about them. |
| `visuals.instructions` | tenant | the two formats, in about 140 words | What the model is told. Empty tells it nothing. |
| `visuals.max_block_bytes` | install | 20000 | The largest block drawn. |
| `visuals.chart_max_series` | install | 6 | The most series, and the most slices of a pie. |
| `visuals.chart_max_points` | install | 200 | The most points of one series. |
| `visuals.chart_label_max_chars` | install | 80 | The longest title, label or name. |
| `visuals.chart_height` | tenant | 320 | A chart's height in pixels, at least 120. |
| `visuals.diagram_height` | tenant | 360 | A diagram's frame's height in pixels, at least 120. |
| `visuals.diagram_wait` | install | 20s | How long a diagram's frame is waited for. |

## The test tenant

`fixtures/test-tenant/turns/inline-visuals.json`, three recorded turns with invented numbers, and the chat `chats/04-kayak-hires.json`, "Kayak hires in charts", which holds the first two:

- "Chart the kayak hires at the boathouse for the season." A bar chart of two series.
- "Draw how a kayak hire goes, from arriving to handing the paddle back." A flowchart.
- "Show me one of each kind of chart, and a second diagram." A line, a pie, an area and a scatter chart, a sequence diagram, and a block that is not a chart, to show what the page does with one.

## Checked by

- `npm test` in `web/app` (Node's own test runner, decision 0501), 27 tests:
  - `chart.test.ts`: a chart of each type is read; number labels and untidy space; unknown keys ignored; each refusal and its sentence; every value a finite number; a pie's rules; the settings' limits; round scales; **what the page builds for each type** (bars standing on zero and hanging below it, a line a series, an area's wash, a pie's slices and shares, a scatter's points on two scales); phone width, with labels thinned and the note inside the drawing; the summary and the table; nothing in a layout but paths, numbers and plain text; and every chart block in the test tenant's recorded turns is one the page draws.
  - `visuals.test.ts`: the settings read from the registry's values, and one that does not read draws nothing; only `chart` and `mermaid` are visuals; a whole block draws and anything else is code; switched off, all code; **a block still being written is a placeholder and draws once closed**; too large or empty; a diagram's title; the tokens' colours and nothing that is not a colour; the frame reads a draw request and nothing else; the page reads three states and a problem only as bounded text.
- `go test ./internal/visuals`: the page holds the renderer and no diagram, in each theme, and a theme that is neither reaches nothing; the policy runs the one script by its digest, fetches nothing, gives no origin, lets only Keystone frame it, and a crafted Host does not reach it; a build that cannot draw says so and runs nothing; the route's headers, its ETag answered with 304, and 401 with no session; saving makes a Mermaid artifact with its request id, each refusal, another person 404 with nothing written, and a held chat's is sensitive.
- `go test ./internal/turn`: the standing start begins with the standing text and then the instructions, the same in two calls of one chat, and the recorded turns answer with a chart and a diagram; switched off, emptied, and a person's own words.
- `verify`, a new standing check, **`inline visuals`** ([tools.md](tools.md)): from the source, the main page's policy is still `default-src 'self'`; `chart.ts` and `visuals.ts` import nothing; `InlineVisuals.tsx` never sets markup from text, never evaluates, never imports Mermaid, and gives its one frame `allow-scripts` alone; the renderer draws at `strict` and hears only its parent; no file but `Conversation.tsx` draws with `InlineVisuals`, and none but `InlineVisuals.tsx` gives a drawer; no chart library is a dependency; the nine settings are in the registry. With Node, the 27 tests. On the real server with an invented person and a model that plays the test tenant's recorded turns: the chart and diagram replies, the standing start with the instructions and the same twice, nothing told with `visuals.draw` off, the diagram's page and its policy, 401 without a session, saving a diagram and another person refused. `internal/visuals` has its `componentDocs` line and its `offline` line in the same file.
- In a browser, against the server built from this branch on `127.0.0.1:7984` with a data folder in the worktree, as the test tenant:
  - **The desktop app's browser pane**: the bar chart in dark and light; the pointer's note; the Data panel; Larger and Escape; the third recorded turn streamed, with the placeholder and the drawn visual the same height (358 pixels for a chart, 398 for a diagram); line, pie, area and scatter in light; the block that is not a chart as its source with its sentence; at phone width in light and dark with no page that scrolls sideways; Save made the artifact and opened it beside the chat; the shared page showed eight code blocks, no figure and no frame. The diagram's page opened on its own drew the flowchart in dark and the sequence diagram in light when sent a draw message, answered `drawn`, answered `problem` to a diagram that does not read, ignored a message on another channel and one with a colour that is not a colour, and had the origin `null` with its cookie refused. **The frame inside the reply stayed empty in that pane**, which refuses a sandboxed frame's request (`ERR_BLOCKED_BY_CLIENT`); after `visuals.diagram_wait` the reply showed the source with "Its frame did not answer in time."
  - **Microsoft Edge, headless, driven over its debugging port** with a throwaway profile: both diagrams drew inside their frames in the reply, in dark and in light at 1280 pixels wide and in dark at 375, on the reply's own ground; Larger made the frame 1246 by 830 without loading it again; no request of a frame was blocked.

## Provisional

- **The look of all of it** waits on the workspace mockups (P1-06): the frame, the heading and its buttons, the chart's marks, the placeholder, the larger view.
- **The series colours** wait on a chart palette in the design system (decision [0544](decisions/0544-series-colours-are-tokens-with-a-second-mark.md)).
- **A diagram inside a reply has not been seen in Chrome, Safari or Firefox, or on a phone**: only in headless Edge and, alone, in the app's pane.

## Not here yet

- Charts and diagrams in a Markdown artifact or a research report, and on a shared page (which shows text only, by decision 0472's rule).
- Saving a chart as an artifact; downloading a visual as a picture.
- Panning and zooming a diagram; a wide diagram is small at phone width.
- Stacked bars and areas, gaps in a line, a time axis, a second axis.
- Moving between a chart's marks with the keyboard; the table is the keyboard's way to the numbers.
