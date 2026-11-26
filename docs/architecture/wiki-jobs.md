# Wiki upkeep jobs

Folders: `internal/wikijobs`, its guard `internal/wikijobs/scope`, and the section of the Scheduled screen in `web/app/src/WikiJobs.tsx`

The memory pass only adds sources. These jobs turn them into cited pages. The plan's words: the Waymarks agent-kit jobs (compile, lint, upkeep, fix issues) run as Keystone scheduled agents, each with its own token, and turn those sources into cited pages; the built-in store gets a simple compile job.

Built in P8-06. Decisions [0440](decisions/0440-a-wiki-job-is-a-schedule-it-owns-made-from-settings.md) to [0444](decisions/0444-the-proof-against-a-real-waymarks-runs-in-go-test-and-skips-without-it.md) say why it is built this way. The runbook for the owner is [docs/runbooks/wiki-jobs.md](../runbooks/wiki-jobs.md).

## The jobs

They are Waymarks' own agent kit (`agent-kit/` in the Waymarks repository), followed as written: the same four jobs, the instructions of each job file, the read tools of Waymarks' MCP server by their own names, and the kit's grant for each, with one principal and one token per job, so Waymarks' audit shows each job as itself and the owner can suspend one without the rest.

| Job | Store | Its token (a secret in the person's vault) | The kit's grant | What it is offered | What its answer may write |
| --- | --- | --- | --- | --- | --- |
| `compile` | Waymarks | `knowledge.waymarks.compile` | `*:read,search,write_pages,delete_pages` | `whoami`, `read_schema`, `read_catalog`, `list_pages`, `read_page`, `page_links`, `list_sources`, `read_source`, `search` | pages written and deleted |
| `lint` | Waymarks | `knowledge.waymarks.lint` | `*:read,search,write_pages,delete_pages,raise_issues` | the same, `lint_report`, `list_issues`, `read_issue` | pages written and deleted, issues raised and noted |
| `upkeep` | Waymarks | `knowledge.waymarks.upkeep` | `*:read,search,write_pages,delete_pages,raise_issues,close_issues` | as lint | all of the above, and issues closed |
| `fix_issues` | Waymarks | `knowledge.waymarks.fix_issues` | `*:read,search,write_pages,close_issues` | the page tools, `list_issues`, `read_issue` | pages written, issues noted and closed |
| `builtin_compile` | the built-in store | none | none: the store is the person's own database | `read_catalog`, `list_uncompiled_sources`, `read_source`, `read_page` | pages written |

The kit's intake job is not here: the memory pass is Keystone's intake, under `knowledge.waymarks.intake` ([memory-pass.md](memory-pass.md)). The kit says the upkeep job is the one to schedule, since its daily round does what the other three do; the runbook says so.

## A run

1. **A job is a schedule it owns** (decision [0440](decisions/0440-a-wiki-job-is-a-schedule-it-owns-made-from-settings.md)). The first time the person switches a job on, Keystone makes a schedule ([schedules.md](schedules.md)) named by the job ("Wiki upkeep: compile pages"), whose prompt is the job's instructions, whose recurrence, model and budget for each run are the job's settings. A row in `wiki_jobs` names it. When the settings change, the schedule is changed once to match; a change made to it on the Scheduled screen stands until they do. While the job is off, or cannot run, its schedule is paused.
2. **The run is the schedule's run**: a background task in a chat of its own ([jobs.md](jobs.md)), with the schedule's budget for each run and the pause switch, as every unattended run has ([unattended-spend.md](unattended-spend.md)). Missed times make one late run, and a run a restart cut short goes on.
3. **In the run's chat**, every model call is offered the job's tools and nothing of the person's own connectors (through the turn's connectors, `wikijobs.Connectors`). Each tool reads, under the job's own token, through Waymarks' API (the same calls Waymarks' MCP server makes), or from the built-in store. Keystone's own tools are offered there as in any chat; one that changes something asks the person, and the web tools are off because the chat is held.
4. **The job ends with one JSON answer**, as its instructions say: the batches it wants (page writes and deletes), the issue changes, and a report. It writes nothing with a tool (decision [0441](decisions/0441-a-wiki-jobs-writes-come-from-its-answer-through-its-own-grant.md)).
5. **When the run's task is done**, Keystone reads the answer, checks all of it against the job's grant (`scope.Check`), and writes it through that grant: each batch as a Waymarks batch under the job's own token (open, writes, close, so it lands as one change the owner can revert), or each page with `builtin.WritePage`. A write Waymarks refuses drops its batch, and nothing after it is written. Each write is two entries in the person's audit log, of the kind `tool`, under the run's request id, as the actor `wiki.<job>`. Then a note in the run's chat says what was written and where Waymarks shows it.
6. A run that was stopped or failed writes nothing. The runner looks every `agents.wiki_poll`, and every second while a run is under way.

A new page may carry a `ref`, and another page of the same batch links to it as `[[new:ref]]` or `[[new:ref|shown text]]`. Keystone writes the pages in order, puts in each id as Waymarks gives it, and writes again in the same batch any page that linked to a page written after it, as the kit's compile job does by hand. Until then such a link is its shown text, so no store is sent a link it does not know.

## The fixed rules

The guard is `internal/wikijobs/scope`. It imports nothing of Keystone's, and `verify`'s `fixed rules` check holds it to that as a guard of rules 2 and 3.

- **Rule 3, sensitive content.** A wiki job reads the person's knowledge store, which may hold sensitive pages, so every call of a run goes to a local or trusted model and to no other (decision [0442](decisions/0442-a-wiki-run-is-held-and-refused-every-hosted-model.md)):
  - a job cannot be switched on, or run now, with a model that is not local or trusted (`scope.MayServe`), and its schedule is paused if its model becomes one;
  - the run's chat is held for sensitive content from its first model call, as recall holds a chat, so every later call of it (the task's rounds, recaps, compaction) is planned for local and trusted models only;
  - and the turn's gate (`wikijobs.Gate`, around the background tasks' budget gate) refuses any call of a wiki run to a hosted model before it is sent, the first call included, so a hosted model in the person's fallback chain is passed over and the run fails if none is left. The refusal is of the kind `unavailable`, so it is never taken for the cap.
  - A page that cites a source from a sensitive space is written only into a sensitive space, in either store (`scope.Check`). Waymarks' kit grants cover normal spaces only; a sensitive space is reached only when the owner names it in a job's grant.
- **Rule 2, approval.** A job's writes go only to the knowledge store, only through its own grant: `scope.Check` refuses the whole answer, and nothing of it is written, when it asks for anything the job's grant does not allow, cites a source the job may not cite (one it cannot read, or one the person withdrew), or writes more than `agents.wiki_max_batches` batches or `agents.wiki_max_new_pages` new pages. Waymarks checks the token's grant again on every call. Anything else a job would do is a tool call in its chat, under the person's approval policy. Why writes that follow outside content are not asked about one by one: decision [0441](decisions/0441-a-wiki-jobs-writes-come-from-its-answer-through-its-own-grant.md), with the owner's question about it.
- **Rule 4, every call recorded.** The run's model calls and tool calls are the turn's, recorded as every chat's are; each write is recorded by the runner under the run's request id. No token is ever in the audit log, a reply, an error or the run's chat.
- **Rule 1, tenant isolation.** Every table is in the person's own database, every token in their own vault, and every route reaches only the signed-in person's jobs.

**No memory pass** reads a wiki run's chat (`turn.Options.SkipMemory`), so a job's report never comes back to the store as a source of its own.

**Withdrawn sources** (decision [0443](decisions/0443-keystones-own-jobs-skip-withdrawn-sources.md)). A source the person asked Keystone to forget ([memory-screens.md](memory-screens.md)) is left out of `list_sources` and `search`, refused by `read_source`, and refused as a citation, so Keystone's own jobs never compile it into a page. This is the answer to the owner's question of decision 0371 for Keystone's jobs; agents outside Keystone are not told.

## The routes

Every path starts with `/api/v1`. `{job}` is a job's name. Each route reaches only the signed-in person's jobs.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /wiki-jobs` | | `JobsReply`: `jobs`, in the table's order. |
| `POST /wiki-jobs/{job}/on` | No body. | `JobState`, after setting `agents.wiki_<job>_on` and putting its schedule in step. |
| `POST /wiki-jobs/{job}/off` | No body. | `JobState`. A run under way goes on. |
| `POST /wiki-jobs/{job}/run` | No body. | `schedules.Run`, 201: a run now, on or off, its schedule made first if it has none. |

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | No such job. |
| `not_set_up` | 409 | Run now for a job that cannot run, and why: the person's store is not the job's, the job's token is not stored, no model is named, or the model is not local or trusted. |
| `run_open` | 409 | Run now while a run of the job is under way. |

`JobState` is the job (`name`, `title`, `store`, `secret`, `principal`, `grant`, `kit`, `tools`), and `on`; `problem`, why it cannot run, for a person; `when` and `says`, its schedule as set and in words; `model`; `budget_usd`; `has_token`, whether its token is in the vault (the token never leaves it); `schedule_id`; `held`, when a switch holds every schedule or every agent; `next`; `last_run`, the schedule's newest run; and `last_written`, the record of the newest ended run: `state` (`applied`, `refused`, `failed`, `nothing`, `interrupted`), the note's words as `summary`, and `written`.

## On the screen

The Scheduled screen has a section, **Wiki upkeep**, under its list (`web/app/src/WikiJobs.tsx` and `wiki-jobs.css`): each job with its title, its store and whether its token is stored, its schedule in words, its state as a pill (Off, On, Cannot run, Held), its last run and what was written, its next run, a switch, and **Run now**. A job that cannot run says why, and names the runbook. Built from the tokens in `design/README.md` after `design/screens/Scheduled.html`; there is no mockup for it, so it is provisional on the agent mockups (P1-05). Each job's schedule is also in the list above it, where its runs and their chats are.

## Tables

In `internal/tenant/migrations/20261125T233100_wiki_jobs.sql`.

| Table | Holds |
| --- | --- |
| `wiki_jobs` | One row for each job that has had a schedule: the schedule it owns, and a digest of the settings it was made from. |
| `wiki_runs` | One row for each run of a job whose task has ended: the run, its chat and task, `applying` while its answer is written (so a restart never writes it twice; a stop meanwhile is marked `interrupted` at the next start, with a note), then what was written and the note's words. |

## In code

    wikijobs.Jobs(), wikijobs.Get(name)               // the five jobs
    wikijobs.New(wikijobs.Options{Install, Log, Config, Schedules})
    runner.Start(turns)                               // after the scheduler; closing turns stops it
    runner.Tick(ctx)                                  // one look at every person, as the goroutine does
    runner.SetOn / RunNow / State                     // what the routes call
    wikijobs.Routes(runner)
    wikijobs.Connectors(pool)                         // turn.Options.Connectors: a job's tools in its runs' chats
    wikijobs.Gate(jobs.Gate)                          // turn.Options.Gate: no hosted model for a wiki run
    wikijobs.SkipMemory                               // turn.Options.SkipMemory
    wikijobs.JobOfChat(ctx, store, chatID)            // the job whose run a chat is
    wikijobs.ParseAnswer(reply), wikijobs.ParseWhen(text)
    scope.MayServe(class), scope.Check(may, limits, plan, store), scope.Resolve(body, ids)

`internal/app` puts it together: `newWiki` in `internal/app/wikijobs.go`, and added lines in `app.go` (the config, the three turn options, the routes, the start in `Handler`).

Existing lines changed elsewhere: `internal/turn/config.go` gained the `SkipMemory` option and its import, and `internal/turn/memory.go` one check in `memoryLater`. New files beside other packages: `internal/schedules/exported.go` (the scheduler's errors by exported names) and `internal/knowledge/builtin/uncited.go` (`Uncited` and `SpaceLabels`).

## Settings

In `settings/agents.json`. For each job, `<job>` its name:

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.wiki_<job>_on` | false | tenant | Whether it runs on its schedule. |
| `agents.wiki_<job>_when` | compile and builtin_compile `daily 02:30`, lint `weekly sun 03:30`, upkeep `daily 04:00`, fix_issues `daily 05:00` | tenant | When it runs, in the person's zone: `hourly 15`, `daily 02:30`, `weekdays 06:00`, `weekly mon,thu 03:30`. |
| `agents.wiki_<job>_model` | empty | tenant | Its model, local or trusted. Empty is `routing.memory_model`'s. |
| `agents.wiki_<job>_budget_usd` | 0.5 | tenant | Its budget for each run, on models that are not local. |
| `agents.wiki_<job>_prompt` | the kit's job, adapted | install | Its instructions. `{max_batches}` and `{max_new_pages}` become the two limits. |

And for all of them:

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.wiki_poll` | 15s | install | How often the runner looks for an ended run and for changed settings. |
| `agents.wiki_max_batches` | 20 | install | The most batches one run may write (the kit's upkeep limit). |
| `agents.wiki_max_new_pages` | 10 | install | The most new pages one run may make (the kit's upkeep limit). |

The Waymarks address, timeout and largest answer are the adapter's ([knowledge-waymarks.md](knowledge-waymarks.md)).

## Checked by

- `go test ./internal/wikijobs/scope`: `MayServe` over every class; an answer that fits is let through; each of twenty ways an answer is refused, by its reason (too many batches or new pages, a write, delete, raise, note or close the grant does not allow, a page with no path or text, a change with no revision, a source it may not cite, a sensitive source on a normal page, a ref named twice, a link to no ref, an issue reason that is none); refs resolved to ids.
- `go test ./internal/wikijobs`, with the turn runner, background tasks, the scheduler and the fake provider playing the person's local model, and a hosted model in the person's fallback chain:
  - **The item's proof, against a fake Waymarks** (`fake_test.go`, written from Waymarks' source), always: a fact told in chat is added by the memory pass under the intake token; with the compile job's token stored and the job switched on, a run now reads the sources under the job's token and ends with its answer; Keystone writes two new pages that link to each other by Waymarks' ids, the beehives page citing the source as `[[source:N]]` and in its sources; Waymarks' audit shows `sources.add` under `keystone-intake` and every batch call under `keystone-compile` alone, which also read the sources; the run's chat is held, had no memory pass, and ends with the note; the hosted model was never called; every model call of the run went to the local model; the audit log holds the run's tool calls and its three page writes under its request id, with no token in it; a second run writes nothing more.
  - **The same proof against a real Waymarks** (`real_test.go`): the binary started on this machine with a throwaway data folder on the first free port from 7955 to 7959, its owner, a `memory` space and three invented principals made through its API with the kit's grants, their tokens in memory only. It skips, saying why, when no binary is found (`KEYSTONE_WAYMARKS_BIN`, the path, or `Waymarks/waymarks/bin` beside a folder above), and when no port is free (decision [0444](decisions/0444-the-proof-against-a-real-waymarks-runs-in-go-test-and-skips-without-it.md)). Run on the owner's machine on November 25, 2026: it passed.
  - **The built-in store's compile job**: the memory pass's source becomes a cited page through a run now, nothing is left uncited, and the Waymarks jobs say they cannot run on the built-in store.
  - The jobs are the agent kit's (grants word for word, and what each lets a job write); every job has its settings, off as shipped, and instructions a schedule takes; when a job runs reads and refuses; the answer reads and refuses a misspelt part; the gate refuses a hosted model for a wiki run's turn and asks the inner gate otherwise; the memory switch; the tools are offered only in a run's chat; an answer that asks for more than the grant opens no batch; a stale write drops its batch and nothing after it is written; a job's schedule follows its settings, is paused by a hosted model, and the routes switch it.
- `verify`, the standing check `wiki jobs` (`tools/verify/wikijobs.go`): the guard's rules, and on the real server put together by `internal/app`, with the built-in store, a fact told in chat on a cited page after a run of the compile job, the run's chat held and with no memory pass, the hosted model in the chain never called, and the run's tool call and write in the audit log under its request id. The four routes are walked with every other route by `tenant isolation`, `calls recorded` and `no keys in output`, and `no secrets` now knows the shape of a Waymarks token.

## Provisional

- **The owner's real Waymarks.** Run against a Waymarks test instance on this machine only. The live run waits on the owner making each job's principal and grant and storing its token ([the runbook](../runbooks/wiki-jobs.md)).
- **The screen** has no mockup and waits on the agent mockups (P1-05).
- **Writes after outside content** run without a person's answer to each, under the job's grant: an owner question (decision [0441](decisions/0441-a-wiki-jobs-writes-come-from-its-answer-through-its-own-grant.md)).
- **A real model.** Only the fake provider has played a job. How well the owner's local model follows the instructions and gives the answer's shape is unknown.

## Not here yet

- A job's issue tools write nothing on their own; `lint_report` and the issue routes have been called on the fake only.
- Telling agents outside Keystone that a source was withdrawn (decision 0371's notice source).
- A step-by-step view of a run: its chat is where its steps are.
- The kit's `revert_changes` and `drop_batch` as tools: the owner reverts a batch in Waymarks.
