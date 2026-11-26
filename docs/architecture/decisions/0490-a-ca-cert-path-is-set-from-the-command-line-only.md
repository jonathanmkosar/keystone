# 0490. A model's ca_cert is PEM text through the API, and a path is set from the command line only

November 26, 2026 · decided in P13-02

## The question

A model's `ca_cert` is one certificate authority its endpoint is trusted with: PEM text, or the path of a PEM file on the machine Keystone runs on (decision 0120). The Chat Completions adapter reads a path with the server's own rights when the model is opened. The settings routes (P11-01) take a whole registry entry, `ca_cert` included. The isolation drill found that any signed-in person can therefore name any path: a file in another person's folder (`data/tenants/<their id>/files/<hash>`, both parts of which a person can learn), `secrets.bin`, a device, or a share on another host (`\\host\share\ca.pem`), which Windows opens by signing in to that host as the account Keystone runs under. Nothing of the file comes back to the person: a turn on the model says only that its entry is refused, and the reason goes to the server's log. But the read happens, with rights no person has. Where should the line be?

## The decision

**Through the API, `ca_cert` is PEM text.** `POST /api/v1/models` refuses an entry whose `ca_cert` is a path (a value without `-----BEGIN`, the way the registry's check and the adapter tell the two apart), with 400 `bad_model` and a sentence that names the command line; the path is not said back. `PUT /api/v1/models/{name}` refuses a path too, unless it is the value the stored entry already holds, so an entry set from the command line can still be changed in the browser, whose form does not show `ca_cert` and sends back what the entry has.

**A path is set from the command line**, `go run ./tools/tenant model add`, by whoever runs the install, as a connector that runs a program is (decision 0310). The registry (`models.Check`) and the adapter are unchanged, so the owner's gateway entry, which names its authority's file, keeps working.

The check is in `internal/settingsapi/cacert.go`, two lines in the add and replace handlers call it, and `TestACACertPathIsRefusedThroughTheAPI` proves it; the drill tries four such paths and must see each refused.

## Why

- Reading a file is a power over the machine, not over one person's data. The command line is where the machine's owner acts; the API is where every person does.
- A path in PEM's place adds nothing a person needs from the browser: they can paste the authority's PEM text, which is what the file holds.
- Closing it in the routes keeps the change small and local: the registry is a guard of fixed rule 3 and the adapter is the models lane's.

## What it costs

- A person who wants their own gateway's authority pastes the PEM text, or asks whoever runs the install to set the path.
- An entry that holds a path cannot be given a different path from the browser; it can be given PEM text or none.
