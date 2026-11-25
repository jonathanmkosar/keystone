# 0014. A fixed rule is a name no setting can have, enforced by code that cannot read settings

November 24, 2026 · decided in P2-04

## The question

The plan has four rules that no setting can switch off, and a standing check that says so. Until now the rules were sentences in `PLAN.md`. A check needs something concrete to test. What is a fixed rule in the code, and what would it mean for a setting to switch one off?

The same item stores each tenant's own setting values. Where does that code live?

## The decision

**A fixed rule is an entry in a list, not a setting.** `settings.FixedRules()` returns the four, each with a key and the plan's text. A rule has no value, no default and no scope. There is no switch to guard, because there is no switch.

**The keys are in a space kept for them.** Every rule's key starts with `rules.`. No setting may use that space: the registry refuses one, the tenant settings refuse to store one, and the `tenant_settings` table refuses the row. So a setting cannot be passed off as a rule, and a rule cannot be given a stored value by any path.

**The code that enforces a rule cannot read a setting.** A setting with an innocent name could still weaken a rule if the enforcing code read it. So that code lives in a package that imports neither the registry nor the tenant settings, itself or through another package. The `fixed rules` check lists these guard packages for each rule and reads their imports with `go list -deps`. Today `internal/tenant` guards tenant isolation and `internal/audit` guards call recording.

**The list must match the plan.** The check compares the text in code with the numbered list in `PLAN.md`, word for word. Changing a rule takes a change to both, and so a decision record.

**Tenant settings are a package of their own, `internal/tenantsettings`.** It needs both the registry and a tenant's store.

## Why

- A rule that was a setting with a locked value would be one mistake away from unlocked. A rule that is not a setting has nothing to unlock.
- "No setting can switch it off" cannot be tested by trying every setting, since a new one arrives with every item. It can be tested by showing that the enforcing code has no way to read any setting. That stays true as settings are added, with no change to the check.
- Tenant settings do not belong in `settings`: the registry is plain data compiled into every tool, and should not bring a database with it. They do not belong in `internal/tenant` either: the store handle guards tenant isolation, so by this decision it may not import the registry.

## What it costs

- Each later rule's code needs a home that reads no settings. The approval policy (P7-02) reads per-tool settings, so the part that says "after outside content, always ask" goes in a small package of its own, and the part that reads settings calls it. The same holds for sensitive content in routing (P3-08) and recall (P6-05). Each of those items adds its package to `fixedRuleGuards` in `tools/verify/fixedrules.go`.
- The check proves that a guard cannot read a setting. It does not prove that the guard is correct, or that every path goes through it. Each rule's own standing check does that: tenant isolation, approval, sensitive content, every call recorded.
- A guard may not import a package that one day starts to read settings. That is why `internal/audit` does not import the seam (decision [0009](0009-audit-log-append-only.md)).
- The admin cannot change a setting whose scope is `tenant`. Decision 0001 says that value is the person's own, and this follows it. If the admin needs to reset one, that is a one-line change in `MayChange`.
