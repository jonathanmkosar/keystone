# P4-04. Chat screen

Status: doing (interface)
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
