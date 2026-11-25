# 0122. An adapter retries only until the first event, calls a local model directly, and follows no redirect

November 24, 2026 · decided in P3-06

## The question

The item asks for retries with backoff. Routing (P3-08) will also retry and fall back, reading the seam error's kind. Which of them sends a call again, and when? And two smaller questions about where a request may go: through the environment's proxy, and after a redirect.

## The decision

**The adapter sends a call again only while nothing has reached the caller.** A refusal marked retryable, or a connection that failed, is tried again up to `chat_completions.max_attempts` times in all. The wait doubles from `chat_completions.retry_wait`, is shortened by up to half at random, and is capped at `chat_completions.retry_wait_max`. A longer `Retry-After` from the endpoint is honoured up to the same cap.

**Once the first event has been sent on, a failure is reported and not retried.** The error keeps its `retryable` mark, and the caller decides.

**The adapter never moves to another model.** That is routing's.

**An entry classed `local` is called directly.** The proxy in the environment is used for `hosted` and `trusted` entries only.

**A redirect is not followed.** It comes back as an error of kind `other`.

## Why

- A retry before the first event is invisible. The caller gets one stream, a little late. A retry after it would send a second `start`, or would have to hide that the text so far is being thrown away, and a person may already have read it. The turn loop knows what to do with a partial reply. The adapter does not.
- A busy endpoint usually clears in seconds, and the adapter is the only place that sees `Retry-After`. Leaving every retry to routing would mean routing has to know each provider's headers.
- The cap is short on purpose. A person is waiting. Past a few seconds, a fallback to another model is the better answer, and routing reads `rate_limited` and `overloaded` to make it.
- The random part keeps calls that failed together from returning together.
- `local` is the class that may read sensitive content because the model is on this machine or the home network. A proxy is another machine. Sending the content through it would quietly undo what the class promises. A hosted model is on the internet already, and an install behind a proxy needs it to reach one.
- A redirect sends the request body to an address nobody put in the registry. The standard client drops the `Authorization` header when the host changes, and still sends the content. The class was checked against the address in the entry, so that is the only address the content may go to.

## What it costs

- A reply that breaks off after one word is not retried by the adapter, though a retry would likely work. The turn loop or routing has to do it, and has to decide what to do with the word.
- Two layers can both retry. Three attempts here and a fallback chain in routing multiply. The settings are there to turn the adapter down to one attempt if routing's own retries prove enough.
- A local model that can only be reached through a proxy cannot be used as `local`. Marked `trusted`, it can, and that says what is true: the content leaves the home network's direct path.
- An endpoint that redirects, for example from a bare host to a path, needs the final address in the entry.
- The other two adapters keep their own retry loop for now. Three copies may drift. When all three exist, the integrator can lift the one they agree on.
