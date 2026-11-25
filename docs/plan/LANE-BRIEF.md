# The lane brief

The brief an integrator gives an agent working one item in its own worktree. Fill in the parts in angle brackets. It stands alone: the agent knows only what is here and in the files it points to. `LOOP.md` says when to use it, including after a stop.

---

You work the <lane> lane of Keystone (Go server with an embedded React + Vite + TypeScript web interface; branch `reboot`). Your one item is <id>, <title>: `docs/plan/items/<file>.md`.

FIRST, inside your worktree, put yourself on the item's branch, cut from `reboot`, never from `main` (an unrelated Python history):

- If the branch `<lane>/<id lower>-<words>` exists (locally, or on `origin` after `git fetch origin`), check it out: a previous session started this item. Read the item's Plan, the branch's commits (`git log reboot..HEAD`) and any uncommitted files (`git diff`) first, and finish from there; do not start over.
- Otherwise `git checkout -b <lane>/<id lower>-<words> reboot`.

Read first: `AGENTS.md`, `docs/plan/STATE.md` (including "Waiting on the owner": build to each recommended answer that touches your item and mark that part provisional), `docs/plan/LOOP.md`, the item file, the parts of `docs/plan/PLAN.md` the item rests on, the component docs under `docs/architecture/` for the parts it touches, the decisions they link, and the latest journal entries of your lane. <Name the files, docs and code that matter most.>

Build: <what the item's Scope and Proof ask for, as far as it can be built today; what is gated and how to build without the gate; where the code goes; what must not change.>

Other agents may be working at the same time in: <their folders>. Keep your edits in shared code as few and local as you can, put new code in new files, and list every existing line you changed in shared files in your report. Expect `web/dist/` to conflict at merge; the integrator rebuilds it.

Hard rules:

- Work ONLY inside your own worktree directory. Never write to another checkout or to the main checkout's `data/` folder. A server you start for a check runs on a port of its own (never 7900 or 9080) with a data folder inside your worktree, and is stopped when you finish.
- Do not edit `docs/plan/STATE.md` or `AGENTS.md`. In shared files (`docs/architecture/overview.md`, `docs/architecture/tools.md`, `tools/verify/*.go`, `internal/app/*.go`, `settings/*.json`, `internal/README.md`) only add lines.
- A package that enforces a fixed rule must not import `settings` or `internal/tenantsettings`. A package that imports a network package is listed in `tools/verify/offline.go` with its reason. Tests talk only to this machine.
- `go run ./tools/verify` must be green before every commit; run it once per commit, no long or repeated runs; never weaken a check.
- Commit messages and authorship carry NO agent or AI attribution: no Co-Authored-By line, nothing. Subject style: `<id>: <what>`.
- Commit each time verify is green, and after every commit push your branch: `git push -u origin <branch>`. Never push `reboot` or any other branch. Do not open a pull request or merge.
- Public repo: invented data only. No real names, addresses, keys or tokens.

Order:

1. The plan in the item file, saying what will be built and what is left; commit and push.
2. The build with its tests, in steps that each end green; commit and push after each.
3. IMMEDIATELY the write-up: the component doc with its `componentDocs` line and overview row, decision records for choices that were not obvious (numbered from <next free number>, each linked in the overview's decision table), one journal entry `docs/journal/<date>-<lane>-<n>.md` in the shape of the lane's last one, and the item's status (`done`, or `provisional` with the gate it waits on) and Result; verify; commit and push.

Report back: the branch and commit hashes, the last line of `verify`, the exported surface other lanes will use, every existing line changed in shared files, the settings added, what you ran and what you did not, what is provisional, and any owner question with your recommended answer.
