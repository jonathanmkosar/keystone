# 0452. A style is words in the standing start, and changes nothing else

November 25, 2026 · decided in P9-05

## The question

A style is text the person writes, and it goes into every call of a chat. Where in the call does it go, and what may it change?

## The decision

**It goes in the context builder's `style` part, the last part of the standing start, and it changes nothing but those words.**

- The part was waiting in the builder since P5-01, after the rules, the profile, the person map and the project's instructions, all under the call's first cache point. A chat that keeps its style keeps its start byte for byte; changing the style changes only that part, from the next call. A style with no instructions sends nothing, so a chat on Normal is sent what it was before styles.
- The text is the heading of `styles.heading`, with the style's name, and the instructions. The shipped heading says the style is for tone and format and nothing else.
- The style is read after the call's route is chosen, by the turn, and passed to the builder as a string. `internal/styles` imports nothing of routing, the model registry, recall, the tools or the approval rule, and the `styles` check fails if it ever reaches one. What a style says cannot grant an approval, mark content as fit for a hosted model, or move a call: those are decided in code from values the style never touches.
- A style is the person's own text in their own database, and goes to every model of the chat's route, hosted ones too. It has no sensitive mark, as a project's instructions have none (decision 0403): private material belongs in a space or file marked sensitive.

## Why

- The plan names the slot and the order: the standing start is what a provider caches, and a style is chosen per chat and changes rarely, so it belongs there and not in the message.
- Last in the standing start, it is the part most likely to change of the five, so a change leaves the parts before it as they were.
- Fixed rules 2 and 3 are enforced by code that cannot read text a person wrote. Keeping the style out of that code entirely, and checking it stays out, is simpler than reasoning about what a style might say.

## What it costs

- A model may still be swayed by a style's words in what it writes, as by any instruction. That is the point of a style; what Keystone lets the model do is not.
- A style written as one long text costs its tokens in every call of every chat that uses it, read from the cache after the first. `styles.instructions_max_chars` bounds it.
