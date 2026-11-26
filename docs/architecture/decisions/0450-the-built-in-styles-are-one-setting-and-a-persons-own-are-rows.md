# 0450. The built-in styles are one setting, and a person's own are rows of their database

November 25, 2026 · decided in P9-05

## The question

Keystone ships a few styles every person has (Normal, Concise, Explanatory, Formal), and each person may make their own. Where does each kind live, and may a person change a built-in one?

## The decision

**The built-in styles are one install setting, `styles.builtin`, a JSON list of `{id, name, instructions}`. A person's own styles are rows of `styles` in their tenant's database.** A built-in style is not a row: the turn reads the setting, `styles.ParseBuiltIn` checks it, and the list is passed to `internal/styles` with the other options. A person cannot change or delete a built-in style; one who wants other words makes a style of their own.

The two kinds never share an id: a person's own style has sixteen hexadecimal characters, and a built-in id is lower-case letters, digits and hyphens that may not have that shape. A name may not be another style's in any letter case, built-in or the person's own, so the picker never shows two of one name.

## Why

- The built-in wording is a value someone might change, so it is a setting, not code (AGENTS.md). The registry has no list type, and four keys for four styles would fix how many there are; one JSON setting lets the admin add, remove, reword or reorder them, and `ParseBuiltIn` refuses a list that does not read, naming the setting.
- It is install scope because it is the same for everyone, and keeping it out of a person's reach keeps the General screen from offering a JSON field. A person has their own styles for anything else.
- Copying the built-in styles into each tenant's database would freeze the shipped words in every tenant and make a better wording reach no one. Read from the setting, a change reaches every chat that uses the style from its next call.
- Keeping the built-in styles out of `chat_styles`' foreign key costs nothing: a chat whose built-in style was taken out of the setting counts as having none of its own and uses the default.

## What it costs

- The admin edits a JSON string to change a built-in style. A mistake is refused when the setting is read, and until it is put right the turn sends no style and the style routes fail, with the reason in the server's log.
- A person cannot hide a built-in style they never use.
