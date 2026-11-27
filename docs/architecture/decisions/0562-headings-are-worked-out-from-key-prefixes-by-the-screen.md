# 0562. The headings are worked out from key prefixes by the screen, not kept in the registry

November 27, 2026 · decided in P11-04

## The question

General grouped settings by the lane that owns them: "Agents", "Chats", "Core", "Models". That is how the code is organised and not how a person thinks: the usage report's settings were under "Core" beside the warning levels of Operations, the model that writes summaries was under "Models" and the switch for summaries under "Chats", and "Agents" and "Chats" held 30 of a person's 146 settings each. Decision [0191](0191-the-general-screen-is-drawn-from-the-route.md) named it as a cost. Under what headings do 346 settings go, and where is that written?

## The decision

- **A setting goes under the heading of the longest prefix of its key that the screen lists.** The list is in `web/app/src/settingsView.ts`: 50 prefixes and 35 headings today, 34 of them with a setting, such as `chats.context_` under "What a model is sent", `knowledge.recall` under "Recall", `agents.wiki_` under "Wiki jobs", and `signin.` under "Signing in".
- **The order of the list is the order on the screen:** what a person changes most first, and what only an install has last.
- **A key that no prefix covers gets a heading made from its first word**, after the others. `zebra_crossing.wait` would go under "Zebra crossing".
- **The registry has no `group` field.** The lane is still in the route's answer and is not shown.

## Why

- The item asked for plain headings from the registry's lanes and key prefixes. Keys are already named for what they belong to (`chats.recap_after_turns`, `chats.recap_timeout`), so the prefix carries the grouping and nothing has to be written a second time.
- A setting another lane adds lands in the right place with no change anywhere: `agents.task_something` goes under "Background tasks and sub-agents" because `agents.task_` does. With a `group` field, every new setting would need a third line, and two lanes would soon write "Agent" and "Agents".
- The longest prefix winning lets a few settings sit apart from their lane's default: `knowledge.incognito_` is under "Chats", and `chats.test_model_` with `fake.` under "The test person".
- The list is plain data in a file with no imports, so its tests run against the real `settings/*.json`: every setting is in exactly one group, no heading comes twice, and a part of the list keeps the order of the whole.

## What it costs

- The screen names key prefixes. It still names no setting but the theme, and a prefix is not a setting, but a renamed prefix moves its settings to the fallback heading until the list is changed. Nothing fails when that happens: the settings are still listed and found.
- A lane whose new settings start a new first word gets a heading made from that word, which may be less plain than one chosen. It is one line in the list to choose.
- Within a heading the settings are in key order. That keeps relatives together and puts a master switch first only when its key sorts first.
