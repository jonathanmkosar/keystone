# Spec: the model router, and admin and views

Written from `docs/briefs/model-router.md` (clear) and `docs/briefs/admin-and-views.md`. The second brief's round of questions is not answered yet, so Part 2 takes the recommended answers. Each is marked **Assumed**, is listed in `STATE.md` under "Waiting on the owner", and the work that rests on it is `provisional`.

The items are P14-01 to P14-08 and P15-01 to P15-05 in `docs/plan/items/`. When this spec and the code disagree, this spec wins until a decision record changes it. The four fixed rules in `PLAN.md` hold for everything here, and nothing here loosens a check on them. Every value someone might change is a setting; the tables below name each one, its default and its scope.

## Part 1. The model router

### What it does

A chat on **Auto** has each request go through the router, which chooses the model and the effort:

1. **Rules**, which cost nothing, narrow the ladder to the steps this request may use.
2. **A verdict**: a decision model picks one of those steps and an effort, with a probability.
3. The chosen step's model and the effort go to `routing.Plan` as the ask. The privacy filter, the capability filter and the fallback chain work as they do today (`docs/architecture/routing.md`).

The router only ever narrows or orders what `routing.Plan` allows. It never adds a model to a route that the plan would withhold.

### Terms

- **Auto**: the picker's choice that lets the router decide. A chat is on Auto or on a named model.
- **Ladder**: the admin's ordered list of models for a person, smallest first.
- **Step**: one model on the ladder.
- **Floor**: the lowest step a chat may use. It starts at the bottom and only rises.
- **Verdict**: the router's answer for one request: step, effort, probability and a one-line reason.

### The ladder

`router.ladder` is a list of registry names, smallest first, separated by commas. A name may carry a size limit, as `name<=100000`: that step is skipped when the call's estimated size is over the limit. The owner's ladder, as an example in docs only:

    local-chat, small-hosted<=100000, mid-hosted, large-hosted

A name that is not a usable model in the person's registry is skipped. An empty ladder means Auto is not offered and every chat uses the model picked, as today.

### The rules, in order

1. **Held chat.** A chat held for sensitive content (decision 0211) may use only steps whose model may read sensitive content (`models.Model.MayReadSensitive`). If none is left, the turn fails with the code `sensitive`, as today.
2. **Needs.** An image asks for `vision` (decision 0382), offered tools ask for `tools`, a file asks for `files`. A step whose model lacks one is skipped.
3. **Size.** A step with a size limit is skipped when the call's estimate is over it. Every step must also fit the call in its window (the budget's check).
4. **Floor.** No step below the chat's floor.
5. **Local machine down.** When the decision call or the last call to a local endpoint failed with `unavailable` within `router.local_down_for`, local steps are skipped for this request and the reply carries the notice `local_unavailable`. A held chat with only local steps fails with the code `local_unavailable` and says to try again when the machine is back. It never moves to a hosted model.

What is left are the **candidates**. With one candidate there is no verdict call.

### The verdict

The decision model is the registry entry named by `router.decision_model`, which has the capability `decision` (below). It is shown:

- the newest message's text, cut to `router.verdict_max_chars`;
- the call's facts: estimated tokens, whether it holds images or files, how many tools are offered, the floor step's label, and how many turns the chat has.

It is never shown the rest of the chat.

It is asked two questions:

- `step`: a choice among the candidates. Each option's description is the step's entry in `router.step_notes` if there is one, otherwise "<label>: step n of m, smallest first".
- `effort`: a choice of `low`, `medium` or `high`.

Then:

- If the chosen step's probability is under `router.min_confidence`, the request moves one candidate up.
- With no answer within `router.verdict_timeout`, or any error, the rules alone decide: the lowest candidate, at `router.default_effort`.
- With no decision model named, or one that cannot be opened, the rules alone decide.
- The reason is one line written by Keystone, never by a model: "Local model, effort low, decision 0.91", "Moved up: decision unsure (0.48)", "Rules only: local machine not answering".

A held chat's verdict goes only to a decision model that may read sensitive content. Otherwise the rules alone decide. This is asked of `routing.Plan` with a new kind, `decision`, so the existing guard covers it.

### Effort on the seam

`seam.Request` gains `Effort`: empty, `low`, `medium`, `high`, `xhigh` or `max`. Empty leaves things as they are today. When both `Effort` and `ThinkingTokens` are set, `Effort` wins. `ThinkingTokens` alone works as decision 0111 says.

| Adapter | What `Effort` becomes |
| --- | --- |
| Anthropic Messages | `output_config.effort` for provider models matching `anthropic.effort_models`. For other models with `thinking`, a thinking budget from `anthropic.effort_budget_low`, `_medium` and `_high`. Otherwise nothing is sent. |
| Responses | `reasoning.effort`. `xhigh` and `max` are sent as `high`. Only for entries with `thinking`. |
| Chat Completions | `reasoning_effort`, only for entries with `thinking` and when `chat_completions.send_reasoning_effort` is true. Otherwise nothing is sent. |

The router never asks for more than `router.max_effort`.

### Decision calls on the seam

- A new capability, `decision`. An entry with it takes decision calls and nothing else, as an embedding entry does.
- A new seam interface: `Decide(ctx, DecisionRequest) (DecisionResult, error)`.
  - `DecisionRequest` holds the model, the state (text) and the questions. Each question has an id, instructions, and a type, `choice` or `yes_no`, with its options as id and description.
  - `DecisionResult` holds each question's answer with a probability for every option.
- A new adapter, `systemone`: `POST {endpoint}/v1/systemone`, the decision API Ollama serves from version 0.35.1 for decision models. Before sending, it refuses a request over `systemone.max_request_bytes` or with more than `systemone.max_questions` questions. Its timeout is `systemone.timeout`.
- The fake provider answers decision calls from recorded scripts, as it answers turns (decision 0011).
- Every decision call has a started and an end entry in the audit log and a usage record of kind `decision`. A local model costs nothing.

### Auto in the turn

- A migration gives each chat `auto` (yes or no) and `auto_floor` (a registry name). A new chat takes `router.auto_default` and the ladder's bottom.
- A turn of a chat on Auto, with a ladder, asks the router. The chosen model is the ask's model, and the effort is set. Everything after that is the existing route and chain.
- When the reply ends well, the floor rises to the step that was asked for, if that is higher. A fallback that answered does not lower it.
- The turn's record keeps `auto`, the step, the effort, the reason and the verdict's id.
- A new table, `router_verdicts`: id, turn id, request id, the decision model, the facts it was shown (as JSON, without the message text, which is in the chat), the step answered, its probability, the effort, `moved_up`, `timed_out`, `rules_only` with its reason, latency in milliseconds, `stronger_asked`, and when.
- **Try a stronger model.** `POST /api/v1/chats/{id}/turns/{turn}/stronger` writes that turn again, on a new branch of the message tree (decision 0020), one step above the step that answered. It raises the floor and sets `stronger_asked` on the verdict. It is refused on the top step, or when no step higher up may take the call.
- `GET /api/v1/chats/{id}/model` (`ModelChoice`) gains `auto`, `floor`, `ladder` (names and labels), `may_switch_off`, `allowed` and `held`. `held` replaces the browser's own record of held chats (decision 0241), which can then be dropped.
- A turn's stream starts with a `route` event: `auto`, the model, the step, the effort, the reason, and any notice.

### The admin's policy for each person

| Key | Type | Default | Scope | Meaning |
| --- | --- | --- | --- | --- |
| `router.ladder` | string | empty | tenant-admin | The ladder, as above. |
| `router.decision_model` | string | empty | tenant-admin | The registry name of the decision model. |
| `router.step_notes` | string | empty | tenant-admin | Optional descriptions of steps for the verdict, as `name=words`, separated by semicolons. |
| `router.auto_default` | bool | true | tenant-admin | A new chat starts on Auto. |
| `router.may_switch_off` | bool | true | tenant-admin | The person may take a chat off Auto. |
| `router.allowed_models` | string | empty | tenant-admin | Registry names the person may pick by hand, separated by commas. Empty means every model in their registry. Auto uses the ladder whatever this says. |

The server enforces them, and the screens only reflect them:

- A turn that names a model the person may not pick is refused: 403, code `model_not_allowed`.
- Taking a chat off Auto when the person may not is refused: 403, code `auto_required`.
- A `chats.default_model` outside the allowed list is ignored.

Picking a named model in a chat on Auto takes the chat off Auto, when that is allowed.

### The router's own settings

| Key | Type | Default | Scope |
| --- | --- | --- | --- |
| `router.min_confidence` | float | 0.6 | install |
| `router.verdict_timeout` | duration | 2s | install |
| `router.verdict_max_chars` | int | 8000 | install |
| `router.default_effort` | string | medium | install |
| `router.max_effort` | string | high | install |
| `router.local_down_for` | duration | 2m | install |
| `systemone.timeout` | duration | 5s | install |
| `systemone.max_request_bytes` | int | 65536 | install |
| `systemone.max_questions` | int | 64 | install |
| `anthropic.effort_models` | string | (patterns for models that take `output_config.effort`) | install |
| `anthropic.effort_budget_low`, `_medium`, `_high` | int | 2048, 8192, 24576 | install |
| `chat_completions.send_reasoning_effort` | bool | false | install |

### Unattended work

Sub-agents, background tasks and scheduled runs start on Auto with their own floor at the bottom, unless the call that started them names a model. A held parent's sub-agent is held, as decision 0483 already says. At the cap, unattended work falls back to a local model or stops, as decision 0392 says.

### Screens

- **The picker**: Auto first, and the default. A model the person may not pick is greyed, with the reason. The switch that takes a chat off Auto is hidden when the person may not use it.
- **The reply's footnote**: the model, the effort and the reason. The notice when the local machine is not answering.
- **The inspector**: the verdict record.
- **The reply's menu**: "Try a stronger model".
- **Usage**: an Auto card: this month on Auto against the same tokens priced at the ladder's top step, and how often a stronger model was asked for.

### Proof

- Recorded turns in the test tenant reach each step of the ladder, with the fake provider giving the verdicts.
- A held chat on Auto, given a verdict that names a hosted step, reaches no hosted model. This joins the `sensitive content` check in `verify`.
- A verdict under the confidence level moves up one step. A timed-out verdict uses the rules alone. A chat's floor never falls.
- The local machine not answering: a chat on Auto is answered by the next step with the notice; a held chat fails with `local_unavailable`.
- A person who may not pick a model, or may not leave Auto, is refused by the server.
- Effort reaches each adapter's request in the shape in the table above, on recorded exchanges.
- Every decision call has its audit entries and its usage record.
- The live check (P14-08): the owner's decision model and chat model on his local machine, and his hosted models.

## Part 2. Admin and views

### Assumed answers (owner to confirm)

- **A1.** An admin is a person with a mark, not a second account. The first admin is marked from the command line only. The admin area asks for the password or a passkey again, and stays open for `admin.step_up_for`.
- **A2.** An admin sees another person's numbers and settings, never their chats, titles, messages, files, memory, traces or error words.
- **A3.** A view that is off is off: its routes and its tools are refused for that person, not only hidden from the menu.
- **A4.** Views are given by named view sets, with per-person additions and removals on top.
- **A5.** Besides the owner, a few people use the install. The shipped "simple" set is chat, projects, artifacts and the settings needed to use them.

### The admin mark

- Each tenant's entry in `keystone.json` gains `admin`, yes or no. Only `go run ./tools/tenant admin <id> on|off` changes it. No route can. The command warns before it removes the last admin.
- **Step-up.** `POST /api/v1/admin/step-up` with the password or a passkey. The session row gains `admin_until`, now plus `admin.step_up_for`. Every admin route refuses a session without it: 401, code `step_up`.
- The admin routes are under `/api/v1/admin/`, behind a new door in the API's list (decision 0040). The door checks the mark and the step-up.
- **Audit.** Every admin route call is written to the admin's own audit log with the actor `admin`. Every read or change of another person's numbers or settings is also written to that person's log, with the actor `admin:<the admin's tenant id>`, and a change records the old and the new value.

### Reading another person

- A new package, `internal/adminview`, is the only code the admin routes use to read another tenant. It returns numbers and settings, nothing else.
- A new standing check, `admin isolation`:
  - it proves `internal/adminview` does not import the chat, turn, context, knowledge, uploads, artifacts, projects, recap, inspector or share packages, itself or through another;
  - it runs a drill in the manner of the isolation drill (decision 0491): marker words placed in a second person's chats, titles, files, memory and traces appear in no reply of any admin route.

### People and the install

- `GET /api/v1/admin/people`, for each person: id, name, admin mark, when added, last sign-in, spend this month against the cap, the size of their database, files and artifacts, jobs failed and stuck in the health window, the number of schedules, model health as counts by state, and which watch levels are crossed. No titles, no error words.
- `GET /api/v1/admin/install`: the disk, the number of people, the version, the install settings that were changed, and the sampler's state.
- `POST /api/v1/admin/people` adds a person by name and returns a one-time setup code, good for `admin.setup_code_for`. The person opens `/setup`, enters the code, and sets their own password or passkey.
- `POST /api/v1/admin/people/{id}/setup-code` gives a new setup code. The admin never sees or sets anyone's password.
- Removing a person stays on the command line.
- Screens: **Admin → People** and **Admin → Install**, from `design/screens/Admin.html` and `Operations.html` (approved November 27, 2026).

### Settings from the browser

- Install settings are stored in `keystone.json` under `settings`, and read at start and on each change. The registry gains an optional field, `restart`, for a setting that takes effect only after a restart. The screen says so.
- `GET` and `PUT /api/v1/admin/people/{id}/settings`: that person's `tenant-admin` settings, through `tenantsettings.Set` with the role `admin`. The router's policy is set here.
- `GET` and `PUT /api/v1/admin/install/settings`: the `install` settings.
- The fixed rules are refused for every role, as now.

### Views

- **The view list**, data in `internal/views`: for each view an id, a label, a parent (for submenus), its screen paths, its API route prefixes, its tool names, and whether it is always on. For example: chat (always), projects, artifacts, tasks, scheduled, operations, usage, memory, connectors, settings.general (always), settings.models, settings.approvals, settings.connectors. Admin is shown only to admins and is in no set.
- **View sets** are install data in `keystone.json` under `view_sets`, a name and its views. Shipped: `full` (every view) and `simple` (chat, projects, artifacts, settings.general). `GET` and `PUT /api/v1/admin/view-sets`.
- **For each person**, `tenant-admin` settings: `views.set` (default `full`), `views.add` and `views.remove` (view ids, separated by commas).
- **The person's own**, `tenant` settings: `views.hidden` and `views.order`. These only hide or reorder views the person has.
- **Enforcement.**
  - The API door checks each route against the person's views. A route of a view they lack is refused: 403, code `view_off`.
  - The tool registry withholds the tools of a view they lack.
  - A person without `scheduled` keeps their schedules, held, until the view returns.
- `GET /api/v1/me/views`: the person's views in their order, with the hidden ones marked. The sidebar and every submenu are drawn from it.
- Screens:
  - Settings → General → Menus, for hiding and ordering.
  - Admin → People → a person → Views.
  - Admin → View sets.

### Admin settings

| Key | Type | Default | Scope |
| --- | --- | --- | --- |
| `admin.step_up_for` | duration | 15m | install |
| `admin.setup_code_for` | duration | 24h | install |
| `views.set` | string | full | tenant-admin |
| `views.add` | string | empty | tenant-admin |
| `views.remove` | string | empty | tenant-admin |
| `views.hidden` | string | empty | tenant |
| `views.order` | string | empty | tenant |

### Proof

- A person without the mark is refused on every admin route. An admin without a fresh step-up is refused.
- The `admin isolation` check passes.
- Each admin read and change is in both audit logs.
- An admin changes a person's `tenant-admin` setting, and the person's next turn uses it. A person cannot change it.
- A person on `simple` is refused the routes and tools of a view outside it. Their sidebar shows only their views. A held schedule does not run.
- A person hides and reorders their own views. They cannot add one they were not given.
- A setup code works once, and not after it expires.

## Out of scope

- A trained router, fine-tuning the decision model, a hosted decision model, and a door for other apps to use the router.
- Replacing a reply part of the way through (decision 0160 stands).
- An admin reading another person's content. Teams or shared spaces.
- Editing another person's model registry from the admin screens. It stays an open question from the models lane.
