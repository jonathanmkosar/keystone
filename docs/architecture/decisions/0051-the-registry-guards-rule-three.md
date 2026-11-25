# 0051. The registry is a guard for rule 3, and `local` is checked against the address

November 24, 2026 · decided in P3-02

## The question

Fixed rule 3 says content from a sensitive space reaches only models the registry marks local or trusted. Routing (P3-08) and recall (P6-05) are where a call is stopped. What is the registry's part, and how far does it check a mark it is given?

## The decision

**`internal/models` is a guard.** It is listed under `rules.sensitive_content` in `fixedRuleGuards`. By decision [0014](0014-fixed-rules-and-tenant-settings.md) it may not import the settings registry or the tenant settings, itself or through another package. The limit on how many models a tenant holds is passed in by the caller.

**One function answers the question.** `Model.MayReadSensitive()` is true for `local` and `trusted` and false for everything else: `hosted`, an empty class, a class it does not know. Routing and recall call it. They do not compare classes themselves.

**The package imports the tenant store and the standard library, and nothing else of Keystone's.** It does not import the seam. Making a provider from an entry is in `internal/adapters`, which does.

**`local` must look local.** An entry marked `local` is refused unless its endpoint is this machine or the home network, read from the address alone: `localhost`, a name with no dot, a name ending in `.local`, `.lan`, `.home.arpa`, `.internal` or `.localhost`, or a loopback, private, link-local or `100.64.0.0/10` address. The list is in code. It is not a setting.

**`trusted` is the admin's word.** It is not checked against anything. It is how a model on a machine with any other name is allowed sensitive content.

## Why

- The rule's words are "models the registry marks". If the code that reads the mark could read a setting, a setting named something harmless could make every model read as trusted. A guard has no way to.
- The fewer packages a guard imports, the fewer can one day start reading settings and turn the `fixed rules` check red. The audit log keeps away from the seam for the same reason (decision [0009](0009-audit-log-append-only.md)).
- `local` is a claim about where a model runs, and the address shows most mistakes: a hosted address pasted into an entry copied from a local one. `trusted` is a choice, and there is nothing to check a choice against.
- Looking a name up would need the network, would give a different answer on another network, and can be made to lie. Reading the address is the same everywhere and offline.
- The suffix list would be a setting by the usual rule, since someone might want to change it. It is not one because the guard cannot read settings, and passing it in would let a setting widen `local` to any name. `trusted` is the way through for a name the list does not have.

## What it costs

- A home machine reached by a name outside the list, such as one a mesh network gives out, cannot be `local`. It is marked `trusted`, which reads the same to rule 3 and tells the truth less exactly. A decision record can add a suffix.
- The check reads an address. It does not prove where the model runs. A private address can forward anywhere. The mark is still the admin's statement, and this only catches slips.
- The guard is proven unable to read a setting. That every sensitive call asks it is for the `sensitive content` check, which arrives with routing and recall.
