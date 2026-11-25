# P4-04. Chat screen

Status: provisional
Lane: interface
Depends on: P4-01, P4-03
Owner gate: Approval of the chat mockups (P1-04).
Built without the gate: Built from the draft mockups, marked provisional.
Scope: Composer, streaming markdown and code with copy, thinking, model picker, edit and retry with branch navigation, feedback, keyboard shortcuts, motion from the design system.
Proof: Each chat screen is checked against its mockup, light and dark, and the screen works against the test tenant.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan, November 25, 2026

The owner wants a chat that runs in the browser first. So this pass builds the screen a person needs to hold a conversation, and leaves the rest of the item's scope for a second pass.

Built in this pass:

- The sidebar lists starred and recent chats and opens one. "New chat" opens the empty state: the greeting and a composer.
- `/chats/<id>` shows the chat on its current branch: the person's messages as bubbles, replies as reading text with paragraphs, headings, lists, links, inline code and code blocks with a copy button, thinking as a block that opens, and tool calls as steps.
- The composer sends on Enter, and Shift and Enter makes a new line. While a reply streams, the send button is a stop button.
- The reply streams in as it arrives: text, thinking, tool steps. A model error or a failed call is shown in plain words under the reply.
- A dropped stream connects again with `Last-Event-ID` and picks up where it stopped.
- A title appears in the header and the sidebar when the server makes one.
- The chat's menu in the header: rename, star, delete.
- `/chats` lists every chat, starred first.
- Dark, light and system themes from the tokens, laid out as in `MainDark.html` and `MainLight.html`. No inline style, nothing from another site, no new package: the Markdown reader is a small one written here.

Left for a second pass, and said so in the result: the model picker, edit and retry with the branch switcher, feedback buttons, attachments, the tools menu, the thinking switch, the context meter, the share button, the turn inspector, citations, approval cards and sub-agent lanes (P7), and motion beyond the streaming cursor.

The contract is P4-03's: `docs/architecture/turns.md` and `docs/api/openapi.json` on the branch `chats/p4-03-chat-turn`. Every call stays in `src/api.ts`. The event-stream reader is built first against `GET /api/v1/stream/test`.

Proof: `npm run build`, `go run ./tools/rebuild` and `go run ./tools/verify` are green; the screen is opened in a browser as the test tenant, in dark and in light, and compared with the two mockups; a message is sent and its reply streams, where the server has the routes.

### Result, November 25, 2026

Provisional. Everything in the plan's "Built in this pass" list is built, against the P4-03 contract as it stood at commit `5e781f7` on `chats/p4-03-chat-turn`. `docs/architecture/web.md` has the chat screen's section and its calls. Decisions 0150 and 0151. Journal: `docs/journal/2026-11-25-interface-002.md`.

Proof so far:

- `npm run build`, `go run ./tools/rebuild` and `go run ./tools/verify` (15 of 15) are green.
- Against Keystone's own server, as the test tenant: sign-in, the frame, and the event reader on `GET /api/v1/stream/test`, from the start and from `Last-Event-ID`.
- Against Keystone's own chat and turn routes, on a build of the P4-03 branch at `cba624d` with this branch's page, made outside the repo, as the test tenant in a browser: the sidebar's three loaded chats, opening one, a new chat whose first message streamed its recorded reply with thinking and got its title, a second message in a loaded chat, rename, removing a star, and delete.
- Against a stand-in for the contract, in a browser, dark and light, wide and at phone width: list, open, new chat, a streamed reply with thinking, a tool step and Markdown, a dropped stream that came back with `Last-Event-ID`, a reload in the middle of a reply, stop, a failed turn, the title arriving, rename, star and delete.

Provisional because:

- **The owner gate.** The tokens are a draft and the chat mockups wait for approval as a group (P1-04). New chat follows `Home.html`, a draft.
- **Only part of it has met the real routes.** P4-03's routes arrived as this pass ended. Listing, opening, sending, streaming, the title, rename, the star and delete ran against them. Stop, a dropped stream, joining a turn after a reload and a failed turn ran only against the stand-in. Run them against the merged server, with a reply slow enough to interrupt.
- **Scope left for a second pass.** The model picker, edit and retry with branch navigation, feedback, keyboard shortcuts and motion from the design system are in the item's scope and are not built. The list is in `web.md` under "Not built yet".

### Result, the model picker, November 25, 2026

Still provisional. The model picker from the item's scope is built: a menu in the composer, beside the send button, that lists the person's models by label with a mark for local, hosted or trusted, shows the one that will answer, and picks another for the chat. A model whose key is missing is shown and cannot be picked. In a chat the page knows is held to local or trusted models, the hosted ones cannot be picked. The new chat screen has one too. The footnote names the model that will answer.

How the choice sticks: the server keeps it. The pick goes with the chat's next message as `model`, and from then on the chat's turns and its model route use the model of its last turn. Decisions 0240 (the pick) and 0241 (the held chat, which the model route does not report, learned from a refused turn and kept in the browser). `docs/architecture/web.md` has the section "The model picker". Journal: `docs/journal/2026-11-25-interface-004.md`.

Proof so far: `npm run build`, `go run ./tools/rebuild` and `go run ./tools/verify` (19 of 19) are green. In a browser, against Keystone's own server, with an invented person and four invented models whose endpoints answer nothing: a pick on the new chat screen and in a chat went with the next message and the server then reported it as the chat's model; the next message named none; a model with no key was greyed; a chat held on the server was refused on a hosted model, and the picker then greyed the hosted models. As the test tenant: one entry, "Recorded turns (test data)", and a recorded reply. Light at desktop width, dark at phone width.

Provisional because of the owner gate (the mockups show the button, not the menu), and because no model that replies has been picked yet. Edit and retry with branch navigation, feedback, keyboard shortcuts and motion are still not built.
