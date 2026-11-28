# P15-02. People, the install and monitoring

Status: todo
Lane: core + interface
Depends on: P15-01
Owner gate: None for the server side. The screens follow the approved drafts.
Built without the gate: Everything.
Scope: The people, one person's usage and the whole install's routes; adding a person with a setup code and `/setup`; disabling and enabling a person; Operations split, with the person's own under Settings and install-wide parts only in the admin account (a decision record changes 0460); and the admin screens People, A person and Install.
Proof: The numbers equal each person's own Usage and Operations figures, and the install's equal their sum. A disabled person cannot sign in, their sessions end, their schedules do not run and their share links answer as revoked; enabling restores it. A setup code works once and not after it expires. No content of a person reaches a reply.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "Disabling a person" and "People, the install and monitoring", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
