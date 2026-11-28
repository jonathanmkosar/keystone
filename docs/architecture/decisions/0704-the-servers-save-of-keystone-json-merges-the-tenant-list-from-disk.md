# 0704. The server's save of keystone.json first merges the tenant list from disk

November 28, 2026 · decided in P15-02

## The question

Before P15-02 the server saved `keystone.json` only when it recorded its keeper or restored a person, and a person added from the command line while it ran was seen only after a restart (decision 0641). Now an admin adds people from the browser, so the server saves the list whenever one is added, and the copy it holds in memory does not have a person `go run ./tools/tenant add` wrote meanwhile: the save would drop them from the list, leaving their folder unlisted. What does the server do?

## The decision

Every save of the registry first reads `keystone.json` from disk and adds to the list in memory each person it lists that the process does not, whose folder is there. The server then serves them as well, and writes them back. A person the process holds is never removed by the merge. A file that does not read stops the save. Making a person, if its save fails, takes out that one person by id, not the last in the list.

## Why

- It is the recommended answer in `STATE.md`, and the smallest change that keeps a person added from either place.
- Serving the merged person at once is safe: a store opens and brings its database up to date when first used, and a plain person is sealed at the next start, as for one added from the command line (P15-06).

## What it costs

- A person removed from the command line while the server runs is written back by the server's next save, as it would have been before. Removing a person is done with the server stopped.
- Every save reads the file once more.
