# P10-06. Location

Status: doing (interface)
Lane: interface + chats
Depends on: P4-04
Owner gate: none
Scope: Asked for by the owner on November 27, 2026 ("add GPS"), as he chose: per message. A "Use my location" control behind the composer's + asks the phone for its position once, and the location, rounded to about a kilometre, goes with that message only, so the model can answer weather and nearby questions.
Proof: A message sent with location carries the rounded position into the model's request and the record, a message without it carries none, and nothing reads the position without the person's tap.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result
