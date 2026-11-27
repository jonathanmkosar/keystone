# 0590. On a phone the composer's and the header's controls are rows of a sheet, drawn by each control itself

November 27, 2026 · decided in a fix the owner asked for from his phone

## The question

On an iPhone at 390 pixels the chat header held the title, the project chip, Hand off and Share, and the two icon buttons overlapped so neither read. The composer's row held the attach button, Research, the tools menu, the style picker, the model picker, the microphone and send: seven controls in about 300 pixels. The owner pointed at another app's composer as the shape to move to: a roomy box, and one row under it with a round `+` and the model's chip on the left and the microphone and a round send button on the right, with everything else behind the `+`. How should the controls get there without changing what any of them does?

## The decision

- **At 760 pixels and under** (the width at which the sidebar is already a drawer), the composer's row is `+`, the model picker as a chip with the model's short name, the microphone and send. The `+` opens a sheet with a row for each of: files or photos, attach a chat, Research, connectors and style. The chat header is the sidebar button, the title, and one `...` button that opens a sheet with Share, Hand off, the project, "Continued from" and "Continued in".
- **Each control draws its own row.** `PhoneSheet.tsx` gives a sheet a React context; a control that finds itself in one (`useSheet()`) draws a `SheetRow` (its icon, its name, its state) in place of its usual button, and its own picker opens under the row in the sheet while the other rows step aside. A control calls `done()` when a choice is made, which closes the sheet. The pickers, the calls and the state are the controls' own, unchanged.
- **The sheet stays in the page while closed**, hidden, and is drawn into the body with a portal: the controls keep what they have read, and the sheet sits outside the composer's form, so Enter in a box inside it cannot send the message.
- **The layout is chosen in script** (`useNarrow()`, a `matchMedia` on the same width), not by drawing both and hiding one in CSS, so each control is mounted once and reads its data once.
- **The words of each row's state, and the model's short name, are in `composerSheet.ts`**, a file with no imports, tested by Node's own runner.
- **On a phone nothing takes the focus by itself**: the composer no longer takes it when a chat opens (`mayTakeFocus()`), and the sheet puts the focus on its first row, a button.

## Why

- A context and one branch in each control is the smallest change that keeps every control's behaviour: the alternative, lifting each control's state into hooks the sheet could read, would have rewritten seven components the other lanes also touch.
- Mounting each control once avoids double reads (the tools menu and the style picker read on mount) and two copies of state that could disagree.
- Rows that say their state ("Research: off", "Connectors: 1 of 2 on", "Style: Concise") are what lets the controls leave the row without the person losing sight of what is on.
- A portal and a hidden sheet were the plainest way to be outside the form and keep the controls' state; the attach picker's earlier fix showed that a menu opening upwards from the composer fails once the keyboard is up, so the sheet is anchored to the visible viewport as that picker is.

## What it costs

- Seven controls (`AttachButton`, `AttachChatRow`, `ResearchControl`, `ConnectorMenu`, `StylePicker`, `ShareControl`, `ChatProjectChip`) each carry a second way of drawing their trigger. A new composer control has to do the same to reach the sheet; one that does not still draws, in the sheet, as its usual button.
- Research is one tap further away on a phone, and its state shows only in its row and in the line above the composer while it is on.
- The connectors' switches do not close the sheet, since a person may flip several; every other choice does.
- There is no web search switch in the composer today (web search is a tool the model uses, with no per-chat switch), so the sheet has no such row.
- Desktop is unchanged, but the composer's props changed: the model picker is passed as `model`, apart from `beside`.

`verify` fails if a record in `decisions/` is not linked from `overview.md`.
