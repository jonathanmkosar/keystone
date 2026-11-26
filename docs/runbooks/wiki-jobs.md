# Wiki upkeep jobs: tokens and switching them on

For the owner. Keystone runs Waymarks' agent-kit jobs (compile, lint, upkeep, fix issues) as scheduled agents, each with a principal, a grant and a token of its own in Waymarks, so the facts the memory pass adds become cited pages and Waymarks' audit shows each job by name. The built-in store has a compile job too, which needs no token. Every job is off until you switch it on. How it works: [docs/architecture/wiki-jobs.md](../architecture/wiki-jobs.md).

You type every token yourself. None goes in a file, a chat, a commit or the journal.

## Before you start

- Keystone already reads your Waymarks: `knowledge.adapter` is `waymarks`, `knowledge.waymarks_address` says where it is, and the reader and intake tokens are stored ([knowledge-waymarks.md](../architecture/knowledge-waymarks.md)).
- A local or trusted model is in your registry, and `routing.memory_model` names it (the jobs use it unless a job's own `agents.wiki_<job>_model` names another). A job will not run on a hosted model.
- You have Waymarks' command line and an administrator's token in `WAYMARKS_TOKEN` in that terminal, and `WAYMARKS_URL` pointing at your instance.

## 1. One principal and one grant for each job

The grants are the agent kit's (`agent-kit/grants/*.json` in the Waymarks repository). They cover your normal spaces only. Add a sensitive space to a job's grant only if you want that job, on that model, to read it (`;health:read,search,write_pages` and so on).

    waymarks principal add keystone-compile    --kind agent --tool "Keystone" --model "<your local model>" --grant "*:read,search,write_pages,delete_pages"
    waymarks principal add keystone-lint       --kind agent --tool "Keystone" --model "<your local model>" --grant "*:read,search,write_pages,delete_pages,raise_issues"
    waymarks principal add keystone-upkeep     --kind agent --tool "Keystone" --model "<your local model>" --grant "*:read,search,write_pages,delete_pages,raise_issues,close_issues"
    waymarks principal add keystone-fix-issues --kind agent --tool "Keystone" --model "<your local model>" --grant "*:read,search,write_pages,close_issues"

You need only the jobs you will switch on. The kit's advice: the upkeep job is the one to schedule, since its daily round does what the other three do.

## 2. A token for each, straight into Keystone's vault

For each job you made, issue a token and pipe it into the vault, so it is never shown or saved. `<tenant-id>` is yours (`go run ./tools/tenant list`); add `--data <folder>` after `tools/tenant` if your data folder is not the default. The server can stay running. In PowerShell, from the Keystone folder:

    waymarks token issue keystone-upkeep --label "keystone" | go run ./tools/tenant secret set <tenant-id> knowledge.waymarks.upkeep

The names, one per job:

| Job | Principal | Secret in Keystone's vault |
| --- | --- | --- |
| compile | `keystone-compile` | `knowledge.waymarks.compile` |
| lint | `keystone-lint` | `knowledge.waymarks.lint` |
| upkeep | `keystone-upkeep` | `knowledge.waymarks.upkeep` |
| fix issues | `keystone-fix-issues` | `knowledge.waymarks.fix_issues` |

If you issued a token in the Waymarks app instead, run `go run ./tools/tenant secret set <tenant-id> knowledge.waymarks.upkeep` and paste it when it waits for input.

## 3. Watch the first run, then switch it on

1. Open **Scheduled**. Under **Wiki upkeep**, each job says whether it can run. "Cannot run" names what is missing: the token, the store, or a local or trusted model.
2. Press **Run now** on the job. Its run is a chat of its own, in the sidebar: open it to watch each step. When it ends, Keystone writes what the job's answer asked for, and a note at the end of the chat says what it wrote and that Waymarks shows it as the job's principal.
3. Look at the change in Waymarks (`waymarks log`, or the change feed). Any batch can be undone there with `waymarks revert <id>`.
4. Switch the job on. It runs at the time in `agents.wiki_<job>_when` (Settings → General), in your time zone: `daily 04:00` for upkeep as shipped.

## Changing or stopping a job

- **When, model, budget**: Settings → General, the `agents.wiki_<job>_…` settings. Its schedule follows at the next look, within `agents.wiki_poll`.
- **Stop one job**: its switch on the Scheduled screen. In Waymarks, `waymarks suspend keystone-upkeep --reason "…"` refuses every call it makes.
- **Stop everything**: **Pause all agents** on the Scheduled screen holds every job with every other agent.
- **Take a job's access away**: `waymarks token revoke keystone-upkeep`, then `go run ./tools/tenant secret delete <tenant-id> knowledge.waymarks.upkeep`. The job then says it cannot run, and its schedule is paused.

## The built-in store

With `knowledge.adapter` set to `built-in`, only `builtin_compile` can run. It needs no token: switch it on under Wiki upkeep, after one Run now.
