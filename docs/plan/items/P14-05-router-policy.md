# P14-05. The admin's router policy

Status: doing (chats)
Lane: chats + interface
Depends on: P14-04
Owner gate: The owner's answers on admin and views (A1 to A5), for the screen only.
Built without the gate: All of it: the settings, the server's refusals, and the picker reflecting them. The admin sets the policy from the command line until P15-03.
Scope: The six `tenant-admin` router settings, the refusals `model_not_allowed` and `auto_required`, and `chats.default_model` ignored outside the allowed list. Screen half: greyed models and a hidden switch.
Proof: A person who may not pick a model, or may not leave Auto, is refused by the server and shown why on the screen.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "The admin's policy for each person", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `chats/p14-05-router-policy`, cut from `reboot` at `165ada6` (P14-04 merged). The six `tenant-admin` settings of the spec: four exist (`router.ladder`, `router.decision_model`, `router.step_notes` from P14-03, `router.auto_default` from P14-04, decision 0672); this item adds the last two and enforces all of the policy the router does not read.

1. **Settings** in `settings/models.json`, after `router.auto_default`: `router.may_switch_off` (bool, true, tenant-admin) and `router.allowed_models` (string, empty, tenant-admin; registry names separated by commas, empty means every model). The admin sets them with `go run ./tools/tenant setting set <tenant-id> <key> <value>`, which already stores a `tenant-admin` setting as the admin (`docs/architecture/tools.md`), until the admin's settings screen (P15-03) draws them from the registry.
2. **The policy** (`internal/turn/policy.go`, new): read once per use from the person's settings and their usable ladder. "Auto is required" only while Auto is offered, that is while the ladder has a usable step; with no ladder there is no Auto to stay on.
3. **The refusals.** 403 `model_not_allowed` for a message that names a model the person may not pick by hand; 403 `auto_required` for a message that names a model while Auto is required (it would take the chat off Auto). Checked when `POST /chats/{id}/turns` is called, before anything is queued, and again when the turn leaves the queue (a turn queued before the admin changed the policy, or sent by `Runner.Send`), where it ends as a failed turn with the same code. Picking a model is how a chat changes its model (decision 0690), so this is the "model change" refusal too.
4. **Chat create and the chats off Auto.** `POST /chats` takes no model, so there is nothing to refuse there: a chat made while Auto is required starts on Auto whatever `router.auto_default` says. A chat off Auto (made before, or by a route that makes chats off Auto) is put on Auto by its next turn that names no model, and its model choice says `auto` already.
5. **Try a stronger model** stays on Auto, and Auto uses the ladder whatever `router.allowed_models` says (the spec), so it is not refused by the policy; a test holds that.
6. **`chats.default_model` outside the allowed list is ignored**, and so is the chat's last model: the next turn with no model named takes the first allowed one. `ModelChoice.allowed` is the allowed subset of `models`, `may_switch_off` is the setting (true while there is no ladder), and `auto` is true when the chat is on Auto or Auto is required.
7. **Screens** (provisional on the A1 to A5 screen answers and the chat mockups): the picker on a computer and the phone's model sheet grey a model the person may not pick, with the reason ("Your admin has not allowed this model to be picked by hand."); when Auto is required the model rows and the switch off Auto are hidden and one line says why; the new chat screen reads the two settings as it reads the ladder; a refused message says why in the person's words. The words in a pure module with a Node test.
8. **Proof.** `go test ./internal/turn` (`policy_test.go`): a person who may not pick a model is refused with 403 `model_not_allowed` and nothing is queued, the allowed one works, `chats.default_model` outside the list is ignored, a queued turn naming a model refused at the turn's start; a person who may not leave Auto is refused with 403 `auto_required`, a new chat and an old one off Auto are on Auto, the switch reports false; a stronger model still works with an allowed list that leaves out the ladder; no ladder means no refusal. The settings with the spec's type, default and scope. A test of `tools/tenant setting set` storing the policy as the admin. The screen half seen in a browser at desktop and phone widths.

Left to others: the admin's settings screen (P15-03, generic and scope-driven), unattended work on Auto (P14-06), Auto in usage (P14-07).
