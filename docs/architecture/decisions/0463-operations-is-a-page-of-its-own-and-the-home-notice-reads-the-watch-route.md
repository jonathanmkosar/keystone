# 0463. Operations is a page of its own, and the home screen's notice reads a route of its own

November 26, 2026 · decided in P11-03

## The question

The item says Settings → Operations, or a page of its own linked from Settings and the sidebar, and a notice on the home screen when a watch level is crossed. Where does the screen live, and what does the home screen read to know?

## The decision

- **A page at `/operations`**, in the sidebar after Memory as `Operations.html` puts it under SYSTEM, and a tab on Settings that goes to it (`/settings/operations`). It is wider than the settings column, as the mockup is: its tables have five to seven columns.
- **The home screen reads `GET /api/v1/operations/watch`**, which returns only the limits at or past their watch level. It is read once when the home screen opens; nothing polls. The notice lists each crossed limit in a sentence and links to Operations; with none, or when the server cannot say, there is no notice.
- **Trends are drawn as inline SVG lines** beside the bars, as the usage screen's bars are (decision [0432](0432-the-usage-bars-are-inline-svg.md)): a `polyline` whose points are attributes, coloured from the tokens by class.

## Why

- A settings tab is a column 860 pixels wide; seven columns of models or traces do not fit it, and Operations is a place to look, not a value to set.
- The full report reads tasks, schedules, model health and traces as well as the limits; the home screen needs only the limits, and opening a new chat should not wait on twenty traces. `Watch` reads the limits with the same function as the report, so the notice and the page never disagree.
- One read on opening the home screen is enough for limits that move over hours and days, and costs nothing while a person chats.

## What it costs

- The page and the notice are provisional until the settings mockups (P1-07) exist; the sidebar's SYSTEM group of the mockup, with Admin, is not drawn, since there is no Admin page.
- A limit crossed while the home screen is open shows on the next visit, not at once.
