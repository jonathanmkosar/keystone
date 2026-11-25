# 0131. The estimate counts characters, and leans high

November 24, 2026 · decided in P3-07

## The question

Where a provider offers no count, Keystone has to guess a request's tokens. A real tokenizer for each model family is an outside module or a large table, and the local models a tenant registers can be any family. How is the guess made, and what happens when the request asks for more reply than the model writes?

## The decision

**Characters divided by a setting.** Text is counted by its characters over `budget.chars_per_token`, which ships at 3. English prose runs nearer 4, and code, JSON and other scripts run lower.

**Flat numbers for what has no characters.** Each message, each tool, each image and each file named by reference adds a number from settings. A file sent inline counts as text when its type is text, and as its base64 length otherwise.

**A margin on top, from a setting.** `budget.margin_percent`, 15, is added to an estimate and never to a provider's own count.

**The room for the reply is never more than the entry's output limit.** A request that asks for more than the model writes keeps the output limit, and is not refused here.

## Why

- No module is added and nothing is downloaded, so `verify` stays offline, and one rule covers every model a tenant can register.
- An estimate that is too high costs a little context. One that is too low costs a failed call, after the wait. So every choice leans high, and the two that matter most are settings an admin can move.
- The budget's refusal is about the window. Whether a provider clips or refuses a reply limit above its own is the adapter's to say, and refusing it here could stop a call that would have worked.

## What it costs

- On a model with a small window the estimate gives away a real share of it: at the shipped values, roughly a third against English prose. A model that needs every token should be reached through an adapter that counts.
- An image is one number whatever its size, and a large PDF sent inline is counted far above what a provider that reads pages would bill. Both err toward refusing.
- A file named by reference is a guess that can be wrong in either direction until its size is known to the caller.
