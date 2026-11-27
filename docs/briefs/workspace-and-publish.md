# Brief: workspace, preview and publish

Type: new (it brings back work the reboot brief deferred)
Requester: the owner
Owner: the owner
Status: fuzzy
Last updated: November 27, 2026

## The ask

The owner pasted a README draft for the reboot and said "Let's also add the following". Its new parts:

- **Build.** A project gets isolated virtual compute, a filesystem and a way to run its code. The agent can build an app with a frontend, an API and background work.
- **Preview.** The app's web server starts inside the workspace. A private preview address opens on a phone, and the real workflow can be tried, including saving a change and reloading.
- **Publish.** An AWS skill describes one supported serverless stack and how to package an app for it. A deployment tool applies the approved release and returns the app's address and status. The stack is an open decision.
- **Operate.** A deployed app owns its data and scheduled jobs, and its jobs keep running while the home Keystone is off.
- **Apps beside the assistant.** From the old Go Keystone: a domain module owns its data, offers tools to the assistant, and has a web panel a person uses on the same records (the old Budget and Weather modules).

## The problem

Keystone can write an app as an artifact but cannot run, try or ship one. The owner wants to ask for an app from his phone, try it on his phone, approve it, and have it running on its own afterwards.

## Why now

The chat, artifacts, approvals, background tasks and schedules this needs are built. Scripts were deferred until a sandbox was researched; the workspace is that sandbox.

## Done looks like

Not settled. Draft, from the paste's own example with an invented city:

- Ask Keystone to build a small weather app. It is written and run in the project's workspace.
- Open the preview on a phone, save a city, reload, and see it kept.
- Approve the release. Keystone deploys it and returns its address and status.
- The next morning, with the home machine off, the app shows an outlook its own scheduled job wrote.

## Out of scope

Not settled.

## Constraints

- Fixed rule 1: a workspace belongs to one person and reaches nothing of anyone else's.
- Fixed rule 2: a deploy is a side effect. It happens only under the approval policy, and nothing the agent read can approve it.
- Fixed rule 3: content from a sensitive space reaches no workspace or deployed app unless the owner decides how.
- Fixed rule 4: every command run, preview opened and deploy is recorded.
- Keys stay in the vault. The agent and the code in a workspace never see the cloud account's keys or the model keys.
- The repo is public: no account ids, addresses or real places in code, fixtures or docs.
- The host is a Windows PC. The local model machine is a 64 GB Mac mini that may be off.

## What already exists

- The reboot brief defers agent-written scripts and script workflows until the sandbox is researched (decided November 24, 2026). Its open question: "Docker vs WSL vs Windows sandbox, isolation per tenant, and what a script may reach".
- The reboot brief says Factory (New Keystone's Codex worker, previews and publish approvals) "is not part of this build; its job-run, preview and artifact pieces are reused and renamed". This ask is close to Factory's flow.
- Artifacts: "publish" today means a stable signed-in address on this Keystone that shows one artifact in a sandboxed frame (decision 0423). The new meaning is a running app with its own infrastructure. Two meanings of one word.
- Approvals, background tasks with their own chat, schedules, connectors (MCP) and the audit log are built.
- The old Go Keystone's stones: each a plugin with its own data, MCP tools and a web panel.
- The current README describes the build as it was planned; the pasted draft describes what reboot does at commit 15fc019.

## Terms

- **Workspace**: one project's isolated machine: its files, its running processes and its preview.
- **Preview**: the app running inside its workspace, reached through a private address.
- **Release**: one approved, packaged version of an app.
- **Deploy**: putting a release onto the cloud account. Not the artifact "publish".
- **App**: what a project builds and deploys. Replaces "stone" for the new build, unless the owner prefers the old word.

## Decisions

None settled.

## Open questions

| Question | Owner | Blocks start? |
|---|---|---|
| Where does a workspace run? | the owner | Yes |
| Is this Factory brought into Keystone, or does Factory stay separate? | the owner | Yes |
| Which AWS stack is the one supported stack? | the owner | Yes, for publish |
| How does a deployed job reach a model when the home machine is off, and whose spend cap covers it? | the owner | Yes, for operate |
| Do deployed apps offer tools back to Keystone, as the old stones did? | the owner | No |
| Does the pasted draft become the repo's README? | the owner | No |

## Log

- November 27, 2026: started from the owner's pasted README draft. Read the reboot brief, the plan, the artifacts doc and the current README before asking.
