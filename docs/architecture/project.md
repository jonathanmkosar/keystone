# Project helpers

Folder: `internal/project`

Shared by the scripts in `tools/`. Nothing in the Keystone binary uses it.

- `Root` walks up from the working directory to the folder that holds `go.mod`.
- `Run` runs a command in a folder and returns what it printed.
- `Items` reads every work item in `docs/plan/items`: id, title, status, lane, dependencies and owner gate. `CheckItems` reports a dependency that does not exist, a loop, or a status the loop does not know. `Ready` lists the open items whose dependencies are done.
- `StateLine` reads one line of `STATE.md`, such as `Phase:`.
- `Skip` names the folders the scripts leave out when they walk the tree: `.git`, `bin`, `data` and `node_modules`.

An item's status is `todo`, `doing (lane)`, `provisional: reason` or `done`. Only `done` counts for the items that depend on it.
