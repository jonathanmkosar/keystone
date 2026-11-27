# Fake provider

Folder: `internal/adapters/fake`

A scripted model. It is a `Provider` in the seam's terms ([seam.md](seam.md)), and it needs no network and no keys. It answers each request from a set of scripts, which are read from fixture files. The tests, `verify` and the test tenant use it in place of a real model, so every lane can build against a model that streams, thinks, calls tools and fails, on any machine.

It keeps no memory of earlier calls. The same request always gets the same stream.

## A script file

A script file is JSON. It holds one or more scripts.

```json
{
  "test_data": true,
  "scripts": [
    {
      "name": "ferry times",
      "when": { "user_text": "When does the north ferry leave on Tuesday?" },
      "steps": [
        {
          "reply": {
            "message": {
              "role": "assistant",
              "blocks": [
                { "type": "text", "text": "Let me look at the timetable." },
                { "type": "tool_call", "id": "call_1", "name": "lookup_timetable", "input": { "route": "north", "day": "Tuesday" } }
              ]
            },
            "stop_reason": "tool_call",
            "usage": { "input_tokens": 240, "output_tokens": 38 }
          }
        },
        {
          "reply": {
            "message": {
              "role": "assistant",
              "blocks": [ { "type": "text", "text": "The north ferry leaves at 09:00 and at 11:30 on Tuesday." } ]
            },
            "stop_reason": "end",
            "usage": { "input_tokens": 310, "output_tokens": 19 }
          }
        }
      ]
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `test_data` | Must be `true`. It marks the file as invented data, and a file without it is refused. |
| `scripts` | The scripts, at least one. |
| `name` | What the script is for. No two scripts share a name. |
| `when` | Which requests the script answers. See below. |
| `steps` | The model's replies, in order, at least one. |

## Which script answers a request

The fake looks at the last message the person wrote: the last user message that is not a set of tool results. A script's `when` lists conditions on the request, and every condition that is set must hold.

| Condition | Holds when |
| --- | --- |
| `user_text` | The text of that message is exactly this. |
| `user_text_contains` | The text of that message contains this. |
| `system_contains` | The request's system text contains this. |
| `model` | The request's model name is exactly this. |

The three text conditions ignore letter case and the spaces around the text. A `when` with nothing in it, `{}`, answers any request.

Since P5-03 the context builder puts recalled knowledge before the person's words in that message ([caching.md](caching.md)). So `user_text` also holds when the message's last text block, the person's own words, is exactly this. `user_text_contains` still looks in the whole text, recall and all.

When several scripts match, the one with the most conditions wins. A tie goes to the earlier script: by file name, then by place in the file. So a script for one exact question beats a script for anything else, wherever the two sit.

Most scripts need `user_text` and nothing more. `system_contains` tells a call for a title or a summary apart from the chat turn it is about. `model` gives one model a different answer, which is how a fallback is tested: one model's script fails and the next model's answers.

## Which step answers a request

The step is the number of assistant messages since the person's last message, plus one. The first call of a turn holds none, so it gets step 1. When step 1 asks for a tool, the tool loop adds the reply and the tool results to the request and calls again. That request holds one assistant message since the person's message, so it gets step 2.

Nothing is remembered between calls. The request alone says where the script is, so a turn that is retried, branched or resumed after a restart gets the right step.

## What a step sends

A step is one of three things.

| Step holds | What happens |
| --- | --- |
| `reply` | The whole reply is streamed, and the stream ends with `stop`. |
| `error` | The call fails before anything is sent. The stream is the one `error` event. This is a provider that refuses the call: a bad key, a rate limit, an overloaded service. |
| `reply` and `cut_after` | The reply breaks off after `cut_after` characters of its text, thinking and tool input, and the stream ends with an `error` event. With no `error` in the step, the error is the one a dropped connection gives: `unavailable`, retryable, "the stream ended before its stop event". With an `error`, it is that one. |

A `reply` is a `Response` in the seam's own JSON: the assistant message with its text, thinking and tool call blocks, the stop reason and the usage. `error` is the seam's `Error`: a kind, a message and `retryable`. Both are checked with the seam's own `Validate` functions.

How a reply is streamed:

- Text, thinking and tool input are sent in pieces of `fake.delta_size` characters. A thinking block's signature is sent whole.
- A reply with no `model` takes the model named in the request.
- A tool call keeps the `id` the script gives it. If the request already holds that id, as it does when a script is used twice in one chat, the fake adds `-2`, `-3` and so on, so the next request is still valid.
- A tool call's `input` is sent in its shortest JSON form, however the file lays it out.
- The usage is sent as written. The fake counts nothing.

## Requests it refuses

Each of these ends the call with an `error` event of kind `invalid`, as a real provider would. The message starts with `fake provider:` and gives the reason.

- The request fails the seam's `ValidateRequest`.
- The last message is the assistant's.
- A tool call has no result in the message after it. Every real provider refuses this, so the fake does too.
- No script matches. Add a script, or a catch-all with an empty `when`.
- The script has no step for the place the request has reached.

## Scripts it refuses

`Decode`, `Load` and `New` refuse a script, and say why, when:

- the file is not marked `"test_data": true`, has a field the format does not know, or holds no scripts;
- a script has no name or no steps, or two scripts share a name;
- two scripts have the same conditions, because the second could never be chosen;
- a reply or an error is not valid in the seam's terms;
- a step has neither a reply nor an error, or has both and no `cut_after`;
- `cut_after` is negative, or is not less than the number of characters the reply sends;
- a step can never be reached, because the step before it fails or does not stop for a tool call.

A script may end on a step that asks for a tool. Calling it again is refused as above, which is one way to test a step limit.

## Fixtures

`fixtures/fake-provider/` holds example scripts: a plain answer, a reply that stops at the output limit, a catch-all, thinking, a tool call with its continuation, two tool calls at once, and four failures. They are invented, and they are the reference for anyone writing a script by hand.

The test tenant (P3-09) keeps its recorded turns as script files in the same format, in its own folder.

## Settings

In `settings/models.json`.

| Key | Default | Meaning |
| --- | --- | --- |
| `fake.delta_size` | `12` | How many characters go in each piece. Zero sends each block in one piece. |
| `fake.delta_delay` | `20ms` | How long the fake waits before each piece, so a chat in the test tenant streams at a speed a person can watch. |

`DefaultOptions` reads both from the registry. Tests pass their own `Options`, with no wait. Reading a tenant's own values comes with tenant settings (P2-04) and the registry (P3-02).

Since P6-08, `fake.embedding_size` (`1024`, from 1 to 65536) is the size of the fake's vectors, which `DefaultOptions` reads too.

## Vectors

Since P6-08 the fake is an embedding model too: `Provider.Embed` answers a `seam.EmbedRequest` ([embeddings.md](embeddings.md)). Its vectors are deterministic and need no network:

- A text's words are lower-cased, and a word with a plural or verb ending (`s`, `es`, `ies`, `ed`, `ing`) is the same word without it.
- A word in one of `Options.Meanings`, groups of words the fake takes to mean one thing, stands for its group; any other word stands for itself.
- Each distinct word or group is hashed (FNV-1a) to one place of the vector, with a sign, and adds to it once: a group 1, another word a quarter, so a text's few known meanings stay ahead of its many other words.
- The vector is made unit length, so the dot product of two vectors is their cosine. A text with no word points at one fixed place.

So a question finds a page that shares a meaning group with it and no word. The groups are test data: the `embeddings` check gives the fake those in `fixtures/test-tenant/knowledge/meaning.json`. A fake with `EmbeddingSize` 0 makes no vectors and refuses, as a model that makes none would; a size out of range, a group of one word, a word in two groups and a word that is not letters and digits are refused by `New`. The usage it reports is a count of the texts' words.

## Decisions

Since P14-02 the fake is a decision model too: `Provider.Decide` answers a `seam.DecisionRequest` from the scripts whose `when` has `"decision": true` (decision [0634](decisions/0634-the-fake-answers-decisions-from-scripts-marked-decision.md), [decision-calls.md](decision-calls.md)).

    {"name": "decision: an easy question stays local",
     "when": {"decision": true, "user_text_contains": "what day is it"},
     "steps": [{"decision": {"answers": [
       {"question": "step", "choice": "house-chat", "probabilities": {"house-chat": 0.91, "small-hosted": 0.07, "mid-hosted": 0.02}}],
       "usage": {"input_tokens": 212, "output_tokens": 2}}}]}

- A decision script answers decision calls only, and a script without the mark answers turns only: the catch-all answers no decision.
- The state is matched as the user text (`user_text` and `user_text_contains`), the questions' instructions, one to a line, as the system text (`system_contains`), and `model` as for a turn.
- It has one step, a `decision` or an `error`. A decision with no model takes the request's. It must answer the request it is played for, one answer for each question with a probability for every option, or the call is refused as `invalid`.
- Read-time refusals: two steps, a reply or a cut, a decision and an error together, a decision that answers nothing, and a `decision` step in a script without the mark.

`fixtures/fake-provider/decisions.json` holds the examples: a sure and an unsure verdict, a yes or no matched by its instructions, and a decision model that cannot be reached.

## In code

```go
scripts, err := fake.Load(os.DirFS("fixtures/fake-provider")) // or fake.Decode(raw) for one file
p, err := fake.New(scripts, fake.Options{DeltaSize: 12})
reply, err := seam.Call(ctx, p, request)                      // or p.Stream(ctx, request, emit)
```

`Load` reads every `.json` file at the top of a folder, in order of file name. A `Provider` holds nothing that changes, so one can serve every call at once.

## Checked by

`go test ./internal/adapters/fake`. Text arrives in several pieces. Thinking arrives before the answer, with its signature whole. A tool call stops the reply, and the next step plays when the result comes back. An error ends a call before anything is sent, and a cut ends it part of the way through with the text so far kept. Every kind of bad script and bad file is refused with its reason. Every stream folds back to its scripted reply through the seam's collector. A call stops when its context is cancelled or its receiver fails. Every example script in `fixtures/fake-provider/` loads and plays to the end it scripts. Since P14-02, each decision script of the examples answers with each option's probability, an unscripted decision and an answer that does not fit the questions are refused, and a decision script never answers a turn.

The `offline` check in `verify` ([tools.md](tools.md)) proves this package imports no network package.

## Not here yet

- The fake is not wired into the binary. The model registry names it as an adapter, and `adapters.Fake` gives the builder for it ([models.md](models.md)). The test tenant (P3-09) loads its scripts.
- A script that fails once and then works. That needs memory of earlier calls, and nothing needs it yet: a fallback goes to another model, which `model` covers.
- Conditions on the tools offered or on what a tool returned.
