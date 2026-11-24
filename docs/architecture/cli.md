# Command line

Folder: `cmd/keystone`

`keystone` is the one binary. It will be the server and its command line. Today it has four commands.

| Command | What it does |
| --- | --- |
| `keystone serve` | Serves the web interface that is compiled into the binary. It listens on the `server.address` setting, `127.0.0.1:7900` unless changed, or on `--address host:port`. It prints the address it is listening on, and stops on Ctrl+C. |
| `keystone settings` | Prints every setting: key, lane, type, who may change it, default and description. `--json` prints the same as JSON. `--lane core` prints one lane. |
| `keystone version` | Prints the version. A plain build says `dev`. |
| `keystone help` | Prints the usage. So does running it with no command. |

An unknown command prints the usage and exits with code 2. A broken settings registry exits with code 1 and says what is wrong.

## What serve does today

It answers `GET` and `HEAD` for the files in `web/dist/` and refuses every other method. Every response carries headers that stop the page being framed, stop content types being guessed, and allow files from Keystone itself only. There is no API, no session and no data folder yet: the API shell (P2-05) takes over this server and adds them. Port 0 asks the system for a free port, which is how `rebuild` and the tests start it.

Each command is a plain function that takes its arguments and two writers and returns an exit code, so tests call it directly.
