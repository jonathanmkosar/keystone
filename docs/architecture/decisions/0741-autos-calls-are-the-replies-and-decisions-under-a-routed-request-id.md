# 0741. Auto's calls are the replies and the decisions under a request id the router routed

November 28, 2026 · decided in P14-07

## The question

A usage record names no turn and no chat: it carries the request id of the API call that set it off (decision 0430). A turn on Auto makes several kinds of call under its request id: its replies and tool steps (`chat`), the router's verdict (`decision`), and maybe a title, a summary of its oldest turns or a memory pass. Which of them are "on Auto", and how is a record known to be one?

## The decision

- A request id is on Auto when `router_verdicts` has a row with it: every message on Auto is routed, and each routing writes one, a stronger model's rewrite and a second routing after a local step did not answer included.
- Of the records under such a request id, those of kind `chat` are the replies on Auto (`replies`), and those of kind `decision` the router's decisions (`decisions`). Auto cost is the two together.
- A title, a summary, a recap or a memory pass under the same request id is not Auto's. The router does not choose their model; the report counts them under their own kinds of work as before.
- The request ids are found by one join from the usage records of the period's months to `router_verdicts`, through a new index on `router_verdicts(request_id)`, so a report reads them in one query, not one per request.

## Why

- The request id is how the report already places every record (decision 0430), and how a task's budget counts its calls: no new link between the records and the turns is needed, and the usage records stay as they are, append-only.
- Pricing a title or a memory pass at the top step would compare costs Auto never chose.
- Counting the decisions in Auto's cost shows what the router itself costs, which a hosted decision model makes real.

## What it costs

- A request id with turns on Auto and off would count all its chat calls as Auto. Today a request id has one turn, except a background task's, whose turns P14-06 routes alike.
- One index on a table every message on Auto writes to.
