# 0543. A visual holds its place at a set height, and a diagram is fitted inside its frame

November 26, 2026 · decided in P9-04

## The question

A reply arrives a few words at a time. A chart block is a dozen lines of JSON before it is a chart, and a diagram's size is known only once Mermaid has drawn it, inside a frame the page cannot measure. The item asks that an unfinished block shows as text or a placeholder and draws once it is complete, without the page jumping. How tall is a visual, and when is that known?

## The decision

- **A visual's height is a setting, known before anything is drawn**: `visuals.chart_height` (320) for a chart and `visuals.diagram_height` (360) for a diagram's frame, with a heading of a fixed height above.
- **A block still being written is a placeholder of that height.** `Markdown.tsx` now records whether a fenced block's closing fence was found. While the reply streams, an unclosed `chart` or `mermaid` block is drawn as the visual's frame with "being written" in its heading and an empty place of the set height. When the fence arrives, the chart or the diagram's frame takes that place at the same height. In the browser check the placeholder and the drawn chart were both 358 pixels, and the placeholder and the diagram both 398.
- **A chart lays itself out inside the set height**, at the width it is given: its legend, its axes and its plot share the box, labels thin out when they would touch, and nothing is scaled, so text is 12 pixels at any width.
- **A diagram is fitted inside its frame**, whole: at its own size when it is smaller, scaled down by its `viewBox` when it is wider or taller. The frame never changes height, and never tells the page a size.
- **Open larger** lifts the visual's box over the page at the size of the window, and a place of the same height is held in the reply, so nothing under it moves. A chart lays itself out again in the larger box; a diagram's frame is the same frame, not loaded again.
- **Sizes are attributes** (`height` on the SVG and on the frame), not styles: the page's policy refuses a style attribute (decision 0432).
- A block whose reply ended without its closing fence is not a visual: its source is shown with "The reply ended before this chart was finished."
- A diagram's frame is made only when the diagram is within 600 pixels of the window; until then its place is held empty at the same height.

## Why

- **The only way not to jump is to know the height first.** A chart could be measured, but a diagram cannot until megabytes of script have run in another document; asking the frame for its size and resizing would move everything under it a second after it appeared, in every long chat, at every reload.
- **A set height suits a reply**: a visual is an aside in a column of text, and one that is 1,200 pixels tall pushes the answer off the screen. The person who wants it large has one click.
- **A placeholder, not the source as text**: half-written JSON scrolling past is noise, and its height is not the chart's.
- **No size from the frame** also means the frame has one less thing to say, and the page one less thing to trust (decision [0541](0541-charts-are-drawn-by-the-app-and-diagrams-in-a-frame-of-no-origin.md)).

## What it costs

- A tall or very wide diagram is small in the reply, and smaller on a phone: a flowchart of eight boxes in a row is hard to read at 345 pixels. Open larger helps on a wide screen and little on a phone; panning and zooming a diagram are not built.
- A small diagram sits in a frame taller than it needs.
- A chart with many series gives rows of its height to its legend.
- Opening a panel (the data, the source) makes the visual taller. That is the person's click, not the page's doing.
- The first time a page is loaded, a chat's visuals are code blocks for the moment before the settings are read. They are kept between chats after that.
