# 0514. Notebook entries, drafts, decisions and built apps are counted and left in New Keystone

November 26, 2026 · decided in P12-03

## The question

Beside turns and routines, New Keystone's database holds notebook entries (`memories`), decisions on publishing builds, drafts of calendar events and emails, build proposals, the apps it built (`artifacts`), reports on them, trial records and its own wiki tables. The item says to decide what becomes of each and record it, and recommends: memories and decisions as a source for the memory screen only if the knowledge store takes sources, otherwise left out and listed; drafts and proposals left out and listed; artifacts as Keystone artifacts if their kinds map.

## The decision

None of them is brought. Each table's rows are **counted in the report**, with what becomes of them, and notebook entries are counted by their state.

- **Notebook entries** are not added to the knowledge store.
- **New Keystone's artifacts are not Keystone artifacts.** Each one's summary, state and number of files go in the note of the chat its build became.
- **Decisions, drafts, proposals, reports and trial records** are left. The chat a proposal or a draft was made in is brought, like any chat.
- **New Keystone's own wiki tables** are left: the wiki is Waymarks.

## Why

Notebook entries:

- New Keystone gives each entry a state. `waymarks` means the note was sent to Waymarks and the row is only a receipt. Those notes are in the knowledge store Keystone reads already; adding them again would put each fact there twice, as two sources.
- `suggested`, `dismissed` and `forgotten` entries are ones the person did not keep, or took back. Bringing them would undo a choice.
- That leaves `active` entries, from before Waymarks was connected. Adding them means a write to the person's knowledge store. For Waymarks that is a call over the network with the intake token, from a command that otherwise touches one database file and calls nothing. The import's promise, that it writes one tenant's store and calls no model, no knowledge store and no network, is worth more than a handful of notes the person can tell Keystone again in one message, which the memory pass then keeps with a source that links to a chat.
- The report's count by state tells the owner how many there are of each, so he can decide with the number in front of him.

Artifacts:

- A Keystone artifact is a document with content and versions: code, HTML, React, SVG, Mermaid or Markdown, drawn in the panel. A New Keystone artifact is a built app: a digest, a summary, a state, check evidence and the names of up to forty files. The files are on New Keystone's disk and not in its database, so there is nothing to draw.
- A Markdown artifact made from the manifest would be a page the import wrote, listed among the person's artifacts as if a model had made it. The note on the build's chat says the same thing in one sentence.

The rest:

- A decision is a yes or no on publishing one build on one machine. A draft is an event or an email that was never sent, bound to New Keystone's own approval flow. None has a place in Keystone's record, and making one (an approval already answered, a draft in Gmail) would be a side effect outside Keystone that nobody asked for.

## What it costs

- Notes that live only in New Keystone's local notebook do not reach Keystone's memory until the person says them again. The dry run says how many there are.
- A built app is a sentence in a chat's note here. To keep the app, keep New Keystone's files.
- If the owner wants the active notes brought, that is a small addition: one source through the knowledge store's own interface, behind a setting, for a store that takes sources.
