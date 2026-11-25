# 0210. Recall searches once, before the route, and a call is built for the least of its route

November 25, 2026 · decided in P6-05

## The question

Recall's answer depends on the model: a model that may not read sensitive content is given less. Routing's answer depends on recall: a call that carries sensitive content may go only to models that may read it. And a turn's route is one provider with a fallback chain behind it, which sends the same request to whichever model answers (decision 0163). In what order are these decided, and who is a call built for?

## The decision

**The store is searched once for each turn, with the person's newest message, before the route is chosen.** `Find` returns a `Found` that holds what every kind of model may be given, so the search does not depend on the model.

**The route is told the call is sensitive** when the chat's own model may read sensitive content and `Found.For(true)` kept a sensitive item, or when the chat was sent sensitive content before (decision 0211). Routing then keeps only models that may read it.

**Each model call is built for the least of its route.** The answer passed to `Found.For`, to the person map and to the lookups is true only when every model of the route may read sensitive content.

## Why

- A route can fall back to a hosted model after a local one. A request built for the local model, sensitive items and the whole map in it, would then reach the hosted model unchanged. Building for the least of the route closes that door without a second request for each model.
- Searching before the route is the only order in which the route can know what the call will carry. The search does not need the model, so nothing is lost by doing it first.
- Marking the route sensitive only when the chat's model may read sensitive content keeps a hosted model's chat on its hosted model: it is given the open recall, which holds nothing sensitive, so nothing needs to move.
- A local model with a hosted model behind it, asked a plain question, keeps its fallback. It is given the open map and the open recall, which is a little less than it could read, and the turn keeps going when the local model is down.
- A `Found` taken once is a snapshot. The route and the call read the same one, so a page made sensitive between the two cannot slip into a route that was chosen without it.

## What it costs

- A local model asked a plain question, with a hosted fallback configured, is given the open map. The person loses the names of their sensitive spaces in that call. Taking the hosted model out of the fallback chain gives them back.
- Recall runs before the model is named, so a slow store delays the turn's first event by up to `knowledge.recall_timeout`.
- The person map is still loaded for each call, apart from the search. It reads the store's catalog marker and is bounded by the adapter's own timeout, not recall's.
