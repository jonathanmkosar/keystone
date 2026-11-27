# 0610. The phone's composer is one element, a pill while idle and the card once used

November 27, 2026 · decided in P10-07, the phone layout the owner asked for

## The question

The owner's pictures of another app's phone screens show its composer in two states: idle, one pill row with `+`, the placeholder, the microphone and a round send button; focused or holding text, a card with the box on top and a row under it with `+`, the model's chip, the microphone and send. Keystone's phone composer was always the card. How should it change shape without the focus, the keyboard or the page jumping, and without disturbing the controls other lanes add (the location lane's chip over the box)?

## The decision

- **One form, one textarea, two classes.** `Composer.tsx` draws the same elements in both states and adds `is-idle` to the form when it is idle. The form is a wrapping flex box; the row's buttons join it through `display: contents` and `order` places them. Expanded, anything over the box and the box itself take a whole line and the buttons share the next; idle, `+`, the box, the microphone and send are one line and the model's chip is hidden. Because the textarea is the same element in both, the focus stays in it as the shape changes.
- **When it is expanded** is `composerExpanded()` in `composerSheet.ts`: the focus is in the composer, or it holds text (even only spaces), or files, or dictation is listening. Tested by `test/composerSheet.test.ts`.
- **"The focus is in the composer" counts its sheets.** React's focus events come up through the sheets' portals, so the form's `onFocus` sees the focus in the `+` sheet and the model's sheet too, and the composer stays expanded behind them; when a sheet closes and hands the focus back to `+` or the chip, they are still there to take it. `onBlur` folds the composer 120 milliseconds later, cancelled by a focus that follows: long enough for a tap's click to land on the button it began on.
- **A press on the row's buttons keeps the focus in the box** (`mousedown` default prevented, only for presses inside the row's own DOM, never in a sheet), so the phone's keyboard stays up for the next message.
- **The box says "Message Keystone"** on a phone; the longer placeholders stay on desktop.
- **Anything else in the form takes a line of its own** in either state (`form.composer.is-phone > *` is a whole line unless placed), so a chip another lane adds over the box still draws above it.

## Why

- Two separate layouts (a pill component and a card component) would unmount the textarea on focus, lose the caret and the keyboard, and double every control's state. One element with a class keeps all of it.
- A grid with named areas was the other way to place the buttons, but an element a later change adds and no area names is placed in the next free cell, anywhere; a wrapping flex box with a whole-line default degrades to "above the box".
- The fold delay is what makes a tap safe: on iOS a tap on a button blurs the box at its synthesized `mousedown`, and folding at once would move the button before the click.

## What it costs

- The composer grows upward when it expands, so the chat's visible part shrinks; `Chat.tsx` keeps the end of the chat in view on that resize (a `ResizeObserver`) when it was in view.
- The location lane's chip, once merged, should count as "holding something" so an idle composer with a location set opens as the card: one more term in `composerExpanded()`'s call in `Composer.tsx`.
- Not seen on a real phone: the keyboard's own timing on iOS and Android.

`verify` fails if a record in `decisions/` is not linked from `overview.md`.
