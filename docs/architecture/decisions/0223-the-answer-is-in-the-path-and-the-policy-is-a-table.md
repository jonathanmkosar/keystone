# 0223. The person's answer is in the route's path, and each tool's policy is a table, not a setting

November 25, 2026 · decided in P7-02

## The question

The item asks that the decision be in the audit log. The API's audit wrapper records a call's method and path and never its body (decision 0009, `audit.md`). Where does the answer go so the log holds it without a new kind of entry? And where is each person's policy for each tool kept: in settings, or somewhere of its own?

## The decision

**Two routes, one for each answer, with no body**: `POST …/approvals/{approval}/approve` and `…/decline`. The audit log's entry for the call holds the path, so it says which call, what was answered, who answered (the person's actor), when, and under which request id. The approval's row keeps the same actor and request id. A call that did not run is also in the log as the tool's own two entries, the second `refused`, with the sentence the model read.

**Each tool's policy is a row in `tool_policies`**, read by the turn, set by `PUT /tools/{name}/policy` and removed by `DELETE`. Only tools Keystone knows can have one.

## Why

- The log records what the route was, and the route is the answer. No new kind of entry, no body in the log, and no change to `internal/audit`, which is the guard of fixed rule 4 and another lane's.
- A setting has one key for each value, fixed in the registry. Tools come and go with connectors (P7-04) and their names are not known when the registry is written. A JSON blob in one setting would be drawn by General as a text box.
- The policy is read by the turn and passed to `approval.Decide`. Keeping it out of settings also keeps it out of everything that reads settings.

## What it costs

- Two routes where one with a body would do.
- The policies are not on the General screen. The Approvals screen draws them instead.
- An admin cannot set a policy for a person yet; nor can a person set one for a tool before it exists.
