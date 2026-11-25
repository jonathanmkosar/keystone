# Fake provider scripts

Test data. Everything in this folder is invented: the questions, the answers, the tools and the numbers. Every script file says so itself with `"test_data": true`, and the fake provider refuses a file that does not.

These are the example scripts for the fake provider, the scripted model that stands in for a real one in tests, in `verify` and in the test tenant. The format is described in [`docs/architecture/fake-provider.md`](../../docs/architecture/fake-provider.md).

| File | Shows |
| --- | --- |
| `plain-answer.json` | A text answer, a reply that stops at the output limit, and a catch-all that answers any message no other script matches. |
| `thinking.json` | A reply that thinks before it answers. |
| `tool-call.json` | A tool call and the answer that follows its result, and two tool calls in one reply. |
| `failures.json` | A provider that is busy, a key that is refused, a stream that ends early, and a rate limit part of the way through. Each answers one invented model name, so a fallback to another model can be tested. |

## Writing a script

1. Copy the script nearest to what you need.
2. Give it a `name` no other script has.
3. Put the person's message in `when.user_text`.
4. Write one step for each call the model gets in the turn. A step that asks for a tool ends with `"stop_reason": "tool_call"`, and the next step is what the model says when the result comes back.
5. Run `go test ./internal/adapters/fake`. A script that is not valid is refused with the reason.

Use invented content only. Nothing here may be a real name, address, account or key.
