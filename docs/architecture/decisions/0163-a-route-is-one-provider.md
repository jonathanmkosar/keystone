# 0163. A route is one provider, and the turn's model stays the one that was asked for

November 25, 2026 · decided in P3-08

## The question

A turn opens one model and calls it once for each step. With routing it has a list of models, a cap to ask before each call, and a usage record to write after. Where does that go, when another lane is changing the turn at the same time?

## The decision

**The route is a `seam.Provider`.** `routing.Chain` holds the route's opened models and is called as one model is. Falling back, the cap's gate and the records of a failed attempt all happen inside `Stream`, through two hooks the caller passes in.

**The chain holds back the start of a stream and its error until the first block arrives.** Until then nothing has reached the caller, and the next model can take the call.

**The turn learns which model answered from `Chain.Last()`**, after the call. The cost, the audit entry for the call's end, the usage record and the reply's own record use that model. The turn's `model` stays the one the person asked for.

**The seam has one more error kind, `spend_cap`**, which no provider gives. A call the cap stops is a failed model call like any other.

**The usage record of a call that ended is written in the turn's `call`**, beside the audit entry, and not inside the chain.

## Why

- The turn changes in two places: where the model is opened, and after a call. Everything else is in `internal/routing`, `internal/usage` and one new file, `internal/turn/route.go`. Two lanes can work on the turn at once.
- A background task, a sub-agent and the memory pass will each call a model without being a turn. A provider is the one thing all of them already hold.
- Holding events is the only way to fall back without the client seeing a start, an error and a second start. A stream's start carries nothing a person reads, so holding it costs no time anyone would notice.
- Keeping the turn's model as the one asked for means the chat's next turn starts from the person's choice again, and a fallback is an event in one reply and not a change of model for the chat.
- The audit log's rule is that a call whose first entry cannot be written is not made. The `Moved` hook writes the next model's started entry before the chain calls it, and an error there ends the call.
- A turn's `call` already knows how the call ended, the person's stop included, and it serves the test tenant, which has no chain. Writing the record there gives one rule: every call that ended has a record.

## What it costs

- The chain remembers the last model between `Stream` and `Last`, so one chain belongs to one turn. A caller that shared a chain across goroutines would read the wrong model. The turn makes a new chain for each turn.
- A title written through the chat's own route is recorded as a `title` call, but if that call falls back, the failed attempt is recorded as `chat`, because the chain was made for the chat.
- The seam's list of error kinds is no longer only what providers say.
