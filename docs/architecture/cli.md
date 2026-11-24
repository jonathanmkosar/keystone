# Command line

Folder: `cmd/keystone`

`keystone` is the one binary. It will be the server and its command line. Today it has three commands.

| Command | What it does |
| --- | --- |
| `keystone settings` | Prints every setting: key, lane, type, who may change it, default and description. `--json` prints the same as JSON. `--lane core` prints one lane. |
| `keystone version` | Prints the version. A plain build says `dev`. |
| `keystone help` | Prints the usage. So does running it with no command. |

An unknown command prints the usage and exits with code 2. A broken settings registry exits with code 1 and says what is wrong.

Each command is a plain function that takes its arguments and two writers and returns an exit code, so tests call it directly.
