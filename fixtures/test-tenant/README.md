# Test tenant

Test data. Everything in this folder is invented: the person, the password, the chats, the tools and the answers. Every JSON file says so itself with `"test_data": true`, and the loader refuses a file that does not.

This is Test Person One, the tenant `rebuild` loads so a fresh checkout has someone who can sign in and a model that answers with no key. The loader and the formats are described in [`docs/architecture/test-tenant.md`](../../docs/architecture/test-tenant.md).

| File | Holds |
| --- | --- |
| `tenant.json` | The person's name and password. The password is invented, published here, and guards nothing. |
| `turns/` | Recorded model turns, as fake provider scripts. The format is in [`docs/architecture/fake-provider.md`](../../docs/architecture/fake-provider.md), and `fixtures/fake-provider/` has examples to copy. |
| `chats/` | One file for each chat: its title and the person's messages. The replies come from `turns/` when the chat is loaded. |
| `cases/` | Use cases and their expected results, which the `test tenant` check in `verify` runs. |
| `knowledge/` | Invented pages and sources in two ordinary spaces and a sensitive one, with the questions the `recall` and `sensitive content` checks ask (`knowledge.json`), and the messages and facts the `memory pass` check expects (`memory.json`). The checks load it themselves; the loader does not read this folder and the binary does not embed it. |
| `past-chats.json` | What the `past chats` check expects (P5-05): the recaps the recorded turns give three of the chats, the questions about earlier chats and what the search and the reply must hold, and the chat held for sensitive content. The loader does not read it and the binary does not embed it. |
| `fixture.go` | Compiles this folder into the binary. |

## Adding a chat

1. Write the person's messages in a new file under `chats/`, with a title no other chat has.
2. Add a script under `turns/` for each message, unless the catch-all reply is what you want.
3. Run `go test ./internal/testtenant`. A chat the recorded turns cannot answer is refused with the reason.

## Adding a use case

Add an entry to a file under `cases/`: what is done, and what must come of it. `go run ./tools/verify` runs it.

Use invented content only. Nothing here may be a real name, address, account or key. Files, images and a knowledge store are added by the items that build what holds them (P9-02 and P6-02).
