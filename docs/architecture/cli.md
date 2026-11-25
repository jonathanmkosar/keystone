# Command line

Folder: `cmd/keystone`

`keystone` is the one binary: the server and its command line.

## Commands that need no server

| Command | What it does |
| --- | --- |
| `keystone serve` | Serves the API and the web interface that is compiled into the binary. It listens on the `server.address` setting, `127.0.0.1:7900` unless changed, or on `--address host:port`. It prints the address it is listening on, and stops on Ctrl+C. |
| `keystone settings` | Prints every setting: key, lane, type, who may change it, default and description. `--json` prints the same as JSON. `--lane core` prints one lane. |
| `keystone openapi` | Prints the API's OpenAPI document. `--out <file>` writes it to a file, which is how `docs/api/openapi.json` is brought up to date. |
| `keystone version` | Prints the version. A plain build says `dev`. |
| `keystone help` | Prints the usage. So does running it with no command. |

An unknown command prints the usage and exits with code 2. A broken settings registry exits with code 1 and says what is wrong.

## What serve does

It opens the data folder, which is the `data.dir` setting or `--data <folder>`. A relative folder is read from where `keystone` is started. It then serves two things from one address.

- Under `/api/`, the API ([api.md](api.md)).
- Everywhere else, the files in `web/dist/`, for `GET` and `HEAD` only.

Every response carries headers that stop the page being framed, stop content types being guessed, and allow files from Keystone itself only.

It reads the `secrets.keeper` setting and tells the data folder where an install with no master key yet keeps one ([secrets.md](secrets.md)). The key store is not touched until a secret is used.

The server's own log, one line for each API call, goes to standard error. The first line on standard output is the address, which is what `rebuild` and the tests read. Port 0 asks the system for a free port.

The server has no write timeout, because a stream of server-sent events stays open.

## The client

These commands talk to a running server over the same HTTP API the web interface uses. They have no other way to the data folder.

| Command | What it does |
| --- | --- |
| `keystone people` | Lists the people who can sign in, with their ids. |
| `keystone signin <person-id>` | Signs in. The password is read from standard input, one line, and never from an argument. |
| `keystone whoami` | Says who is signed in, and until when. |
| `keystone signout` | Ends the session on the server and removes the session file. |
| `keystone api <METHOD> <path> [<json> \| -]` | Makes any API call and prints the answer. JSON is printed indented. A stream of server-sent events is printed line by line as it arrives. `-` reads the body from standard input. A path with no leading slash is read under `/api/v1/`. |

Each takes `--server <address>`, such as `http://127.0.0.1:7900`. Without it, the server last signed in to is used, and failing that the `server.address` setting.

When a call is refused, the client prints what the server said, the status, the error's code and the request id, and exits with code 1.

### The session file

`keystone signin` keeps the session token in `keystone/session.json` in the user's configuration folder (`%AppData%` on Windows), readable by that user only. `--session-file <file>` or the `KEYSTONE_SESSION_FILE` variable names another place. The token is sent back as the session cookie, and only to the server that gave it. No command prints it. Decision [0041](decisions/0041-the-session-travels-in-one-cookie.md) says what this costs.

A password typed at the prompt shows on the screen, as it does for `tools/tenant password`. Piping it in avoids that.

Each command is a plain function that takes its arguments, its input and two writers and returns an exit code, so tests call it directly.
