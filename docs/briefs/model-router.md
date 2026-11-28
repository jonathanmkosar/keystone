# Brief: model router

Type: new (it changes one earlier decision, see "What already exists")
Requester: the owner
Owner: the owner
Status: clear
Last updated: November 26, 2026

## The ask

"Thinking about a model router and where it should live? Should it be in harness or separate? And should it live on harness pc separately or on the Mac min? If on Mac min separately from the engine for qwen? I want all request to go through it decide which model to use. How do a lot of router do this now? I think I want all request to go to qwen and only go to haiku as next step if under 100k then decide what is best afte that sonnet or opus. Decide also the effort is neede. I would like ui to default to auto and able for admin to set for users whether they can switch off auto or not and what models they can use if not only auto."

## The problem

Today a person picks one model for a chat and every message goes to it. Easy messages cost as much as hard ones, and the free local model is used only when someone remembers to pick it. The owner wants Keystone to choose the model and the effort for each request, starting from the local model and moving up only when needed, with an admin deciding who may turn that off.

## Why now

The model seam, the registry, routes, fallbacks, the spend cap and usage records are built. The next step in the models lane is the live checks with real keys, which is when model choice starts to cost money.

## Done looks like

- A new chat starts on Auto. Each reply names the model that answered, the effort, and the reason for the verdict, in its footnote and in the inspector.
- On Auto a request passes the rules, then the decision model's verdict, then the model of the step the verdict names. The local chat model answers when the verdict says it can.
- Each message on Auto is routed afresh before its call. A hard message followed by an easy one goes back down.
- With the local machine off or not answering, a chat on Auto is answered by the small hosted model and the reply says so. A held chat waits and says why.
- A held chat on Auto reaches no hosted model, whatever the verdict.
- An admin sets three things for each person: whether Auto is the default, whether the person may switch Auto off, and which models the person may pick. The server refuses a named model to a person who may not pick it; the screen only reflects that.
- Sub-agents and scheduled runs go through Auto with the same ladder.
- Every verdict is a recorded call: what it was shown, its answer, the probability, and how long it took.
- The usage report shows what Auto cost against the same calls on the largest model, and how often a person asked for a stronger model.

## Out of scope

- A trained router, or fine-tuning the decision model. The first version uses rules and one decision call, and keeps the records training would need.
- A door for other apps. Nothing besides Keystone uses the router for now (the owner, November 26, 2026). It stays behind one interface so a door can be added.
- A hosted decision model.
- Replacing a reply part of the way through. Decision 0160 stands.
- The rules for titles, summaries, the memory pass and embeddings. Auto is for chat replies, sub-agents and scheduled runs.

## Constraints

- Fixed rule 3: content from a sensitive space reaches only local or trusted models. The router sits behind the privacy filter and can never widen a route.
- Every model call is recorded, the router's own call included.
- The spend cap of 100 US dollars a month still gates every paid call.
- Nothing is a constant: the steps, the size limit and the timeouts are settings.
- The local model runs on another machine on the home network and may be off. That machine has 64 GB of memory (the owner, November 26, 2026), which holds the 27B chat model and a 9B decision model together with room left.
- Ollama serves the local chat model, and serves the decision model beside it through its own decision endpoint, `/v1/systemone`. The 9B decision model needs Ollama 0.35.1 or later. Ollama takes at most 64 questions and a 64 KiB request for one decision.

## What already exists

- `internal/routing`: `Plan` returns the models one call may go to, in order, and `Chain` falls back along them. It holds no state and reads no setting (`docs/architecture/routing.md`).
- The rule for a chat reply today: "The model the person picked. No setting overrides it." Auto changes this rule.
- A chain moves to the next model only before any block of a reply has been passed on (decision 0160). A reply the person has started to read is never replaced by another model's.
- A chat sent sensitive content is held to local and trusted models for good (decision 0211).
- The chat keeps the model the person asked for, and each reply records the model that answered (decision 0163).
- The seam carries a thinking budget in tokens, not an effort. The Responses adapter turns the budget into low, medium or high (decision 0111). Choosing an effort needs the seam to carry one.
- The owner has three models registered: a 27B local chat model on the home network through the Chat Completions adapter, and a small and a mid-sized hosted model through the Anthropic adapter. The largest hosted model is not registered yet.
- Hosted decision models cannot run locally: their weights are not published. Open decision models with published weights can, in 9B and 27B sizes and smaller. A hosted decision model would read every message before it is routed, so it could never give the verdict for a held chat.
- The reboot brief and the plan say the router "is its own app" and that Keystone "only provides the seam". The owner changed that on November 26, 2026: it is built inside Keystone (see Decisions).

## Terms

- **Auto**: the choice in the model picker that lets the router pick the model and effort.
- **Step**: one rung of the ladder the router climbs, for example local, small hosted, large hosted.
- **Verdict**: the router's answer for one request: the step, the effort and a one-line reason.

## Decisions

Rows that begin "Settled" were answered by the owner on November 26, 2026. "Assumed" means the owner had no preference and the recommended answer stands until he says otherwise. The rest are proposed.

| Decision | Why | Hard to reverse? | Owner |
|---|---|---|---|
| Settled. The router is a package in Keystone, fed into `routing.Plan`, and not a separate service. This replaces "the router is its own app" in the reboot brief and the plan | It needs the held mark, the registry, the cap, the admin policy and the usage records, which all live here | No, if it stays behind one interface | the owner |
| Settled. Nothing of the router runs on the machine that serves the local model. That machine serves two models: the chat model and the decision model | That machine serves a model. If it is off, routing must still work | No | the owner |
| Settled. Rules first, then a verdict from a small local decision model that runs beside the chat model. With no decision model registered, the local chat model gives the verdict | Rules are free and exact. A decision model returns a typed answer with a probability far sooner than a 27B chat model writes one. The owner raised this on November 26, 2026 | No | the owner |
| A verdict is a call of its own on the seam, as an embedding is (decision 0410), with one adapter for the decision endpoint | A decision model takes a state and typed questions and returns typed answers. None of the three adapters speaks that | It changes the seam's contract, which every lane builds on | the owner |
| Settled. The decision model is shown the newest message and the call's facts: its size, whether it holds images or needs tools, and the step the previous message used. It is not shown the whole chat | A decision takes a small request, and a long chat is far larger. It also keeps a verdict quick | No | the owner |
| Settled. The admin policy is three settings for each person, at the `tenant-admin` scope: Auto is the default, the person may switch Auto off, and the models the person may pick | It is what the owner asked for, and the scope exists (decision 0001) | No | the owner |
| Settled. Sub-agents and scheduled runs use Auto with the same ladder | Nobody is watching them to pick a model | No | the owner |
| Assumed. The verdict chooses an effort of low, medium or high. The seam carries the effort and each adapter turns it into what its provider takes. Higher efforts are a setting | Every provider that takes an effort takes these three (decision 0111) | It changes the seam's contract | the owner |
| A verdict whose probability is under a setting moves the request up one step | An unsure verdict is the cheapest sign that a request is hard | No | the owner |
| Settled. Every message is routed afresh before its call, and the choice holds for that message's whole turn, tool steps included (the owner, November 27, 2026; previously a chat stayed on the step it reached and only moved up) | The point is to pick the model before each message, so one hard message does not keep a chat on a costly model. A switch loses the prompt cache for that call, and Usage shows it | No | the owner |
| Settled. With the local machine off or not answering, rules alone decide and a chat on Auto starts at the small hosted model, with a notice on the reply. A held chat waits and says why | Routing must not depend on one machine, and a held chat may never reach a hosted model | No | the owner |
| Assumed. "Under 100k" is the size of the call in tokens: under it, the small hosted model is the step after the local one; over it, the call goes straight to a larger model. The number is a setting | It is the reading that fits the ladder the owner described | No | the owner |

## Risks

- The local model's verdict is slow or wrong -> a timeout that falls to rules alone, and a record of every verdict so it can be measured.
- The local model answers when it should not have -> a "try a stronger model" action on every reply, counted as a miss.
- Auto spends more than a fixed pick -> the usage report compares them.
- A switch of model loses the prompt cache for that call -> the reason line names the switch, and Usage shows cache reads, so the cost is measured.
- Decision models are tested on sorting requests by intent, not on judging how hard one is -> every verdict is recorded with its probability and with whether the person asked for a stronger model.
- A verdict waits behind the chat model on the same machine -> a timeout, a setting, after which rules alone decide.

## How we will prove it works

- Recorded turns in the test tenant for each step of the ladder, with the fake provider standing in for the decision model.
- A check that a held chat on Auto reaches no hosted model, with a verdict that names one. It joins the `sensitive content` check in `verify`.
- A check that the router's package is not a way round rule 3: it only ever narrows or orders what `routing.Plan` allows.
- A check that a person who may not switch Auto off, or may not pick a model, is refused by the server.
- A check that with the local machine not answering, a verdict times out and the rules decide.
- A live check with the owner's own models: the decision model and the chat model on his local machine, and his hosted models.
- The usage report's comparison, read by the owner after a week of his own use.

## Release and sign-off

Built as numbered items on `reboot`, in the models, chats, agents and interface lanes, each merged with `verify` green. The seam change is contract first: it is agreed and merged before the lanes that use it start. The owner signs off after the live check and a week of his own use on Auto. Nothing is merged into `main` and no pull request is opened unless the owner asks.

## Open questions

| Question | Owner | Blocks start? |
|---|---|---|
| Is Auto measured in the background before it becomes the default? | the owner | No |
| Is the local machine's Ollama at 0.35.1 or later? | the owner | No. It blocks the live check only |
| When is the largest hosted model registered? Until then the ladder ends at the mid-sized one | the owner | No |

## Log

- November 26, 2026: started. Read the routing, models and seam docs and decisions 0051, 0111, 0160, 0163, 0211, 0240, 0241, 0323, 0382 before asking.
- November 26, 2026: the owner asked about a separate decision model beside the local chat model. The proposed verdict moved from the chat model to a small decision model, and the verdict became a call of its own on the seam. Missed in the first pass: decision models as a class were not considered before round 1.
- November 26, 2026: the owner gave the local machine's memory, 64 GB. A decision model fits beside the chat model.
- November 26, 2026: round 1 answered. Settled: decision model first, a chat stays and only moves up, and the small hosted model with a notice when the local machine is off. Assumed: 100k is the call's size in tokens.
- November 26, 2026: round 2 answered. Settled: nothing besides Keystone uses the router for now, Ollama serves the local models, the decision model sees the newest message and the call's facts, the admin policy is three settings for each person, and sub-agents and scheduled runs use Auto. Status clear. What this run missed: the first recommendation had the 27B chat model give the verdict; the owner's question about a separate decision model found the better design. A question to add to round one of a build that picks between models: is there a model made for this one job?
- November 27, 2026: the owner changed stickiness: every message is routed afresh before its call, held for that message's turn. The floor is gone.
