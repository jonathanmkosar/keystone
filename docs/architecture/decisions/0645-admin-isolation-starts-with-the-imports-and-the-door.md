# 0645. The admin isolation check starts with the imports, the one writer of the mark, and a walk of the door

November 27, 2026 · decided in P15-01

**Changed** on November 27, 2026 by decision [0655](0655-admin-isolation-holds-the-separate-account.md), for the separate admin account.

## The question

The spec asks for a standing check, `admin isolation`, in two parts: that `internal/adminview` imports none of the packages that hold a person's content, and a drill of marker words (P15-05). What does the first part hold, and what else is cheap to hold while it is there?

## The decision

The check is `tools/verify/adminisolation.go`, the 46th. Today it proves:

- **The imports.** `go list -deps` of `internal/adminview` and of `internal/adminapi` reaches none of the packages that hold a person's content: the spec's chat, turn, context, knowledge, uploads, artifacts, projects, recap, inspector and share (`internal/chat`, `turn`, `contextbuild`, `knowledge` and below it, `uploads`, `artifacts`, `projects`, `recap`, `inspector`, `share`, `shareapi`), and the others that hold a person's words: `attach`, `compact`, `imported`, `importer`, `jobs`, `schedules`, `subagents`, `research`, `wikijobs`, `styles`, `visuals`, `embedding`, `connectors`, `mcp`, `google`, `operations`, and `tools` and below it. A failure names the chain of imports that reached it.
- **The install stays out of the routes.** `internal/adminapi` does not import `internal/tenant` itself.
- **One writer of the mark.** No Go file outside `internal/tenant` and `tools/tenant` calls `SetAdmin`.
- **The door, walked.** On a throwaway folder with the real handler: every admin route refuses a person without the mark (403 `not_admin`) and an admin without a step-up (401 `step_up`, but for the step-up routes), each refusal in the caller's own log and in no other; a wrong password at the step-up is refused and recorded; after a good one the people route answers, and both logs hold it, the other person's with the actor `admin:<id>`; a step-up that has ended, and a mark taken away by a second process, are refused again. The step-up routes in the code must be the ones listed in the file.

## Why

- A deny list, rather than a list of what may be imported, follows the spec, and lets P15-02 bring in narrow packages such as `usage` without editing the check. Checking through `go list -deps` catches a forbidden package reached through another.
- The walk is the item's proof made standing, so a later admin route is held to it with no new code: it walks every route marked `Admin`.

## What it costs

- A package added later that holds content is not on the list until someone adds it. The drill (P15-05) is the second net: it looks for marker words in every admin reply, whatever the imports.
