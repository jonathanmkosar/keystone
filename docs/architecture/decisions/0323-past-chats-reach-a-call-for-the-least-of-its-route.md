# 0323. Past chats reach a call for the least of its route, and a chat given a held chat is held

November 25, 2026 · decided in P5-05

## The question

Fixed rule 3: content from a sensitive space reaches only models marked local or trusted. A chat that recall held for such content may repeat it in its title, its messages and now its recap. The tool loop's journal (agents 001) asked whether the chat-reading tools should leave such chats out for a model that may not read sensitive content; the recommended answer, yes, waited for recall to mark chats, which it now does. `search_chats` and `read_chat` are in the server's registry, and a registry tool is given no word about the call it serves.

## The decision

**One answer, in a guard.** `internal/recap` decides: `Sensitive(chat)` is true when the chat is held (`recall.Held`) or its recap was made while it was, and a mark that cannot be read counts as sensitive; `MayShow(sensitive, mayReadSensitive)` lets anything of such a chat reach a call only when every model of the call's route may read sensitive content. The package reads no setting and is in `fixedRuleGuards`.

**The registry's tools are made for a call that may not.** `builtin.Add` registers `search_chats` and `read_chat` that leave out every such chat, counted and never named. That is what a model is offered.

**Each call makes its own, for the least of its route**, as the knowledge lookups are made (`internal/turn/lookups.go`): `Runner.chatTools`, from `lookupTools`, makes the two with `readsSensitive(provider, model)`, keeps them on the turn, and the calls the model makes in reply run those through a registry of their own. Only tools the registry holds are made.

**A chat given a held chat is held.** When a tool gives a call content from such a chat, it first holds the chat it is in with `recall.Hold`, and gives nothing if the hold cannot be kept. What a tool gives stays in the chat and is sent with every later call.

**A held chat's recap** is written only by a model that may read it, because its summary route is planned as sensitive, and is kept as sensitive.

## Why

- The least of the route is the answer recall, the person map and the lookups already use, so a call never carries something a fallback model may not read.
- Keeping the safe tools in the registry means a turn that could not make its own, or a caller with its own registry, still cannot leak; it only finds less.
- Making them in `lookupTools` needs no change to `internal/tools`, which other work is changing, and reuses the path a lookup already takes through the approval policy and the audit log.
- Holding the chat that asked is what a sensitive lookup does, for the same reason: otherwise its next call could go to a hosted model with the content in its history.

## What it costs

- A person who asks a local model about a held chat finds the chat they asked in held from then on, as reading a sensitive page does.
- A hosted model is told that some chats were left out, and how many, with no title. The owner's open question on the map (knowledge 004) recommends the same.
- A held chat is left out whole, even a branch from before the content arrived.
