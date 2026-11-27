# P14-05. The admin's router policy

Status: todo
Lane: chats + interface
Depends on: P14-04
Owner gate: The owner's answers on admin and views (A1 to A5), for the screen only.
Built without the gate: All of it: the settings, the server's refusals, and the picker reflecting them. The admin sets the policy from the command line until P15-03.
Scope: The six `tenant-admin` router settings, the refusals `model_not_allowed` and `auto_required`, and `chats.default_model` ignored outside the allowed list. Screen half: greyed models and a hidden switch.
Proof: A person who may not pick a model, or may not leave Auto, is refused by the server and shown why on the screen.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "The admin's policy for each person", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
