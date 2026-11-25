# Model registry entries

Test data. Everything in this folder is invented: the models, the providers, the addresses and the prices. No address here leads anywhere, and no entry holds a key. Each file says so itself with `"test_data": true`.

These are example entries for the model registry. The format is described in [`docs/architecture/models.md`](../../docs/architecture/models.md).

| File | Holds |
| --- | --- |
| `examples.json` | Entries the registry accepts: the fake, two local models on this machine, a gateway on the home network, a hosted model and a trusted one. Between them they use all four adapters and all three classes. |
| `refused.json` | Entries the registry refuses, each with a part of the reason it gives in `why`. |

## Writing an entry

1. Copy the example nearest to what you need.
2. Give it a `name` the tenant does not have yet.
3. Put the name of the secret that holds the provider key in `key`, never the key.
4. Run `go test ./internal/models`. An entry that is not valid is refused with the reason.
