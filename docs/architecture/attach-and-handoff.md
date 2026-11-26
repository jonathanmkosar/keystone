# Attached chats and handoff

Folder: `internal/attach`, joined to the routes and to each model call by `internal/turn/attach.go`. The screen's part is in `web/app/src/AttachedChats.tsx` and `web/app/src/ChatLinks.tsx`.

A chat can **attach** other chats of the same person. Each model call of it then carries every attached chat as its recap, the short summary, topics and decisions of P5-05 ([chat-summaries.md](chat-summaries.md)), and the model can open the rest of one with **`open_attached_chat`**, a page at a time. A **handoff** opens a fresh chat that continues from this one: the new chat records its parent, carries it as it carries an attached chat, and the screen links the two both ways. This is the plan's "Attach a chat" and "Handoff" ([PLAN.md](../plan/PLAN.md), "Chats and context").

The package keeps the links and gives fixed rules 1 and 3 their answers for attached chats. It reads no setting and does not import the chat record: the turn reads both and passes plain values in, as `internal/recap` does. It is in `fixedRuleGuards` for rule 3 (`tools/verify/attach.go`), so it cannot import `settings` or `internal/tenantsettings`, itself or through another package.

## The record

`chat_attachments`, from the migration `20261125T195300_chat_attachments.sql`: one row for each chat attached to a chat.

| Column | Holds |
| --- | --- |
| `seq` | The order chats were attached in. |
| `chat_id` | The chat that carries the other. |
| `attached_id` | The chat it carries. |
| `handoff` | 1 on the row that says `chat_id` was handed off from `attached_id`, its parent. At most one such row for each chat (a partial unique index), and it is not taken off by hand. |
| `added_at` | When. |

Both columns refer to `chats(id)` with `ON DELETE CASCADE`, so a row goes when either chat is deleted, and a chat may not be attached to itself (a `CHECK`). The parent's link to its handoffs is the same row read the other way (`Handoffs`).

| Function | Does |
| --- | --- |
| `Add(chat, attached, max)` | Attaches one chat to another, and reports whether it was attached now: a chat attached already is left as it is. Refuses a chat attached to itself (`ErrSelf`), a chat that is not in this tenant's record (`ErrNotFound`), one past `max` (`ErrFull`), and an incognito chat on either side (`ErrIncognito`). |
| `Remove(chat, attached)` | Takes one off, and reports whether it was attached. The parent stays: `ErrParent`. |
| `Handoff(parent, child)` | Records that the child continues from the parent. Refuses an incognito parent and a child that continues from a chat already (`ErrHandedOff`). |
| `List(chat)` | The chats a chat carries: the parent first, then the rest in the order they were attached. |
| `Handoffs(chat)` | The chats handed off from a chat, oldest first. |
| `Is(chat, attached)` | Whether one is attached to the other. |
| `Incognito(chat)` | `memory.Incognito`, with a flag that cannot be read counting as on. |
| `MayShow(chat, mayReadSensitive)` | Fixed rule 3's answer, and the incognito rule's, below. |
| `Text(card)` | One attached chat as a call carries it: a label and the text. |
| `LeftOut(n)` | The line a call is given for attached chats left out for rule 3. |
| `Page(id, title, messages, page, options)` | One page of an attached chat for `open_attached_chat`. |
| `CheckOptions(options)` | Refuses settings nothing can be attached or read with, naming the setting. |

## Fixed rule 1

A link joins two chats of one tenant's store, and both ends refer to that store's own `chats` table. No function takes two stores, and a chat of another tenant is not in this one's record, so the routes answer `not_found` for it from either end before anything is written, and the table refuses it if anything tried.

## Fixed rule 3

A chat that was sent content from a sensitive space (`recall.Held`), or whose recap was made while it was, holds sensitive content: its title, its recap and its messages. `attach.MayShow` asks `recap.Sensitive` and `recap.MayShow`, as the past-chat search does, and answers for one attached chat and one call:

- **Show** only when every model the call's route may reach may read sensitive content (`readsSensitive` in `internal/turn/recall.go`, the least of the route). A mark that cannot be read counts as sensitive.
- **Sensitive**: when it is shown, the chat it goes into is held first with `recall.Hold`, since what a call is given stays in that chat and is sent again with every later call of it. When the hold cannot be kept, it is not given.
- **Incognito**, below.

So:

1. **In the context.** An attached chat a call may not be given is left out, and the call is told how many were, with no name or id (`LeftOut`, label `attached chats left out`).
2. **In the tool.** `open_attached_chat` refuses such a chat without naming it.
3. **In the route.** When the chat's own model may read sensitive content and a chat it carries holds some, the route keeps to models that may (one line in `Runner.sensitive`), as recall's sensitive items do. So a chat on a local model with a hosted fallback is carried its sensitive attached chat, and the fallback is left out of that turn. A chat on a hosted model is not held to local models by an attachment: the attached chat is left out of its calls instead, and the line says a local or trusted model is needed. Decision [0363](decisions/0363-a-sensitive-attached-chat-is-left-out-or-holds-the-route-as-recall-does.md).

## Incognito

An incognito chat is outside all of it (decision [0361](decisions/0361-an-incognito-chat-is-neither-attached-nor-attaches-nor-handed-off.md)):

- it cannot be attached to a chat, and cannot attach one (`409 incognito`);
- it cannot be handed off (`409 incognito`);
- a chat made incognito after it attached chats carries none of them, and offers no tool;
- an attached chat made incognito afterwards is left out of every call and of the tool, without a line; the screen marks its chip.

## Into each call

`Runner.build` (`internal/turn/context.go`) fills `contextbuild.Input.Attached` with `Runner.attachedItems`, and offers the tool with `Runner.withAttached`:

1. **The links.** None when the chat is incognito.
2. **A fresh recap, in the first call of a turn.** An attached chat with no recap, or whose current branch has moved past the message its recap was made from, is given one by the recap code of P5-05 (`makeRecap`): the summary route, `routing.summary_model` first, with a held chat's kept to models that may read it; the call is under the turn's request id, with a usage record of the kind `summary`, so the turn's audit trail and a background task's budget see it. The turn waits up to `chats.attached_summary_wait`, also for a recap of that chat that is being made already (the job after a turn, or another chat that carries it). A chat whose recap failed lately is left alone until `chats.recap_retry_after` has passed. A chat with no recap is carried without one. This is made whatever `chats.recap` says: that setting is about recaps after a chat's own turns.
3. **Rule 3 and incognito**, as above.
4. **The item** (`attach.Text`): the chat's title, its id and the date of its last message, then its recap as the `Summary:`, `Topics:` and `Decisions:` lines, and the line that names the tool and the id when the call offers it. A parent's item says the chat continues from it. The label is `continued from "<title>"` or `attached chat "<title>"`.
5. **Packing.** The parent's item, and the line for chats left out, are pinned: never left out for room. The others share `chats.context_attached_percent` in the order given, and one that does not fit is left out ([context.md](context.md)). The attached part sits after the standing start with the parts that change rarely, so the start of a call still caches ([caching.md](caching.md)).

The turn inspector shows the part and each item with its tokens. An attached chat is left out of a turn by taking its chip off, not from the inspector.

## open_attached_chat

Offered to a call while its chat carries a chat that is not incognito, to a model that can use tools, unless the person has switched tools off or set it never. Input: `chat_id`, which may be left out when one chat is attached, and `page`, from 1.

- It is made for each call, for the least of the call's route, and kept on the turn beside the past-chat tools (`turnRecall.chats`, `internal/turn/pastchats.go`), so the calls the model makes in reply run it; a turn that resumes after an approval makes it again. It runs through a registry of its own (`runLookup`), so each call is recorded before and after in the audit log, timed, and checked by the approval rule.
- It reads only a chat attached to the chat the call is in.
- **A page** is the attached chat's current branch, oldest first: "The person:" and "The reply:", a tool call named, and a tool's result and a model's thinking left out, as `read_chat` leaves them out. Each page holds at most `chats.attached_page_messages` messages and `chats.attached_page_max_chars` characters; a message longer than a page is cut short and says so. The result opens with the chat's id, title, "Page N of M" and which messages, and says which page holds the next.
- It is read-only, and **Keystone's own content**, not outside content: a page holds the person's words and the replies and nothing a tool brought in (decision [0360](decisions/0360-an-attached-chat-comes-in-as-its-recap-and-the-rest-through-a-tool-made-for-each-call.md)), so reading one never makes a later side effect ask ([approvals.md](approvals.md)).
- A page that is not there is an error the model can mend, with how many pages there are.

## Handoff

`POST /chats/{id}/handoff` makes a fresh chat, records the parent link, and answers with the new chat. The parent's recap is made at the new chat's first turn when it has none or has moved on, as for any attached chat. The answer names the model the parent's next turn would use, so the screen sends it with the new chat's first message and the conversation goes on with the same model. A chat with no messages has nothing to carry (`400`). See decision [0362](decisions/0362-a-handoff-is-a-new-chat-with-its-parent-attached.md).

A handoff from a chat that holds sensitive content is not held by itself: its first turn on a local or trusted model is carried the parent, and is held from then on; on a hosted model the parent is left out and the line says why.

## The routes

In the shell like every chat route ([turns.md](turns.md)), listed in `internal/app` (`turn.AttachRoutes`):

| Route | Reply |
| --- | --- |
| `GET /chats/{id}/attached` | `AttachedReply`: `chat_id`; `attached`, each with its `id`, `title`, recap `summary`, and whether it is the `parent`, `sensitive` or `incognito`; `parent`, the chat it continues from; `handoffs`, the chats handed off from it; `max`, `chats.attach_max`; and `incognito`. The person's own screen sees a sensitive chat's title and summary: the rule is about what reaches a model. |
| `PUT /chats/{id}/attached/{other}` | `AttachedReply`, after attaching. No body. Twice changes nothing. `not_found` for a chat that is not the person's, from either end; `bad_request` for a chat attached to itself; `409 too_many` past `chats.attach_max`; `409 incognito`. |
| `DELETE /chats/{id}/attached/{other}` | `AttachedReply`, after taking it off. A chat not attached changes nothing; the parent is `bad_request`. |
| `POST /chats/{id}/handoff` | `HandoffReply` (201): `chat`, the new chat as `GET /chats/{id}` gives it; `parent`; `model`. `409 incognito`; `bad_request` for a chat with no messages. |

## Settings

In `settings/chats.json`, all a person may set for themselves.

| Key | Default | Meaning |
| --- | --- | --- |
| `chats.attach_max` | 5 | The most chats one chat may have attached, the parent included, from 1 to 50. |
| `chats.attached_page_messages` | 40 | The most messages one page of `open_attached_chat` holds, from 1 to 500. |
| `chats.attached_page_max_chars` | 12000 | The most characters one page holds, from 500 to 200000. |
| `chats.attached_summary_wait` | 30s | How long the first call of a turn waits for an attached chat's recap, from 1s to 10m. |

An attached chat's recap is made with the recap settings ([chat-summaries.md](chat-summaries.md)), and its share of a call is `chats.context_attached_percent` ([context.md](context.md)).

## In shared code

Every existing line of shared code this item changed. All are added lines; none was changed or removed.

- `internal/turn/context.go`, in `Runner.build`: `offered = r.withAttached(...)` and `attached := r.attachedItems(...)` after `leaveOut`, and `Attached: attached,` in the `contextbuild.Input`.
- `internal/turn/recall.go`, in `Runner.sensitive`: `held = held || (mayRead && r.attachedSensitive(ctx, s, t))` before the return.
- `internal/turn/approvals.go`, in `resumeStep`: `r.attachedTools(ctx, s, *t, p, m, nil)` after `lookupTools`.
- `internal/app/app.go`: `all = append(all, turn.AttachRoutes(runner)...)` after the recap routes.
- `tools/verify/main.go`: one `componentDocs` line and the check `attached chats`.
- `settings/chats.json`: the four settings above, after `chats.compact_retry_after`.

New files: `internal/attach/*`, `internal/turn/attach.go` and `attach_test.go`, the migration, `fixtures/test-tenant/turns/attached-chats.json`, `fixtures/test-tenant/attached-chats.json` and `tools/verify/attach.go`.

## Checked by

- `go test ./internal/attach`: chats are attached, listed in order, attached twice to no effect, taken off, and go with a deleted chat; the limit, a chat attached to itself and a chat that is not there are refused; another tenant's chat cannot be named from either end, and the table refuses it; a handoff is listed first, cannot be taken off, and a chat continues from one chat only; an incognito chat is neither attached, nor attaches, nor is handed off, and one made incognito later is not shown; a held chat and a chat whose recap is marked are shown only to a call that may read sensitive content; the text a call carries; a chat is read a page at a time, by messages and by size, with a long message cut and a tool's result and thinking left out, and a page past the end is `ErrInput`; each bad setting is named.
- `go test ./internal/turn`, through the routes with the fake provider:
  - **the item's first proof**: a handoff chat answers a question about its parent; the parent's recap is made in the turn under its request id and is in the call the model gets; the inspector's record holds the pinned `continued from` item; a second turn makes no new recap; the parent stays attached; an empty chat is not handed off;
  - **the item's second proof**: an attached chat's detail, which its recap does not hold, is reached through `open_attached_chat`, recorded before and after in the audit log; taken off, the chat is carried no more and the tool is not offered;
  - **fixed rule 1**: another tenant's chat is not found, attaching, listing, handing off or taking off, and no link is written;
  - the routes: twice changes nothing, `too_many`, a chat attached to itself, a chat that is not there;
  - incognito: refused both ways and for a handoff; an attached chat made incognito is carried no more;
  - **fixed rule 3**: with a hosted fallback, a held attached chat is left out of the hosted model's call, which is told one was left out, and the tool refuses it without naming it; the local model's call carries it, through the context and the tool, its route leaves the hosted fallback out, and the chat is held, so a later hosted pick is refused; carried in the context alone, it holds the chat all the same.

Faults were put in by hand and each was caught: the items made as if every model may read sensitive content, the tool made so, the routing line switched off (caught once the test had a hosted fallback), and the hold in the context left out.

`verify`, `attached chats` ([tools.md](tools.md)), with the real server on a throwaway folder, the expectations of `fixtures/test-tenant/attached-chats.json` and the held chat of `fixtures/test-tenant/past-chats.json`: a handoff chat answers about its parent with the parent's summary in its call, and the parent lists it; an attached chat's detail comes through `open_attached_chat`, recorded before and after; another tenant's chat cannot be attached from either end; on an invented tenant, a chat recall held is never sent to the hosted model when attached, whose call says one was left out, and is carried to the local model, holding its chat.

## Not here yet

- **Attaching from the new chat screen**, before the chat exists. The control is on a chat's screen.
- **Attachments that are files** (P9-02) share the `attached` part; projects are P9-01.
- **A recap sweep.** An attached chat with no recap is given one at the first turn that carries it.
- **An attached chat's own attachments** are not carried: one level only.
- **A real summary model.** Only the fake has written a recap of an attached chat.
