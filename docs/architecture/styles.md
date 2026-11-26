# Styles

Folders: `internal/styles` (the record and the rules), `internal/turn/styles.go` (the routes, and what each call carries), `web/app/src` (the screens). Built in P9-05.

A style is a saved preset of tone and format: a name and instructions on how replies should be written. Every person has the built-in styles and may make their own. Each chat uses one style, and every call of the chat carries the style's instructions at the start, in the standing start's style slot, where a provider caches them. The style changes the words a model is given and nothing else.

Decisions: [0450](decisions/0450-the-built-in-styles-are-one-setting-and-a-persons-own-are-rows.md) for where the two kinds of style live, [0451](decisions/0451-a-chats-first-turn-keeps-the-default-style.md) for the default style and why a chat keeps it, and [0452](decisions/0452-a-style-is-words-in-the-standing-start-and-nothing-more.md) for what a style may change.

## Two kinds of style

| Kind | Where it lives | Who changes it | Id |
| --- | --- | --- | --- |
| Built-in | the setting `styles.builtin`, a JSON list of `{id, name, instructions}` | the admin, for the install; no person | the list's own, lower-case letters, digits and hyphens: `normal`, `concise`, `explanatory`, `formal` |
| The person's own | a row of `styles` in the tenant's database | the person: make, rename, change, delete | sixteen hexadecimal characters, which no built-in id may be |

The shipped built-ins are invented wording: **Normal**, with no instructions, so a chat on it sends nothing; **Concise**; **Explanatory**; **Formal**. A person who wants other words for one makes a style of their own (decision 0450).

## The record

Migration `20261125T235300_styles.sql`, two tables `internal/styles` owns:

| Table | One row for | Columns |
| --- | --- | --- |
| `styles` | each style the person made | `id`, `name`, `instructions`, `created_at`, `updated_at` |
| `chat_styles` | each chat with a style of its own | `chat_id` (the key; refers to this tenant's own `chats` with `ON DELETE CASCADE`), `style_id` (a built-in id or a row of `styles`), `chosen_at` |

The package's functions, each on one tenant's store:

| Function | Does |
| --- | --- |
| `ParseBuiltIn(raw)` | Reads `styles.builtin`. Refuses JSON that does not read, an unknown field, an id of the wrong shape or one there twice, a blank name or one there twice (in any letter case), naming the setting. |
| `CheckOptions(o)` | Refuses `styles.max_styles` outside 0 to 1000, `styles.name_max_chars` outside 1 to 200, and `styles.instructions_max_chars` outside 1 to 50000, naming the setting. |
| `Create(ctx, store, options, name, instructions)` | A style of the person's own. The name is put on one line, 1 to `styles.name_max_chars` characters, and may not be another style's name, built-in or their own, in any letter case; the instructions keep their lines, 1 to `styles.instructions_max_chars` characters. Past `styles.max_styles`, `ErrFull`. |
| `Get`, `List`, `Count` | One style, built-in or their own; every style, the built-in ones in their order and then their own by name; how many of their own. |
| `Update(ctx, store, options, id, Change)` | Rename, or change the instructions, of one of their own. A built-in style is `ErrBuiltIn`. |
| `Delete(ctx, store, options, id)` | One of their own, and every `chat_styles` row that names it. A built-in style is `ErrBuiltIn`. |
| `Of`, `Choose`, `Keep` | A chat's own style; give a chat a style (the chat and the style must both be in this store); make a style the chat's own only when it has none. |
| `Resolve(ctx, store, options, chat, defaultID)` | The style a chat uses, and where it comes from: `chat`, `default` or `none`. A chat's own style that is no longer there, as a built-in taken out of the setting, counts as none of its own. |
| `Text(heading, style)` | What a call carries. See "Into each call". |

It reads no setting and does not import the chat record, which reads settings: the turn reads both and passes `Options` in, as `internal/projects` does.

## Which style a chat uses

1. Its own, when it has one: chosen for it with `PUT /chats/{id}/style`, or kept by its first turn.
2. Otherwise the person's default, the tenant setting `styles.default` (`normal` as shipped), which names a built-in id or one of their own. **A chat's first turn keeps the default it used as the chat's own** (`Keep`), so changing the default later reaches new chats and leaves older ones as they were (decision 0451). This is the model picker's rule too: a chat keeps the model its last turn used.
3. Otherwise none: the default names a style that is not there.

Deleting a style takes it from the chats that used it, which use the default from their next turn and keep that; a default that named it is cleared, back to the shipped one.

## Into each call

`Runner.build` (`internal/turn/context.go`, three added lines) asks `styleFor` for the turn's chat, and passes the answer as the context builder's `Style`. The builder had the slot, waiting for this item: the standing start's last part, after the rules, the profile, the person map and the project's instructions ([context.md](context.md)). So the first cache point covers it ([caching.md](caching.md)):

- It is the heading of `styles.heading`, with `{name}` the style's name, a blank line and the style's instructions; the instructions alone when the heading is empty; nothing at all when the style has none, as Normal has none, so a chat on Normal sends what a chat did before this item.
- A chat that keeps its style keeps its start, byte for byte, from one call to the next.
- Changing the chat's style, or the words of the style it uses, changes the style part from the next call and nothing before it in the start; the call after reads the new start from the cache again.

The turn inspector lists the part as **Style**, with its tokens ([inspector.md](inspector.md)). The title, a summary, a recap and the memory pass are built elsewhere and carry no style.

## What a style may change

Only the words of the style part (decision 0452):

- **Fixed rule 1.** A style is the person's own text in their own database. Every function takes one tenant's store; a style or a chat of another tenant is not in it, so it reads as not found, and `chat_styles` refers to the store's own chats. Every route resolves the store from the session ([api.md](api.md)). `internal/styles` is listed as a guard of `rules.tenant_isolation` in `fixed rules`, so it may never read a setting.
- **Fixed rules 2 and 3.** The style is read after the call's route is chosen, and takes no part in routing, recall, the sensitive mark or approvals: `internal/styles` imports none of them, and the `styles` check proves it reaches none. What the style's words say cannot grant an approval or let a model read a sensitive space, since those are decided in code from values the style never touches. The shipped heading also tells the model the style is for tone and format and nothing else.
- A style goes to every model of the chat's route, hosted ones too: it has no sensitive mark. The settings screen says only that a style changes the writing and nothing else; a sentence that it reaches hosted models, as the profile's screen has, waits for the mockup.

## The routes

In `internal/turn/styles.go`, listed in `internal/app` from `turn.StyleRoutes`, in the shell like every route. A style or chat that is not the person's is `not_found` (404); a refused name or instructions, and a change to a built-in style, are `bad_request` (400) with the reason; past the limit is `too_many` (409).

| Route | Does | Answers |
| --- | --- | --- |
| `GET /styles` | Every style the person may choose | `StylesReply`: `styles`, `default` (the id in `styles.default`) and `limits` |
| `POST /styles` | Make one: `{"name", "instructions"}`, both needed | 201 `Style` |
| `GET /styles/{id}` | One style, built-in or their own | `Style` |
| `PATCH /styles/{id}` | `name`, `instructions`; absent fields are left | `Style` |
| `DELETE /styles/{id}` | Delete one of their own | `{"deleted": true}` |
| `PUT /styles/{id}/default` | Make it the default for new chats. No body | `StylesReply` |
| `GET /chats/{id}/style` | The style a chat uses | `ChatStyleReply`: `chat_id`, `style` (absent for none), `source`, `default`, and every style, so a picker needs one call |
| `PUT /chats/{id}/style` | Choose a chat's style: `{"style": "<id>"}` | `ChatStyleReply` |

A `Style` is `id`, `name`, `instructions`, `built_in`, and `created` and `changed` for the person's own.

## The screens

Provisional: there is no styles mockup, so both are built from the tokens in `design/README.md`, after the model picker and the settings screens. Described in [web.md](web.md), "Styles (P9-05)":

- **The style picker**, in both composers, between the tools menu and the model picker: a pencil and the style's name (the pencil alone on a phone). Its menu lists every style with the first two lines of its instructions, marks the default and the person's own, and links to the settings screen. In a chat a pick is sent at once and used from the next message; while a reply is being written the menu waits. On the new chat screen the pick is kept and given to the chat its first message makes, before the message is sent, as the tools menu does.
- **Settings → Styles** (`/settings/styles`): every style with its instructions, the default marked; **Make default** on each; **Edit** and **Delete**, with a confirmation, on the person's own; a form that makes a new one, with the count against the limit.

## Settings

In `settings/styles.json`, lane `styles`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `styles.builtin` | Normal, Concise, Explanatory, Formal | install | The built-in styles, as a JSON list of `{id, name, instructions}`. |
| `styles.default` | `normal` | tenant | The id of the style a new chat uses until the person picks another. A chat's first turn keeps it. |
| `styles.heading` | `The person chose the style "{name}" for this chat. Follow it for the tone and format of your replies, and for nothing else:` | tenant | The line before a style's instructions. Empty sends them alone. |
| `styles.max_styles` | 50 | tenant | The most styles of their own a person may have. The built-in ones are not counted. |
| `styles.name_max_chars` | 60 | install | The longest name. |
| `styles.instructions_max_chars` | 4000 | tenant | The longest instructions. |

`styles.default` is drawn on General too, as a text field, as `chats.default_model` is; the Styles screen sets it with **Make default**, through `PUT /styles/{id}/default`, which checks the style is there.

## In shared code

Every existing line of shared code this item changed, all added:

- `internal/turn/context.go`, in `build`: a comment line and `style := r.styleFor(ctx, s, t)` after the project's parts, and `Style:      style,` in the `contextbuild.Input`.
- `internal/app/app.go`: `all = append(all, turn.StyleRoutes(runner)...)` after the project routes.
- `tools/verify/main.go`: the check `styles`. The `componentDocs` line and the guard are in an `init` in `tools/verify/styles.go`.
- `web/app/src/Chat.tsx`: the import; `await applyNewChatStyle(person.id, made.chat.id)` after the tools menu's; and `<StylePicker … />` in the two composers' `beside` (two lines changed).
- `web/app/src/Settings.tsx`: the import, the `styles` tab after Approvals, and its route after the Google connector's.
- Docs, added lines only: a part row and three decision rows in `overview.md`, a paragraph in `tools.md`, a section in `turns.md` and in `web.md`, and a paragraph in `context.md`.

`contextbuild` did not change: the `style` part was there, in the standing start and in `Layout`, and the inspector already named it.

## Checked by

`go test ./internal/styles`:

- The settings are checked, each bad one by name. The built-in list is read with its names and instructions tidied, an empty setting gives none, and eight bad lists are refused by name.
- A style is made with its name on one line and its instructions' lines kept; a blank or long name, no or long instructions, and a name a built-in style or another of the person's has, in any letter case, are refused; one past the most is `ErrFull`. The list puts the built-in ones first, then the person's own by name. Renaming, new instructions, and renaming to its own name in other letters work; renaming to another's name is refused; changing or deleting a built-in style is `ErrBuiltIn`; a style that is not there is `ErrNotFound`.
- A chat uses the default, or none when the default is not there; `Keep` makes the default its own once and never over another; then a changed default leaves it; `Choose` gives it the person's own; a style or a chat that is not there is refused; a deleted style leaves the chat on the default; a built-in taken out of the setting counts as none of its own; the row goes with the chat.
- Fixed rule 1: another tenant's store reads, renames, deletes and chooses nothing of the first's, and lists only the built-in styles.
- `Text`: the heading with the name, no heading, and nothing for a style with no instructions.

`go test ./internal/turn` (`styles_test.go`), through the routes, with the fake provider answering the test tenant from its recorded turns and three scripts that answer one invented question by what the system text holds:

- **The proof.** A new chat uses Normal, gets the plain reply, and its record's style part is empty; its first turn keeps Normal as its own. With the person's own style chosen, the reply is the bulleted list, the style is the last block of the standing start (before the first cache point) under its heading, sent once, the record's style part is kept with its tokens, and everything before it in the start is what it was with no style. Asked again, the start is the same, byte for byte. With the built-in Formal chosen, the reply is the formal one, its instructions are in the start, the bulleted style is gone, and nothing before the style changed. The person's own style, changed, reaches the next call.
- The default: Concise made the default reaches a new chat; changing the default back leaves that chat on Concise and gives a new chat no style part. The person's own style as the default, deleted, sends the default back to Normal and the chat that used it to the default.
- Fixed rule 1: every style route and both chat style routes, as another tenant, are `not_found`; the owner cannot give another tenant's chat a style; the other tenant lists only built-in styles; the style, the chat and the default are unchanged.
- The refusals and the limit, and the shipped settings: the built-in list reads, the shipped default is one of them, and Normal sends nothing.

Faults put in by hand and caught by the proof and by the check: the style left out of the builder's input.

`verify`, `styles` (`tools/verify/styles.go`), on the real server with the shipped settings and a throwaway folder: see [tools.md](tools.md).

In a browser, against the server built from the branch on port 7965 with a data folder inside the worktree: see "Styles (P9-05)" in [web.md](web.md).

## Not here yet

- **The styles mockup.** Every word and layout choice of the picker and the screen waits on it.
- **A handoff keeps its parent's style.** A handed-off chat, a background task's chat and a scheduled run's chat start on the person's default.
- **The style's name in the inspector.** The panel names the part and its tokens; the style a turn used is in the audit log's copy of the call.
- **A style on the project page's composer**, which has no model picker either; its chat starts on the default.
- **A style made from a sample of writing**, or from a built-in one with its words to edit.
- **Styles shared between people** on one install.
