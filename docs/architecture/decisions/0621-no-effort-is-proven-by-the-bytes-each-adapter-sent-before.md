# 0621. A request with no effort is proven unchanged by the bytes each adapter sent before effort existed

November 27, 2026 · decided in P14-01

## The question

The item says a request with no effort is sent exactly as before. The recorded exchanges compare the body as JSON, which would not see a field moved, a value written another way, or keys in another order. How is "exactly" proven?

## The decision

- Before any adapter code changed, the exact body each of the three adapters built for every recorded request was written to `fixtures/<adapter>/golden/bodies.json`, in a commit of its own: 37 Anthropic bodies, 21 Responses bodies and 5 Chat Completions bodies.
- A test in each adapter, `golden_test.go`, builds the body of every recorded request with no effort and compares it with the stored one, byte for byte. It runs with the effort settings at their widest: every model on `anthropic.effort_models`, and `chat_completions.send_reasoning_effort` on.
- Every stored body must still be built, so a recorded request cannot drop out unseen. A new exchange need not be added to the file. `KEYSTONE_WRITE_GOLDEN=1` writes the file again, for a later change to the wire that is meant.
- The new wire fields sit apart from the old ones in each request struct, after a blank line, with `omitempty`, so gofmt leaves the old lines as they were and a request with no effort cannot carry them.

## Why

- **Bytes are what the provider and its cache see.** A prompt cache keys on the exact prefix; a change in the bytes of a request that did not ask for one is a change in cost.
- **Written before, not after.** A file written after the change would only prove the code agrees with itself. The commit order shows it was written first.

## What it costs

- A change to the wire that is meant needs the file written again, and the reviewer has to see why the bytes changed.
