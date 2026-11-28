# Spec: the model router, and admin and views

Written from `docs/briefs/model-router.md` (clear) and `docs/briefs/admin-and-views.md`. The owner answered A1 and A2 on November 27, 2026, and chose encryption at rest. A3 to A5 are still the recommended answers, marked **assumed**, listed in `STATE.md` under "Waiting on the owner"; the work that rests on them is `provisional`.

The items are P14-01 to P14-08 and P15-01 to P15-06 in `docs/plan/items/`. When this spec and the code disagree, this spec wins until a decision record changes it. The four fixed rules in `PLAN.md` hold for everything here, and nothing here loosens a check on them. Every value someone might change is a setting; the tables below name each one, its default and its scope.

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
- **Verdict**: the router's answer for one request: step, effort, probability and a one-line reason.

### The ladder

`router.ladder` is a list of registry names, smallest first, separated by commas. A name may carry a size limit, as `name<=100000`: that step is skipped when the call's estimated size is over the limit. The owner's ladder, as an example in docs only:

    local-chat, small-hosted<=100000, mid-hosted, large-hosted

A name that is not a usable model in the person's registry is skipped. An empty ladder means Auto is not offered and every chat uses the model picked, as today.

### The rules, in order

1. **Held chat.** A chat held for sensitive content (decision 0211) may use only steps whose model may read sensitive content (`models.Model.MayReadSensitive`). If none is left, the turn fails with the code `sensitive`, as today.
2. **Needs.** An image asks for `vision` (decision 0382), offered tools ask for `tools`, a file asks for `files`. A step whose model lacks one is skipped.
3. **Size.** A step with a size limit is skipped when the call's estimate is over it. Every step must also fit the call in its window (the budget's check).
4. **Local machine down.** When the decision call or the last call to a local endpoint failed with `unavailable` within `router.local_down_for`, local steps are skipped for this request and the reply carries the notice `local_unavailable`. A held chat with only local steps fails with the code `local_unavailable` and says to try again when the machine is back. It never moves to a hosted model.

What is left are the **candidates**. With one candidate there is no verdict call.

### The verdict

The decision model is the registry entry named by `router.decision_model`, which has the capability `decision` (below). It is shown:

- the newest message's text, cut to `router.verdict_max_chars`;
- the call's facts: estimated tokens, whether it holds images or files, how many tools are offered, the step the previous message used, and how many turns the chat has.

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

- A migration gives each chat `auto` (yes or no). A new chat takes `router.auto_default`.
- **Every message is routed afresh, before its call.** The router decides from that message and the call's facts. Nothing from earlier messages holds a chat on a step. The chosen model is the ask's model, and the effort is set. Everything after that is the existing route and chain.
- **The choice holds for the whole turn.** Every tool step of that message's tool loop uses the same model and effort, since a thinking block goes back only to the model that wrote it. The next message is routed again.
- **Switching costs the cache.** A message routed to another model than the one before loses that provider's prompt cache for its call. The reason line says so ("switched from <label>"), and the usage records already show cache reads and writes, so the cost is visible on Usage.
- The turn's record keeps `auto`, the step, the effort, the reason and the verdict's id.
- A new table, `router_verdicts`: id, turn id, request id, the decision model, the facts it was shown (as JSON, without the message text, which is in the chat), the step answered, its probability, the effort, `moved_up`, `timed_out`, `rules_only` with its reason, latency in milliseconds, `stronger_asked`, and when.
- **Try a stronger model.** `POST /api/v1/chats/{id}/turns/{turn}/stronger` writes that turn again, on a new branch of the message tree (decision 0020), one step above the step that answered, and sets `stronger_asked` on the verdict. The next message is routed afresh. It is refused on the top step, or when no step higher up may take the call.
- `GET /api/v1/chats/{id}/model` (`ModelChoice`) gains `auto`, `last_step`, `ladder` (names and labels), `may_switch_off`, `allowed` and `held`. `held` replaces the browser's own record of held chats (decision 0241), which can then be dropped.
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

Sub-agents, background tasks and scheduled runs start on Auto, each of their turns routed afresh, unless the call that started them names a model. A held parent's sub-agent is held, as decision 0483 already says. At the cap, unattended work falls back to a local model or stops, as decision 0392 says.

### Screens

- **The picker**: Auto first, and the default. A model the person may not pick is greyed, with the reason. The switch that takes a chat off Auto is hidden when the person may not use it.
- **The reply's footnote**: the model, the effort and the reason. The notice when the local machine is not answering.
- **The inspector**: the verdict record.
- **The reply's menu**: "Try a stronger model".
- **Usage**: an Auto card: this month on Auto against the same tokens priced at the ladder's top step, and how often a stronger model was asked for.

### Proof

- Recorded turns in the test tenant reach each step of the ladder, with the fake provider giving the verdicts.
- A held chat on Auto, given a verdict that names a hosted step, reaches no hosted model. This joins the `sensitive content` check in `verify`.
- A verdict under the confidence level moves up one step. A timed-out verdict uses the rules alone.
- Each message is routed afresh: a hard message followed by an easy one goes back down, and every tool step of one turn stays on its model.
- The local machine not answering: a chat on Auto is answered by the next step with the notice; a held chat fails with `local_unavailable`.
- A person who may not pick a model, or may not leave Auto, is refused by the server.
- Effort reaches each adapter's request in the shape in the table above, on recorded exchanges.
- Every decision call has its audit entries and its usage record.
- The live check (P14-08): the owner's decision model and chat model on his local machine, and his hosted models.

## Part 2. Admin and views

### The owner's answers (November 27, 2026)

- **A1, answered.** An admin is a separate account, not a person with a mark. Signing in as an admin shows only admin controls: no chats, no files, no models of its own.
- **A2, answered.** An admin sees another person's numbers and settings, never their data or their logs: no chats, titles, messages, files, artifacts, memory, traces, error words or audit log entries.
- **Encryption at rest, answered.** Every person's data is encrypted on disk with their own key, held by the operating system (server-held keys).
- **Operations, answered.** A person's own jobs, spend and storage stay in that person's Settings. Anything install-wide is in the admin account only.
- **Disable, asked for.** An admin can disable a person, and enable them again.

Still assumed, from the recommendations, and listed in `STATE.md`:

- **A3.** A view that is off is off: its routes and its tools are refused for that person, not only hidden.
- **A4.** Views are given by named view sets, with per-person additions and removals on top.
- **A5.** Besides the owner, a few people use the install. The shipped "simple" set is chat, projects, artifacts and general settings.

### The admin account

- Admin accounts are kept apart from people, in their own database, `data/admin/admin.db`: the accounts, their passwords and passkeys, their sessions, and the admin audit log. An admin account has no tenant database, so it has nothing of a person's to show.
- An admin session's token has its own form, `ksa_` and 32 random bytes as hex. A person's token is refused on every admin route, and an admin's token on every person's route. The two kinds are told apart by the door before anything is read.
- Admin accounts are made only from the command line: `go run ./tools/admin add "Name"`, then `go run ./tools/admin password <id>`. No route makes or removes one. The command warns before it removes the last one.
- Sign-in is at `/admin/sign-in`, with a password or a passkey, under the same rules as a person's (wrong tries, waits, hashing: `docs/architecture/signin.md`). A session lasts `admin.session_for` and ends after `admin.idle_for` without use.
- The owner has two sign-ins: his own person, and his admin account.
- **Replaces the first pass.** P15-01 built the admin as a mark on a person with a step-up. The mark, `tools/tenant admin` and the step-up are removed. The door, `internal/adminview` and the two audit logs stay.

### What an admin never sees

- `internal/adminview` is the only code the admin routes use to read a person. It returns numbers and settings, nothing else.
- Every admin read or change of a person is written to the admin audit log, and to that person's audit log with the actor `admin:<admin id>`. A change records the old and the new value. The person can see these entries in their own Settings. The admin cannot read the person's log.
- The server's own log carries no person's content.
- The standing check `admin isolation`:
  - `internal/adminview` imports none of the chat, turn, context, knowledge, uploads, artifacts, projects, recap, inspector, share or audit-reading packages, itself or through another;
  - marker words placed in a second person's chats, titles, files, memory, traces and audit log appear in no reply of any admin route, and in no line of the server's log.

### Encryption at rest

Server-held keys: each person's data key, wrapped by the install master key that the operating system holds (decision 0006). The same key already seals the person's secrets.

- **What is encrypted:** the person's database (chats, messages, memory, vectors, usage records, the audit log, snapshots and everything else in it), their uploads, files, artifacts and project files. Nothing of a person's is left in plain text in their folder.
- **The database** is encrypted page by page, with its write-ahead log and any temporary files. The driver Keystone uses (decision 0003) has no encryption of its own, as far as the planner knows. The item chooses between an encrypting layer under the driver and another driver, measures it with the load test, and records the choice in a decision record.
- **Files** are sealed with AES-256-GCM, in chunks for large files, bound to the tenant id. A file kept by its hash is named by a keyed hash, so equal names do not show equal files across people.
- **An existing install** is encrypted in place at the first start of the new version, one person at a time, after a backup of each, and refuses to start a person whose folder is half done.
- **Backups** stay sealed under their passphrase (decision 0260).
- **What it protects:** a copied data folder, a stolen disk and a backup file read without its passphrase. The admin account has no route to content at all. It does not protect against someone who controls the machine and runs code as the account Keystone runs as. The owner chose this, knowing it.

### Disabling a person

- `POST /api/v1/admin/people/{id}/disable` and `.../enable`.
- A disabled person:
  - cannot sign in, and is told the account is disabled and to ask the admin;
  - has every session ended at once;
  - has their schedules held, and their background tasks and sub-agents paused, through the pause switches that exist (decisions 0343, 0390);
  - has their share links answer as revoked while disabled.
- Their data is kept, and stays encrypted. Enabling them restores sign-in, the schedules and the links. Runs missed while disabled are not caught up.
- Removing a person stays on the command line.

### People, the install and monitoring

- **People:** `GET /api/v1/admin/people`, for each person: id, name, enabled, when added, last sign-in, spend this month against the cap, the size of their database, files and artifacts, jobs failed and stuck, the number of schedules, model health as counts by state, and the watch levels crossed.
- **One person:** `GET /api/v1/admin/people/{id}/usage?from=&to=`: spend and calls by day and by model, failures counted by kind (never their words), and storage over time.
- **The whole install:** `GET /api/v1/admin/install`: spend and calls for all people this month, by model and by day; failures by kind; the disk; the number of people; the version; the install settings that were changed; and the sampler's state.
- **Adding a person:** `POST /api/v1/admin/people` with a name returns a one-time setup code, good for `admin.setup_code_for`. The person opens `/setup`, enters it, and sets their own password or passkey. `POST /api/v1/admin/people/{id}/setup-code` gives a new one. The admin never sees or sets anyone's password.
- **Operations moves.** A person's Operations page keeps their own jobs, schedules, spend, storage and model health, under Settings. The disk and everything install-wide leave it, and are shown only in the admin account. This changes decision 0460.
- Screens: **People**, **A person**, **Install**, from `design/screens/Admin.html` and `Operations.html` (approved November 27, 2026).

### Settings for people

- `GET` and `PUT /api/v1/admin/people/{id}/settings`: that person's `tenant-admin` settings, through `tenantsettings.Set` with the role `admin`. The router's policy is set here.
- `GET` and `PUT /api/v1/admin/install/settings`: the `install` settings, stored in `keystone.json` under `settings` and read at start and on each change. The registry gains an optional field, `restart`, for a setting that takes effect only after a restart.
- **Assumed:** the admin also sets install-wide defaults for the settings that are each person's own (`tenant` scope). A new person starts with them. A person's own choice stays theirs, and the admin cannot change it.
- The fixed rules are refused for every role, as now.

### Views

- **The view list**, data in `internal/views`: for each view an id, a label, a parent (for submenus), its screen paths, its API route prefixes, its tool names, and whether it is always on. For example: chat (always), projects, artifacts, tasks, scheduled, operations, usage, memory, connectors, settings.general (always), settings.models, settings.approvals, settings.connectors.
- **View sets** are install data in `keystone.json` under `view_sets`. Shipped: `full` (every view) and `simple` (chat, projects, artifacts, settings.general). `GET` and `PUT /api/v1/admin/view-sets`.
- **For each person**, `tenant-admin` settings: `views.set` (default `full`), `views.add` and `views.remove`.
- **The person's own**, `tenant` settings: `views.hidden` and `views.order`. These only hide or reorder views the person has.
- **Enforcement.** The door refuses a route of a view the person lacks: 403, code `view_off`. The tool registry withholds the view's tools. A person without `scheduled` keeps their schedules, held, until the view returns.
- `GET /api/v1/me/views` draws the sidebar and every submenu.
- Screens: Settings → General → Menus; the admin's A person → Views; the admin's View sets.

### Admin settings

| Key | Type | Default | Scope |
| --- | --- | --- | --- |
| `admin.session_for` | duration | 8h | install |
| `admin.idle_for` | duration | 30m | install |
| `admin.setup_code_for` | duration | 24h | install |
| `views.set` | string | full | tenant-admin |
| `views.add` | string | empty | tenant-admin |
| `views.remove` | string | empty | tenant-admin |
| `views.hidden` | string | empty | tenant |
| `views.order` | string | empty | tenant |

### Proof

- A person's token is refused on every admin route, and an admin's token on every person's route. An admin account has no chat, file or model route at all.
- The `admin isolation` check passes, including the server's log.
- Each admin read and change is in both audit logs, and the person sees it in their Settings.
- Every file in a person's folder, read straight from disk, holds no marker word and no plain database header. A folder copied to another tenant, or opened without the master key, does not open. The load test still passes with encryption on.
- An existing plain install is encrypted in place, and a start interrupted halfway refuses the half-done person and resumes.
- A disabled person cannot sign in, their sessions end, their schedules do not run, and their share links answer as revoked. Enabling them restores all of it.
- An admin changes a person's `tenant-admin` setting, and the person's next turn uses it. The admin cannot change a person's own setting.
- A person on `simple` is refused the routes and tools outside it, and sees only their views.
- A setup code works once, and not after it expires.

## Out of scope

- A trained router, fine-tuning the decision model, a hosted decision model, and a door for other apps to use the router.
- Replacing a reply part of the way through (decision 0160 stands).
- An admin reading another person's content. Teams or shared spaces.
- Editing another person's model registry from the admin screens. It stays an open question from the models lane.
