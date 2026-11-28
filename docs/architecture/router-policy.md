# The admin's router policy

Files: `internal/turn/policy.go`, `web/app/src/policyWords.ts`

The admin's policy for each person (P14-05) says which models a person may pick by hand and whether they may take a chat off **Auto**. The server enforces it; the picker only reflects it. The design is `docs/plan/SPEC-router-and-admin.md`, Part 1, "The admin's policy for each person".

## The six settings

All six are `tenant-admin`: one value for each person, set by the admin. Four came before this item and are read where they always were; the last two are read here.

| Key | Type | Default | Read by | Meaning |
| --- | --- | --- | --- | --- |
| `router.ladder` | string | empty | the router (P14-03) | The ladder, smallest first ([router.md](router.md)). Empty: Auto is not offered. |
| `router.decision_model` | string | empty | the router | The decision model. Empty: the rules alone decide. |
| `router.step_notes` | string | empty | the router | Words describing each step to the decision model. |
| `router.auto_default` | bool | true | `POST /chats` (P14-04) | A new chat starts on Auto. |
| `router.may_switch_off` | bool | true | this file | The person may take a chat off Auto. |
| `router.allowed_models` | string | empty | this file | Registry names, separated by commas, the person may pick by hand. Empty: every model in their registry. Auto uses the ladder whatever this says. |

The settings are in `settings/models.json`. The admin sets them from the command line until the admin's settings screen (P15-03) draws them from the registry:

    go run ./tools/tenant setting set <tenant-id> router.allowed_models "house-small, house-medium"
    go run ./tools/tenant setting set <tenant-id> router.may_switch_off false

`tools/tenant setting` stores a `tenant-admin` setting as the admin ([tools.md](tools.md)), and a person cannot change either from Settings: they are listed there, read-only.

## When Auto is required

`router.may_switch_off` false holds only while Auto is offered, that is while the person's ladder has a step that is a usable chat model in their registry. With no ladder there is no Auto to stay on, and the person picks a model as before; `ModelChoice.may_switch_off` is then true (decision [0720](decisions/0720-auto-is-required-only-while-it-is-offered.md)).

## The refusals

| Code | Status | When |
| --- | --- | --- |
| `auto_required` | 403 | A message names a model while Auto is required: naming one would take the chat off Auto. |
| `model_not_allowed` | 403 | A message names a model the person may not pick by hand. |

`auto_required` is checked first: while Auto is required, every named model is refused with it, allowed or not.

They are checked twice (decision [0721](decisions/0721-the-policy-is-checked-when-a-message-is-sent-and-when-its-turn-starts.md)):

- **When the message is sent**, `POST /api/v1/chats/{id}/turns`, after the body is checked and before anything is queued. The answer is the API's error body with the code and a sentence for the person, and no turn is written. A retry or an edit that names a model is refused the same way. Picking a model is how a chat changes its model (decision 0690), so this is the refusal of a model change too.
- **When the turn leaves the queue** (`openTurn`), for a turn queued before the admin changed the policy and for one sent by code, `Runner.Send`, as unattended work sends it. The turn fails with the same code and sentence. A turn that has started already, going on after an approval, is not checked again.

**Try a stronger model** is never refused by the policy: it stays on Auto, and Auto uses the ladder whatever `router.allowed_models` says (decision [0724](decisions/0724-a-stronger-model-and-the-ladder-are-outside-the-allowed-list.md)).

## Chats on Auto

- `POST /api/v1/chats` takes no body, so there is nothing to refuse when a chat is made. A chat made while Auto is required starts on Auto whatever `router.auto_default` says (decision [0722](decisions/0722-a-chat-is-on-auto-whenever-auto-is-required.md)).
- A chat off Auto, made before or by a route that makes chats off Auto (a handoff, an incognito chat, a project's chat), is put on Auto by its next turn that names no model. Its model choice says `auto` already.
- When the admin switches `router.may_switch_off` back on, the chats stay on Auto until the person picks a model in them.

## The model a turn uses

`chats.default_model` outside the allowed list is ignored, and so is the chat's last model: a turn naming no model, off Auto, uses the chat's last model if it is allowed, else the default if it is allowed, else the first allowed model by name. With none allowed the choice has no model and, off Auto, says why; the turn fails with `no_model`. A chat on Auto needs no model picked by hand, so its choice gives no reason and the composer says nothing (decision [0723](decisions/0723-the-default-and-the-chats-last-model-outside-the-list-are-passed-over.md)).

The test tenant's one model answers its turns off Auto whatever is picked, as it always has; its picker shows it greyed when the list leaves it out, and a message naming it is refused like any other.

## The model choice

`GET /api/v1/chats/{id}/model` (`ModelChoice`), and the `model` of a chat's reply:

| Field | Since P14-05 |
| --- | --- |
| `models` | Every model a turn could be sent to, allowed or not, so the picker can grey the rest. |
| `allowed` | The names of `models` the person may pick by hand. |
| `may_switch_off` | `router.may_switch_off`, or true while there is no ladder. |
| `auto` | The chat is on Auto, or Auto is required, and the person has a ladder. |
| `model`, `source`, `reason` | Chosen among the allowed models only. |

## For other lanes

    p, err := runner.PolicyFor(ctx, store)   // turn.Policy
    p.Allows(name)                           // may pick it by hand
    p.AutoRequired()                         // may not leave Auto, and Auto is offered
    if refused := p.Refusal(name); refused != nil { /* refused.Code, refused.Message */ }
    turn.ParseAllowed(value)                 // router.allowed_models as names

`turn.KeyMaySwitchOff`, `turn.KeyAllowedModels`, `turn.CodeModelNotAllowed` and `turn.CodeAutoRequired` name the settings and the codes. Unattended work (P14-06) gets the turn-start check through `Runner.Send` with no work of its own.

## The screens

Provisional on the owner's answers on admin and views (A1 to A5) and on the chat mockups (P1-04): there is no mockup of a greyed model or of a chat held on Auto.

- **The picker**, on a computer, and **the model sheet** on a phone, which share one menu body (`ModelPicker.tsx`): a model the person may not pick by hand is greyed and cannot be chosen, with the line "Your admin has not allowed this model to be picked by hand." When Auto is required, the model rows, the switch off Auto, are hidden, and one line says "Your admin keeps your chats on Auto, so Keystone picks the model for each message."
- **The new chat screen**, which has no chat to ask, reads `router.allowed_models` and `router.may_switch_off` from the settings route as it reads the ladder, and works the same choice out (`newChatPolicy`).
- **The composer's warning** "There is no model to answer in this chat" is not shown for a chat on Auto (`Chat.tsx`): the admin may allow no model by hand and Auto still answers.
- **A pick the server would refuse** is let go when the model choice is read (`modelPicks.ts`), so it is never sent. If one is sent all the same (the admin changed the policy while the page was open), the refusal's sentence is the turn's problem under the composer (`policyProblem`), and the picker reads the choice again when the failed turn ends.

The words are in `web/app/src/policyWords.ts`, a module with no imports, tested by `web/app/test/policyWords.test.ts` with Node's own test runner (decision 0501).

## Decisions

- [0720](decisions/0720-auto-is-required-only-while-it-is-offered.md): Auto is required only while it is offered.
- [0721](decisions/0721-the-policy-is-checked-when-a-message-is-sent-and-when-its-turn-starts.md): the policy is checked when a message is sent and when its turn starts, for every turn.
- [0722](decisions/0722-a-chat-is-on-auto-whenever-auto-is-required.md): a chat is on Auto whenever Auto is required.
- [0723](decisions/0723-the-default-and-the-chats-last-model-outside-the-list-are-passed-over.md): the default and the chat's last model outside the list are passed over.
- [0724](decisions/0724-a-stronger-model-and-the-ladder-are-outside-the-allowed-list.md): a stronger model and the ladder are outside the allowed list.
- [0725](decisions/0725-the-picker-greys-a-model-and-hides-the-rows-when-auto-is-required.md): the picker greys a model it may not pick and hides the rows when Auto is required.

## Fixed rules

The policy narrows what a person may pick; it never widens a route. The privacy filter, the capability filter and the chain decide as before ([routing.md](routing.md)), and `internal/turn` is not a guard of a fixed rule.

## Checked by

`go test ./internal/turn` (`policy_test.go`):

- The two settings have the spec's type, default and scope; the list reads as names; `Refusal` and `AutoRequired` in a table.
- A person who may not pick a model: the choice lists every model and allows two; the chat's last model and `chats.default_model` outside the list are passed over, for an old chat and a new one; a message, and a retry, naming a model outside the list are answered 403 `model_not_allowed` with the model's label, and no turn is queued; an allowed model and a message naming none go on; a turn sent by `Runner.Send` naming the model fails at its start with `model_not_allowed`; with nothing allowed the choice says why.
- A person who may not leave Auto: a new chat is stored on Auto though `router.auto_default` is off, and an old chat off Auto is offered as on Auto; a message naming a model, allowed or not, is answered 403 `auto_required`, and nothing is queued; the old chat's next message is routed and puts it on Auto; Try a stronger model reaches a step the list leaves out; switched back on, a model outside the list is `model_not_allowed`; with no ladder nothing is required.

`go test ./tools/tenant`: `tenant setting set` stores both as the admin.

`npm test` in `web/app` (`test/policyWords.test.ts`): the greyed and hidden states, the new chat's policy, and the refusals' words.
