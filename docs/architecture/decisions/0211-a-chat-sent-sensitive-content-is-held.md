# 0211. A chat that was sent sensitive content is held to models that may read it

November 25, 2026 · decided in P6-05

## The question

A turn recalls a sensitive page for a local model, and the model's reply repeats some of it. The reply is now in the chat's history. In the next turn the person picks a hosted model, or the route falls back to one. The history goes with the call. Is that allowed?

## The decision

**No. Once a turn of a chat has been given content from a sensitive space, every later call of that chat is routed as sensitive.** The turn's row in `turn_recall` carries the mark, `recall.Held` reads it, and the turn asks it before every route, the title's included. A hosted model picked for such a chat is left out of the route; with no local or trusted model left, the turn ends with the code `sensitive` and says to pick one.

**What marks a chat:** a kept recall item from a sensitive space, and a lookup whose result was sensitive (`recall.Hold`, for the tool loop). The person map alone does not.

**A chat whose mark cannot be read counts as held.** **A turn whose record cannot be written sends nothing sensitive**, since the chat could not be held afterwards.

## Why

- A reply is content from a sensitive space when it repeats one. The rule is about content, not about which part of a call carries it.
- The mark is on the chat, the unit whose history a call carries, and it is kept in the tenant's database, so a restart does not forget it.
- The mark is never taken back by a later save. A rule that could be unset by writing the same row again would not be a rule.
- The map names spaces and titles, and a model rarely repeats them unasked. Holding every chat of a person who has a sensitive space and talks to a local model would close hosted models to all of those chats. That is the owner's question in the journal; until then the map is kept out of a hosted model's call by decision 0210 and does not hold a chat.

## What it costs

- A chat is held as a whole. Editing back to a point before the sensitive turn does not free it. A new chat does.
- A chat that recalled a sensitive item which the context builder then left out for room is held all the same. That is the safe side.
- A reply on a local model that repeats a title from the map can reach a hosted model later in the same chat, if the person moves it there. If the owner says the map should hold a chat too, it is a small change in `internal/turn`.
