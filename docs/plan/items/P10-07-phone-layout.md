# P10-07. The phone layout, after the owner's pictures

Status: provisional
Lane: interface
Depends on: P4-04
Owner gate: The owner's look on his iPhone (the keyboard, Safari's blur and safe areas). Built and seen at phone widths in a browser.
Built without the gate: Everything; it builds on the phone sheets of decision 0590.
Scope: Asked for by the owner on November 27, 2026: "Update UI to be more like" another app's phone screens, with five pictures (new chat with the keyboard down and up, a chat idle and focused, a question card). Borrow the layout and feel, not the brand: Keystone keeps its mark, name, tokens, colours and fonts. Phone width only (760 pixels and under); desktop unchanged.
Proof: At 390 by 844, light and dark: the header floats over the conversation with a round menu button, a title pill with the project under it, and a pill with the working mark and `...`; the new chat has a one-line greeting with the first name in the middle and the composer at the foot, with Incognito a round button at the top right; the composer is a pill while idle and the card when focused or holding something, with no jump; no note under the composer, its footnote at the foot of the model's sheet, the test tenant's notice still shown; actions under messages are icons, and a person's own message shows its actions only once tapped; every target 44 pixels, every box 16 pixels, nothing wider than the screen; desktop as before.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

1. `composerSheet.ts`: the pure rules, tested: `composerExpanded`, `phoneGreeting` and `firstName`, `saidActionsShown`, `phonePlaceholder`, `footnote`.
2. `Composer.tsx`: one form for both states (`is-idle`), focus counted across the composer's sheets, a short fold delay, the row's presses keep the box focused. `phone-sheet.css` places the parts with a wrapping flex box and `order`. Leave the `+` sheet's rows and the attach button alone (the location lane is changing them) and leave room for chips over the box.
3. `Shell.tsx`: a floating header on a phone; `ChatTitle` as a pill with the project; the working spinner; Incognito as a round button through a small store in `Incognito.tsx`.
4. `Chat.tsx`: the new chat's phone layout; no note on a phone but the test tenant's notice; keep the end of the chat in view as the composer grows. `ModelPicker.tsx`: the footnote at the foot of the sheet.
5. `Conversation.tsx`, `MessageActions.tsx`, `Markdown.tsx`: icon-only actions on a phone with their words for a screen reader; the person's actions wait for a tap.
6. Build, check in the browser pane at 390 by 844 and 320 by 700 in both themes and at 1280 by 800, `verify`, docs (`web.md`, decisions 0610 to 0612), journal.

## Result

Built as planned. `npm test` 72 of 72 (eight new), `tsc` and the three Vite builds pass, `web/dist` rebuilt and committed, `verify` green. Seen in the desktop app's browser pane against a server from the branch with a scratch data folder, as the test tenant; see `docs/journal/2026-11-27-interface-013.md` for what was and was not seen.

Provisional on the owner's phone: the keyboard over the composer, Safari's blur and safe areas, and whether he wants the person's pinned messages to keep showing their actions.
