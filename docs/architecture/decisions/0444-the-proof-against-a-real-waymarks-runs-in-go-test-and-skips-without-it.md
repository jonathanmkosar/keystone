# 0444. The proof against a real Waymarks runs in go test, and skips without it

November 25, 2026 · decided in P8-06

## The question

The item's proof needs a Waymarks test tenant: a fact told in chat appears on a cited page after a compile run, and Waymarks' audit shows each job under its own token. The owner's own instances and tokens are not to be touched. `verify` must stay green on a machine with no Waymarks, and must never be weakened. Where does the proof run?

## The decision

**Twice, in `go test ./internal/wikijobs`, with one body of assertions (`proveCompile`):**

- **Against a fake Waymarks**, always: an `httptest` server written from Waymarks' own source (its routes, its `{"data": …}` answers, a principal for each token with the agent kit's grant, the refusal of an unknown field, and an audit log by principal).
- **Against the real binary**, when it is on the machine: found from `KEYSTONE_WAYMARKS_BIN`, then `waymarks` on the path, then `Waymarks/waymarks/bin/waymarks` beside a folder above the test. The test makes a throwaway data folder, runs `waymarks init` and `waymarks serve` on the first free port from 7955 to 7959, makes the `memory` space and three invented principals with the kit's grants through Waymarks' API, holds their tokens in memory only, and stops the instance and removes the folder when it ends. Without a binary, or with every port taken, it skips and says why.

`verify` runs both through its `tests` step. Its own standing check, `wiki jobs`, proves the guard and the whole path on the real Keystone server with the built-in store, which needs nothing outside the repository.

## Why

- The same assertions against the fake and the real instance mean the fake cannot drift unseen on a machine that has the binary, the owner's among them.
- A skip with its reason is honest: the check that cannot run says so, and every check that can still runs. Nothing is loosened on a machine with the binary.
- The real instance never sees the owner's data or tokens, and never his ports, 7878 and 7879.

## What it costs

- On the owner's machine every `verify` starts a Waymarks instance for about a second.
- The proof against the owner's own Waymarks, with his tokens, is still his to run (the runbook's step 3).
