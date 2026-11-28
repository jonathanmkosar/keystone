# 0710. The install's values are kept in keystone.json, under settings and person_defaults, and every field and key it does not know is kept

November 28, 2026 · decided in P15-03

## The question

The spec says install settings are "stored in `keystone.json` under `settings` and read at start and on each change". Where do the install-wide defaults for each person's own settings go, how is the file written without losing what other code keeps there, and which install settings can be kept in it at all?

## The decision

- `keystone.json` gains two objects, each of key to value as text, as the registry writes a default:
  - `settings`: the value of each install setting the admin changed;
  - `person_defaults`: the default each person's own (`tenant`) setting starts from.
- `internal/tenant` keeps them (`installvalues.go`: `Install.InstallValues` and `ChangeInstallValues`) and still reads no setting. Each read takes the file from disk, so a value another process wrote is seen. Each write reads the file again under the install's lock, changes only its own object, adopts every other field another program wrote since the install was opened (the tenant list and the version stay the package's own), and writes the whole file through a temporary file and a rename, as before.
- A key the registry no longer knows, or a value that no longer fits its setting, is kept in the file and passed over when read.
- `data.dir` is refused: it names the folder `keystone.json` is in, so the server needs it before it can read the file. `keystone serve --data` is the way to set it.
- `server.address` is read from the file when `--address` is not given, so the admin can set it (after a restart).

## Why

- Two objects say what each value is when the file is read by a person, which one object of mixed keys would leave to the scope of each key.
- Reading from disk at each read and adopting other fields at each write keeps the file one truth for the server and the command line, as P15-02 does for the tenant list.

## What it costs

- A change rewrites the whole file, as adding a person already does.
- The tenant list is still written from memory: a person added from the command line while the server runs is dropped by any save, this one included, until P15-02's merge of the tenant list is in.
