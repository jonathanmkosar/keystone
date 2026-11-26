# P8-06. Wiki upkeep jobs

Status: doing (agents)
Lane: agents + knowledge
Depends on: P8-03, P6-03, P6-06
Owner gate: none
Scope: The Waymarks agent-kit jobs (compile, lint, upkeep, fix issues) run as Keystone scheduled agents on a local or trusted model, each with its own Waymarks token and grant, so sources added by the memory pass become cited pages. The built-in store gets a simple compile job.
Proof: On a Waymarks test tenant, a fact told in chat appears on a cited page after the next compile run, and the Waymarks audit shows each job under its own token.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan, November 25, 2026 (agents, branch `agents/p8-06-wiki-jobs`, cut from `reboot` at cb906f7). P6-03 is provisional on its live check only; the integrator decided to build this now. Nothing here rests on the owner's real Waymarks or his tokens.

The jobs are Waymarks' own agent kit (`agent-kit/jobs/*.md` and `agent-kit/grants/*.json` in the Waymarks repository), followed as written: the same four jobs, the same MCP tool names, the same grants, one principal and one token for each.

What will be built:

1. **`internal/wikijobs`**, a new package: five jobs, `compile`, `lint`, `upkeep` and `fix_issues` on Waymarks, and `builtin_compile` on the built-in store. Each job is a schedule (P8-03) the job owns (a row in `wiki_jobs`), kept in step with the person's settings: on or off (off as shipped), when it runs, its model and its budget for each run, all in `settings/agents.json`. Its secret is its own Waymarks token, `knowledge.waymarks.<job>`, in the person's vault.
2. **A run is the schedule's run**: a background task in a chat of its own, with the schedule's budget and the pause switch (P8-05) as every run has. Before the task's first round the chat is held for sensitive content (a note Keystone writes in it with the sensitive carry), so every call of it, task rounds, recaps and compaction, goes only to a local or trusted model; a second guard, at the turn's gate, refuses a hosted model for any call of a wiki run. A wiki run's chat runs no memory pass, so a job's report never becomes a source. Both guards are in a package that reads no setting.
3. **The job's tools**: Waymarks' read tools by the kit's names (`whoami`, `read_schema`, `read_catalog`, `list_sources`, `read_source`, `search`, `read_page`, `page_links`, `list_issues`, `read_issue`, `lint_report`), offered only in that job's run chats and only those its grant has, each a call to Waymarks' API under the job's own token. The built-in compile job gets `list_uncompiled_sources`, `read_source` and `read_page`. Withdrawn sources (decision 0371) are left out of every answer.
4. **Writes**: the job ends with one JSON object, the batches it wants (page writes and deletes; raised, noted and closed issues, as its grant allows). When the run is done, Keystone applies it through the job's own grant: a Waymarks batch under the job's token, so Waymarks' audit shows the job's principal, or `builtin.WritePage`. Each write is in Keystone's audit log, and a note in the run's chat says what was written. Anything else a job would do asks (Keystone's own tools that change something) or is refused. Why the writes come from the answer and not from tool calls: fixed rule 2, as the memory pass does (a decision record).
5. **Routes and screen**: `GET /wiki-jobs`, `POST /wiki-jobs/{job}/on`, `…/off`, `…/run`; a "Wiki upkeep" section on the Scheduled screen with each job's state, last and next run, Run now and its switch. Provisional on the agent mockups (P1-05).
6. **The proof**: a test against a fake Waymarks that always runs (a fact told in chat, the memory pass, a compile run, a cited page, each job under its own token in the fake's audit); the same against the real `waymarks` binary, started on a free port from 7955 to 7959 with a throwaway data folder, which skips with its reason when the binary is not on the machine; and the built-in compile job with the fake provider. A standing check, `wiki jobs`, for the two guards.
7. **The write-up**: `docs/architecture/wiki-jobs.md`, decisions from 0440, `docs/runbooks/wiki-jobs.md` (each job's principal, grant and token, then switching the jobs on), journal `2026-11-25-agents-009.md`.

Left: the live run against the owner's Waymarks with his tokens; the screen's look (P1-05).

