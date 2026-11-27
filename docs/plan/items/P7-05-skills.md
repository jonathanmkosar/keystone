# P7-05. Skills

Status: doing (agents)
Lane: agents
Depends on: P7-04
Owner gate: none
Scope: Skill folders with instructions loaded when relevant; per-tenant skill list.
Proof: A test skill is loaded only when its task comes up, visible in the inspector.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (November 27, 2026)

Branch `agents/p7-05-skills`, cut from `reboot` at aed8214. An earlier session began this item and stopped before its first commit; this plan takes over its draft and the start it made on `internal/skills` (the shape of a skill, the reading of a `SKILL.md`, the options). A session that picks this up reads this plan, `git log origin/reboot..HEAD` and `git diff` first.

**A skill** is a folder: a `SKILL.md` with a name, one line that says when to use it, and the instructions, and supporting files that are read as text. Nothing in a skill is ever run: a script in a skill's folder is text like any other file.

**The record**, `internal/skills`, a new package. Each person's skills are rows of their own database, in the tenant's own folder (migration `20261127T041237_skills.sql`: `skills`, `skill_files`, `skill_switches`, `skill_loads`). The instructions and each supporting file are kept as text in those rows, not as files beside them, so one change is one transaction and nothing on disk can be left behind (a decision record says why). A person adds one by sending the `SKILL.md` text or the three fields, with the folder's other files as text; changes it, switches it off and on, removes it. Built-in skills come from a folder compiled into the binary (`internal/skills/builtin/`, invented and harmless), are off until a person switches one on, and cannot be changed. Limits are settings in `settings/agents.json`: how many skills, how long the description, the line each call carries, the instructions and each file may be, and how many files. The package reads no setting itself (the caller passes plain values), so it is a guard of fixed rule 1.

**Loaded only when relevant.** In `internal/turn/skills.go`, the turn's side, with a few added lines in shared turn code: two in `build` (`context.go`), one in `resumeStep` (`approvals.go`), one in `row.lookup` (`lookups.go`), a field on `row` (`store.go`) set where the connectors' is (`runner.go`), and a field in the inspector's reply (`inspect.go`).

- Each model call of a person who has a skill switched on carries one paragraph after the standing text and the words on visuals, before anything that may change from call to call: `agents.skills_standing_text`, which says in one line that a skill is instructions and not authority, and under it each skill's name and its one line, by name, so the paragraph is the same in every call until the list changes. The call is offered `load_skill` (`name`, and `file` for a supporting file), whose description is `agents.load_skill_description`.
- `load_skill` only reads, runs through a registry of one as the knowledge lookups do (recorded in the audit log, timed, checked by the approval rule), and answers with the skill's instructions and the names of its files, or with one file's text. Its result is the person's own content, not outside content (a decision record says why), so it marks nothing. Each load is a row of `skill_loads`, which the inspector shows for the turn.
- With no skill switched on, with tools off, or for a model without tools, a call is what it is today: no paragraph, no tool, nothing written. A plain turn of a person with no skill writes exactly what it writes today, so the load test's fast path needs no change.
- A skill changes no rule: the approval policy, the sensitive rules and call recording are in code a skill's text never reaches, and `internal/skills` imports none of routing, the model classes, recall or the approval rule.

**Routes.** `GET` and `POST /api/v1/skills`, `GET`, `PUT` and `DELETE /api/v1/skills/{skill}`. The drill's `pathValue` gets `{skill}`, with a skill the first person adds through the API. `docs/api/openapi.json` regenerated.

**The screen.** Settings → Skills (`SkillSettings.tsx`, new): the list with a switch each, add by choosing a folder's files or by writing or pasting the text, edit, remove; the loaded skill as a step in the chat ("Loaded the skill …", a line in `steps.ts`), and the turn's loads in the inspector. Built from the design tokens; no mockup, so provisional. On a phone nothing autofocuses, text boxes are 16px, and nothing new is an overlay.

**The proof.** `go test ./internal/skills` and `./internal/turn` with the fake provider: a test skill is named in the standing text of every call, loaded in the scripted turn whose task it fits and not in another, the load is in the inspector and the audit log; a second person is offered nothing of it, cannot load it by name and reaches it on no route. A recorded test-tenant turn (`fixtures/test-tenant/turns/skills.json`) and a standing check, `skills`, run the same on the real server.

Left out: running a skill's scripts, a switch per chat, sharing a skill between people, and images or other files that are not text.
