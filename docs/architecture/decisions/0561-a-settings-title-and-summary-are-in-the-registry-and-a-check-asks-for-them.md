# 0561. A setting's title and summary are in the registry, and a check asks for them, not the reader

November 27, 2026 · decided in P11-04

## The question

The settings screen now shows each setting by a readable name and one short plain line (decision [0560](0560-a-setting-is-a-name-a-short-line-and-a-value-and-opens-in-place.md)). Until now the name was made from the key, so `agents.schedule_budget_usd` read "Schedule budget usd", and the only words were the long description. Where do the name and the short line live, and what happens to a setting that has none?

## The decision

- **Two fields in the registry, beside `description`: `title` and `summary`.** Every setting in every `settings/*.json` has both, each on a line of its own directly under the entry's `key` line. `description` is unchanged and is what a row shows once it is opened.
- **Reading the registry does not ask for them.** `settings.Read` and `Validate` take an entry without a title or a summary as before.
- **`settings.CheckWords` asks.** It lists every entry whose title or summary is missing or does not fit. `verify`'s `settings registry` check runs it, and so do the `settings at a glance` check and `go test ./settings`.
- **A title** is 1 to 40 characters, starts with a capital or a digit, does not end in a full stop, has no key in it, and is no other setting's title.
- **A summary** is 3 to 14 words and 80 characters at most, ends in a full stop, and has no key and no `{placeholder}` in it.
- **The route carries both.** A setting with neither still has a row: the screen makes its name from the key, as before, and shows the first sentence of its description.

## Why

- The registry is the one source of what a setting is. Words kept in the interface would be a second list, out of date the day another lane adds a setting; decision [0191](0191-the-general-screen-is-drawn-from-the-route.md) refused that for controls for the same reason.
- If `Validate` refused an entry without the words, a lane that adds a setting while this item is built would have a server that does not start and a test suite that fails in every package that reads the registry. With a check, the same lane has a red `verify` that names the entry and says what to add, and everything else still runs.
- The limits are the registry's own rules, as the shape of a key is, and not settings: they say what fits on one line of a phone.
- Forty characters is a name on one line of a phone beside its value. Eighty characters and fourteen words is two lines at most; the longest summary written is 76 characters.
- A key in a title or a summary is the jargon the short line is there to remove, and `{name}` in one means a description was copied. Both are found by one pattern: lower-case words joined by a dot or an underscore, or a brace.
- Titles are unique so a search by name finds one setting, and so a screen reader's list of buttons has no two alike.

## What it costs

- Every lane that adds a setting writes two more lines, and `verify` is red until it does.
- "A key in it" is found by shape. A summary may not write a number as "v1.x" or name a file with a dot before a letter. None of the 346 needed to.
- The words are English and in the registry. A second language would need them moved.
