# P13-02. Tenant isolation drill

Status: doing (core)
Lane: core
Depends on: P11-03
Owner gate: none
Scope: A second tenant's session, tools, jobs and agents try to reach the first tenant's data.
Proof: Every attempt is refused or empty, and audited.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Branch `core/p13-02-isolation-drill`, cut from `reboot` at a0ba86a (Operations merged). Run now by the integrator's decision: P11-03's open parts are its mockups and room sizes, and its report and routes are built.

The standing check `tenant isolation` already walks every route with no session, a re-addressed session and a signed-out one, and proves a call is in its own tenant's log. What it lacks, and what this drill adds, is a second tenant that is signed in and holds the first tenant's real ids.

**The drill** is a new package, `internal/isolationdrill`: `Run` makes a throwaway data folder with two invented people, fills the first with data of every kind through the real server (`app.Open`, a scripted local model on the model seam, a test MCP server and a stand-in Google on this machine), then has the second try every way in and returns each attempt with its result. A test runs it, and a new standing check, `isolation drill`, runs it in `verify` (one line in `checks`; everything else in a new file). Every piece of the first person's content carries an invented marker word, so a leak is found wherever it lands.

What the second person tries, signed in as themselves:

1. **The API**: every route that takes an id, with the first person's ids (chats, turns and their events, inspect, approvals, pins, skips, uploads and their content, artifacts with their versions, compare, download and sandbox page, projects and their files and chats, styles, attached chats and handoff, schedules and their runs, tasks and their events, connectors, Google, memory, forget and correct, embeddings, usage, operations, wiki jobs, models, secrets, settings, tool policies, incognito); their own chat with the first person's turn, upload, artifact and message ids; guessed and forged ids; path tricks (`..`, encoded slashes, the first person's folder and file hashes in file routes); the first person's ids in bodies and query strings; cookie swaps (two cookies, a token re-addressed to the first person, the token in a header).
2. **Tools**: a turn whose model calls every built-in tool with the first person's ids, titles and hashes: `search_chats`, `read_chat`, `open_attached_chat`, the three knowledge lookups, `read_artifact`, `update_artifact`, `load_tools` and connector tools, Gmail and Calendar against the stand-in.
3. **Jobs and agents**: a background task, a schedule run, the built-in store's wiki compile job and the memory pass, each aimed at the first person's chats, files, knowledge and vault (secret names of the first person's in the second's model and connector records).
4. **Files on disk**: the first person's folder is listed and hashed before and after each part; their database is compared table by table.
5. **The audit log**: every attempt is in the second person's log under its request id, with the method and path tried, refused or answered with nothing; no token or key is in either log; the first person's log does not grow.

A hole found is fixed in the smallest local change, with a test, and listed as a finding. Already seen while reading: a model's `ca_cert` may be a path, and the API takes one, so a person can make Keystone read any file on the machine, another person's included, and learn from the error whether it exists.

Left out, with the reason in the doc: share links (P10-04) and sub-agents (P8-02) are not merged; each adds its tries here when it lands. Waymarks is one instance per person at their own address with their own tokens; the drill uses the built-in store.

