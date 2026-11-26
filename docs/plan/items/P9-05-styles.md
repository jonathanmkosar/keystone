# P9-05. Styles

Status: doing (chats)
Lane: chats
Depends on: P5-01
Owner gate: none
Scope: Saved tone and format presets per tenant, chosen per chat.
Proof: A chosen style is in the context and changes the reply format in a scripted test.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

Branch `chats/p9-05-styles`, cut from `reboot` at 24c6273. Follows the shape of P9-01 (projects): a package that reads no setting, a file in `internal/turn` for the routes and the call's part, a migration, a settings file, a standing check, and the screens.

**Server side.**

- `internal/styles` (new): a person's own styles (a name and instructions on tone and format) in the tenant's database, and the built-in ones, which are not rows: they come from the setting `styles.builtin`, a JSON list of `{id, name, instructions}` read by the turn and passed in (Normal with no instructions, Concise, Explanatory, Formal; invented wording). Create, read, list, change and delete a person's own; a built-in cannot be changed or deleted. A chat's style is a row of `chat_styles` (chat id, style id), which goes with the chat. It reads no setting and imports no settings package, so it is listed as a guard of `rules.tenant_isolation`.
- Which style a chat uses: its own row, else the person's default (`styles.default`, a tenant setting naming a style id, `normal` shipped, as `chats.default_model` names a model). A chat's first turn keeps the default it used as the chat's own, so changing the default later changes new chats and leaves older ones as they were (the model picker's rule: a chat keeps what its last turn used).
- Migration `20261125T235300_styles.sql`: `styles` and `chat_styles`.
- Settings in a new `settings/styles.json` (lane `styles`): the built-in styles, the default style, the heading before a style's instructions, the most styles a person may have, the longest name and the longest instructions.
- `internal/turn/styles.go` (new): the routes `GET/POST /styles`, `GET/PATCH/DELETE /styles/{id}`, `PUT /styles/{id}/default`, and `GET/PUT /chats/{id}/style`; and `styleFor`, which gives each chat call its style part. Shared code: one line in `internal/app/app.go` (the routes), and in `internal/turn/context.go` a comment, the call to `styleFor` and `Style: style,` in the builder's input. `contextbuild` already has the `style` slot in the standing start, under the first cache point, and the inspector already names the part.
- Fixed rules: a style is the person's own text in their own database (rule 1: every route resolves the store from the session; another tenant's style or chat is not found). A style is words in the system text and nothing else: it is chosen after the route, holds no part in routing, recall or approvals, and `internal/styles` imports none of them (checked).

**The screen** (provisional: no styles mockup). A style control beside the model picker in both composers (`StylePicker.tsx`, drawn like the model picker; for the new chat screen the pick is kept and applied to the chat the first message makes, as the tools menu does), and Settings → Styles (`StyleSettings.tsx`) to make, change and delete a style and choose the default. Calls in a new `styleCalls.ts`; added lines in `Chat.tsx` and `Settings.tsx` only.

**The proof.** `go test ./internal/styles` (the record, the built-ins, the rules) and `go test ./internal/turn` (`styles_test.go`) with the fake provider: a chosen style's instructions are in the standing start of the request the model gets, before the first cache point, and the record's `style` part is kept; the fake provider, scripted to answer by what the system text holds, answers one style as a bulleted list and another as a formal paragraph, so the reply's format changes with the style; changing the style changes the next call and keeps the call's start otherwise byte-identical; the default is kept by a chat's first turn; another tenant reaches no style and no chat's style. A standing check `styles` in `tools/verify/styles.go` proves the same on the real server with the shipped settings.

**Left for later.** A handoff keeping its parent's style, the style's name in the inspector, a style on the project page's composer, and the styles mockup.
