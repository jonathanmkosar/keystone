# P12-04. Google from New Keystone

Status: doing (agents)
Lane: agents
Depends on: P12-02
Owner gate: The owner's go-ahead to reuse New Keystone's Google sign-in, given on November 26, 2026.
Scope: Asked for by the owner on November 26, 2026. Keystone takes over the Google sign-in New Keystone already holds on this machine, so Calendar works without a new consent; Gmail still needs the Gmail API and its scopes on the client.
Proof: The owner's Calendar tools answer through Keystone with the sign-in brought from New Keystone, and no token is shown or logged.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan and result (the integrator, November 26, 2026)

New Keystone asked Google for `openid email` and the Calendar scope Keystone uses, with the client already copied into the owner's vault, so its refresh token serves Keystone as it is. Built: `connectors.AdoptGoogle` and `tools/tenant connector google`, which put the token in the vault, record the account and switch on the connector of each service the scopes cover, through the code a sign-in made here uses; tests in `internal/connectors/googleadopt_test.go`; `docs/architecture/google.md` and `docs/runbooks/google.md`. It is command line only. Gmail is not brought: New Keystone never had it, and it still needs the Gmail API, its scopes and Keystone's redirect address on the client.
