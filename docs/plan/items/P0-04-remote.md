# P0-04. Private remote repo

Status: done
Lane: core
Depends on: P0-02
Owner gate: Jonathan's go-ahead to create the private repo.
Built without the gate: All work continues in the local git repo and is pushed when the go-ahead comes.
Scope: Create the private repo once Jonathan gives the go-ahead and the name. Push main.
Proof: main is on the remote and a fresh clone passes `rebuild`.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Result, November 24, 2026: the owner chose a branch of his existing public repo over a new private one. The work is on the `reboot` branch of github.com/jonathanmkosar/keystone, which shares no history with `main`. Proof: the branch is on the remote. A fresh clone passing `rebuild` on Windows is still to be shown, under P0-03.
