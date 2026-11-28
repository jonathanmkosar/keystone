# 0672. The router adds the settings it reads, and leaves the rest of the admin's policy to the items that read them

November 27, 2026 · decided in P14-03

## The question

The spec lists six settings of the admin's policy for each person (`tenant-admin`) and the router's own settings (`install`). P14-03's scope is "the router's settings"; P14-05's is "the six `tenant-admin` router settings". The router cannot work without three of the six: the ladder, the decision model and the step notes. Which item adds which?

## The decision

- P14-03 adds, in `settings/models.json`, with the spec's names, types, defaults and scopes:
  - the six the spec's "The router's own settings" gives the router: `router.min_confidence` (0.6), `router.verdict_timeout` (2s), `router.verdict_max_chars` (8000), `router.default_effort` (medium), `router.max_effort` (high), `router.local_down_for` (2m), all `install`;
  - the three of the admin's policy the router reads: `router.ladder`, `router.decision_model` and `router.step_notes`, all empty and `tenant-admin`.
- `router.default_effort` lists the options `low`, `medium` and `high`, the efforts a verdict chooses among; `router.max_effort` lists the five efforts of the seam.
- `router.auto_default`, `router.may_switch_off` and `router.allowed_models` are left to P14-04 and P14-05, which read them: the chat's `auto`, and the refusals `auto_required` and `model_not_allowed`.
- The `systemone.*`, `anthropic.effort_*` and `chat_completions.send_reasoning_effort` settings of the same table came with P14-01 and P14-02.

## Why

- A setting is added with the code that reads it, so the registry never lists a value nothing uses; P14-04 depends on this item and P14-05 on P14-04, so nothing waits.
- The router's tests read the registry and hold each of its nine settings to the spec's type, default and scope.

## What it costs

- P14-05's "six settings" are three by the time it starts; its item text can say so when it is picked up.
