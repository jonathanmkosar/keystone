# 0612. On a phone a message's actions are icons, and a person's own wait for a tap

November 27, 2026 · decided in P10-07, the phone layout the owner asked for

## The question

Under each message Keystone drew text actions, "Copy", "Pin", "Inspect", on the person's messages and on the replies alike; on a phone (which has no hover) the person's were always shown. The owner's pictures of another app show icons only under a reply and nothing under the person's own message. What should Keystone do on a phone?

## The decision

- **Icons only, a thumb's size.** At 760 pixels and under, the actions under a reply and under a person's message are 44-pixel round buttons with an 18-pixel icon. Their words stay in the markup, in a span hidden from sight but not from a screen reader, and each button has an `aria-label` and a `title` with the same words (`CopyButton` in `Markdown.tsx`, `PinButton` and `InspectButton` in `MessageActions.tsx`). Desktop keeps the words.
- **A person's message shows its actions only once tapped.** The bubble becomes a button on a phone (`role="button"`, `aria-expanded`, Enter and Space); a tap shows Copy and Pin under it, and a second tap hides them. A tap on a file or link in the bubble only opens that.
- **A pinned message keeps showing its actions**, as on desktop: the pin is a state the person set, and hiding it would hide that the message goes with every reply. The rule is `saidActionsShown()` in `composerSheet.ts`, tested.

## Why

- Icon-only actions are what the owner's pictures show, and the words still reach a screen reader and a long press's tooltip.
- Waiting for a tap clears the person's side of the conversation, which on a phone is most of what made it look busy.

## What it costs

- Copy, Pin and Inspect are learned by their icons on a phone.
- Copying a message of one's own is a tap more.
- Showing a pinned message's actions is a departure from the pictures, kept on purpose; the owner may want only the pin.

`verify` fails if a record in `decisions/` is not linked from `overview.md`.
