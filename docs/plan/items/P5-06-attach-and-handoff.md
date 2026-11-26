# P5-06. Attach a chat and handoff

Status: doing (chats)
Lane: chats
Depends on: P5-05
Owner gate: none
Scope: Attach another chat as its summary with access to the rest; handoff opens a linked fresh chat with a summary.
Proof: A handoff chat answers a question about its parent; an attached chat's detail is reachable.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Branch `chats/p5-06-attach-and-handoff`, cut from `reboot` at 798e627. No owner gate.

### Plan

What is built, in steps that each end with `verify` green:

1. **The record.** Migration `20261125T195300_chat_attachments.sql`: `chat_attachments`, one row for each chat attached to a chat (`chat_id`, `attached_id`, both referring to `chats` with `ON DELETE CASCADE`, so the rows go with either chat), with `handoff = 1` on the row that says a chat was handed off from another (its parent; at most one per chat). A new package, `internal/attach`, owns the table: add, remove, list, the parent and the handoffs of a chat, and the fixed rules' answers for attached chats. It reads no setting and does not import the chat record, so it can be a guard of fixed rules 1 and 3: rule 1 holds by construction (one tenant's store, and foreign keys into its own `chats`), rule 3 by asking `recap.Sensitive` and `recap.MayShow` for every attached chat, as the past-chat search does.
2. **The routes** (`internal/turn/attach.go`, listed in `internal/app`): `GET /chats/{id}/attached` (the attached chats with their summaries, the chat it continues from, the chats handed off from it), `PUT` and `DELETE /chats/{id}/attached/{other}`, and `POST /chats/{id}/handoff`, which makes a fresh chat with this one attached as its parent. Refused: a chat of another tenant or none (404), a chat attached to itself, more than `chats.attach_max`, removing the parent link (400), and an incognito chat on either side (409 `incognito`).
3. **Into each call.** `Runner.build` (`internal/turn/context.go`) fills `contextbuild.Input.Attached`: each attached chat as one item, its title, id and recap (summary, topics, decisions), the parent first. In the first call of a turn, an attached chat with no recap, or one older than its current branch, is given one by the existing recap code (`makeRecap`), under the turn's request id, waiting up to `chats.attached_summary_wait`; one that cannot be made is carried with a line saying it has no summary. Fixed rule 3: a sensitive attached chat (held, or its recap marked) reaches a call only when every model of the call's route may read it, and the chat it goes into is held first with `recall.Hold`; otherwise it is left out and a line with the number and no name says so. As recall does, a sensitive attached chat holds the route to local or trusted models when the chat's own model may read it (one added line in `Runner.sensitive`). An incognito chat carries no attached chat, and an attached chat that has become incognito is left out.
4. **The tool.** While a chat has attached chats, each call offers `open_attached_chat` (chat id, page): the messages of an attached chat's current branch, oldest first, a page at a time, each page at most `chats.attached_page_messages` messages and `chats.attached_page_max_chars` characters. It is made per call for the least of the call's route, like the past-chat tools (`internal/turn/pastchats.go`), reads only chats attached to this one, holds the chat before giving sensitive content, is read-only and Keystone's own content, and runs through a registry of its own, so every call is recorded and timed.
5. **The screen** (provisional: no mockup for it): under the chat's header, "Continued from" with a link to the parent, and on a parent, a link to each handoff chat; a Hand off button in the header that opens the new chat; above the composer, the attached chats as chips that can be removed, and an Attach control that picks from the person's chats, filtered by title and summary as they type. Built from the tokens in `design/README.md`.
6. **The proof**, with the fake provider (`internal/turn/attach_test.go`): a handoff chat answers a question about its parent, and the parent's summary is in the request the model gets; an attached chat's messages are reachable through `open_attached_chat`, paged; another tenant's chat cannot be attached; a sensitive attached chat is held back from a hosted model and given to a local one, holding the chat; an incognito chat cannot be attached or handed off. A step in `verify` if it fits, adding lines only.

Settings, in `settings/chats.json`: `chats.attach_max`, `chats.attached_page_messages`, `chats.attached_page_max_chars`, `chats.attached_summary_wait`.

Left for later: attaching from the new chat screen before the chat exists; attaching projects or files (P9-01, P9-02); a sweep that gives older chats recaps.

