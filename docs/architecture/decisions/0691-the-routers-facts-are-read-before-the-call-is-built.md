# 0691. The router's facts are read before the call is built, from the branch and the message

November 27, 2026 · decided in P14-04

## The question

The router needs the call's estimated tokens, whether it holds images or files, and how many tools are offered (`router.Facts`). The call itself is built by the context builder for one model: its recall, map, summary and tools depend on that model. The router has to choose the model first.

## The decision

- `Facts.Tokens` is the budget's estimate (`budget.Estimated`, with its margin) of the branch the first call will be built from and the turn's own message, before it is written: the whole current branch for a new message, the branch up to the message an edit replaces, the branch up to the person's message for a retry or a stronger model. Thinking blocks are left out, as a call to another model leaves them out.
- `Facts.Images` and `Facts.Files` are read from that branch and the turn's images (P9-02); `Facts.Tools` is the number of tools a model that takes tools would be offered (`agents.tools_enabled`, the browser switch and the never policy applied).
- `Facts.ReplyTokens` is `chats.turn_max_output_tokens`, `Facts.Held` the chat's held mark, `Facts.Turns` the chat's other turns, and `Facts.PreviousStep` the model of its newest turn that had one.
- The call is then built for the chosen model as always, and the budget's guard checks the real call before it is sent; a call that does not fit is refused as before, and the fallback chain moves on.

## Why

- Building the call once for each step would cost a recall, a summary read and a count for every candidate, before every message.
- The standing start, the profile and the map are about the same size on every step, and the size limits on the ladder are coarse (`name<=100000`), so the branch is what decides them.

## What it costs

- A call near a step's limit can be estimated under it and built over it; the budget then refuses it and the chain moves on, where a full estimate would have skipped the step. The live check (P14-08) shows whether the margin should be larger for the router.
