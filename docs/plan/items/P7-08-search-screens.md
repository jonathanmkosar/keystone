# P7-08. Web search screens and monitoring

Status: todo
Lane: agents + interface
Depends on: P7-06, P15-02
Owner gate: Approval of the settings mockups (P1-07), for the screens only.
Built without the gate: The server side in full; the screens from the drafts, provisional.
Scope: Routes for a person's own providers and for the install's providers (admin only, keys written and never returned); a person's Settings → Web search; the admin's Web search; search use in the admin's install monitoring; the step row naming a provider that was not the first tried.
Proof: A person manages their own providers and sees the install's by name and state only. No route returns a key. The admin's figures equal the audit log's counts.

See `docs/plan/SPEC-web-search.md`, "Records and screens", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
