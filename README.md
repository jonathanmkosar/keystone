# Keystone

Your own agent workspace: a chat modeled on Claude.ai that runs on your own machines, uses any model, local or hosted, and shows the work its agents do. It keeps what matters in a knowledge store you choose. [Waymarks](https://github.com/jonathanmkosar/waymarks) is the first.

Keystone is being built. The plan is in [`docs/plan/PLAN.md`](docs/plan/PLAN.md), and where the work stands is in [`docs/plan/STATE.md`](docs/plan/STATE.md).

## Run what exists

You need [Go](https://go.dev/dl/) 1.24 or later.

    go run ./tools/rebuild     build and run every standing check
    bin/keystone settings      print every setting, its default and who may change it

## The project scripts

    go run ./tools/rebuild     build, then verify
    go run ./tools/verify      run the standing checks
    go run ./tools/doctor      where the work stands, and what each lane can pick up next
    go run ./tools/snapshot    one hash for the source tree

## Where things are

    cmd/keystone/          the binary: the server and the command line
    internal/              the code behind it, one folder per part
    settings/              every setting, one file per lane
    web/                   the web interface
    tools/                 the project scripts above
    fixtures/test-tenant/  invented data for the standing checks
    design/                the mark, the colours and the mockups
    docs/architecture/     overview, one file per part, decisions/
    docs/plan/             PLAN.md, PARALLEL.md, LOOP.md, STATE.md, items/
    docs/journal/          one entry per loop, never edited afterwards
    docs/runbooks/         how to back up, restore, add a person, add a model

## Working on it

Read [`docs/plan/LOOP.md`](docs/plan/LOOP.md) first. The rules that matter most: invented data only, no secrets in the repo, values someone might change are settings and not constants, and nothing merges with a red `verify`.
