# Token budget

Folder: `internal/budget`

Keeps a model call inside the model's context window. It answers two questions: how many tokens will this request use, and does it fit this model. A call that cannot fit is refused before it is sent, with an error that says by how many tokens.

The package keeps nothing, opens no connection and reads no store. It imports the seam ([seam.md](seam.md)), the registry's `Model` ([models.md](models.md)) and the settings registry.

## The numbers

A registry entry gives two: `ContextWindow`, the most tokens one call may hold, input and output together, and `OutputLimit`, the most the model writes in one reply.

A call fits when

    input tokens + room kept for the reply <= ContextWindow

The room kept for the reply is the request's `MaxOutputTokens`. When the request asks for nothing, or for more than the model writes, it is the entry's `OutputLimit`. Thinking is part of the output in the seam, so it needs no room of its own.

## Counting the input

**The provider's own count, where it offers one.** A provider offers one by also being a `budget.Counter`:

```go
type Counter interface {
	CountTokens(ctx context.Context, req seam.Request) (int, error)
}
```

It returns the input tokens of the request: system text, every message and every tool definition, as the provider would bill them, without the room for the reply. An adapter adds the method to the value its builder returns. It does not import this package, and nothing here changes when it does (decision [0130](decisions/0130-a-provider-counts-through-an-optional-interface.md)). A provider's count is used as it is, with no margin.

**An estimate with a safety margin, everywhere else.** That is a provider with no `CountTokens`, no provider at all, and a count that failed. Only a cancelled context is passed on as an error. The estimate is made this way (decision [0131](decisions/0131-the-estimate-counts-characters.md)):

| Part of the request | Counted as |
| --- | --- |
| Text, thinking, a tool call's name and input, a tool's name, description and schema | characters divided by `budget.chars_per_token`, rounded up |
| A thinking block's signature, ids | their length, as characters |
| Each message | `budget.message_tokens` more |
| Each tool offered | `budget.tool_tokens` more |
| An image, inline or by reference | `budget.image_tokens` |
| A file sent inline | its bytes as characters when its type is `text/...`, and otherwise its length as base64 |
| A file named by reference | `budget.ref_file_tokens`, since its size is not in the request |

Then `budget.margin_percent` of the sum is added, rounded up.

## What a turn calls

| Function | What it does |
| --- | --- |
| `FromSettings(reg, value)` | Reads the `Options` from the settings registry. `value` may be nil. When it is given, it is asked first for each key, which is how an install's or a tenant's stored value comes in. |
| `Check(ctx, model, provider, req, opts)` | Counts the request and measures it. Returns a `Fit` (window, input count, room kept for the reply, room left), and a `*TooLarge` when it does not fit. |
| `Guard(model, provider, opts)` | Wraps a provider so every `Stream` is checked first. A call that cannot fit ends with one `too_long` error event and is never sent. The wrapper is a `Counter` when the provider is one. |
| `InputRoom(model, maxOutput)` | How many input tokens a call to the model may hold. The number the context builder (P5-01) packs under. |
| `Tokens(ctx, provider, req, opts)` | The count alone: the provider's, or the estimate with its margin. `Count.Exact` says which. |
| `Estimate(req, opts)`, `Estimated(req, opts)` | Keystone's own count, without and with the margin. |
| `Measure(model, req, count)` | Checks a count the caller already has. |

A turn does this with what `adapters.Open` returned:

```go
opts, err := budget.FromSettings(reg, nil)
model, provider, err := adapters.Open(ctx, store, set, name)
provider = budget.Guard(model, provider, opts)
```

or calls `Check` itself when it wants the numbers, as the turn inspector will.

## The refusal

`*TooLarge` holds the `Fit` and `Over`, the number of tokens too many. `errors.Is(err, budget.ErrTooLarge)` is true for it. Its text reads:

    model house-small: the call needs 9000 tokens (6952 of input, estimated with a margin, and 2048 kept for the reply) and the context window is 8192: 808 too many

`SeamError()` gives the same refusal as a `seam.Error` of kind `too_long`, the kind a provider's own refusal has, so routing (P3-08) treats both alike. `Guard` returns that one.

## Settings

All in `settings/models.json`, scope `install`.

| Key | Default | What it is |
| --- | --- | --- |
| `budget.chars_per_token` | 3 | Characters of text that count as one token in an estimate. Lower is safer. |
| `budget.margin_percent` | 15 | The margin added to an estimate. |
| `budget.message_tokens` | 4 | Counted for each message. |
| `budget.tool_tokens` | 12 | Counted for each tool offered. |
| `budget.image_tokens` | 1600 | Counted for each image. |
| `budget.ref_file_tokens` | 4000 | Counted for a file named by reference. |

## The standing check

`context fit`, in `tools/verify/contextfit.go`. For every entry in `fixtures/models/examples.json`, with the shipped settings: an estimated request under the window is accepted and one over it is refused with the number over; a provider's count that fills the window exactly is accepted and one token more is refused as one over; and an entry that names the fake adapter is called through the fake provider behind `Guard`, where the request that fits is answered and the one that does not ends as `too_long`.

## Not here yet

- No real adapter counts yet. P3-04 to P3-06 add `CountTokens` where their provider has a count. Until then every model is estimated.
- Nothing calls `Check` or `Guard` yet. The chat turn (P4-03) and routing (P3-08) do.
- The plan's wording of the check is that the largest test chat is packed under every window. The packing is the context builder's (P5-01), which adds that step to this check. Today the check proves the refusal.
- A file named by reference is a flat number. When uploads (P9-02) keep a file's size, the estimate can use it.
- The settings are read at their defaults unless the caller passes stored values. An install's own stored values are not built (see [settings.md](settings.md)).
