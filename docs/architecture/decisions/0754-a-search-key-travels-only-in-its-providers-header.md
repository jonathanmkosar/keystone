# 0754. A search key travels only in its provider's own header, and no redirect is followed

November 28, 2026 · decided in P7-07

## The question

Brave, Exa and Serper take their keys in headers of their own: `X-Subscription-Token`, `x-api-key`, `X-API-KEY`. Go's HTTP client drops `Authorization` and cookies when a redirect goes to another host, but carries every other header. How do the new adapters keep a key from reaching anything but its provider?

## The decision

- **One sender for the four adapters** (`internal/tools/web/send.go`). The key goes in the provider's header and nowhere else: never in the address, an error, a log line or a view.
- **No redirect is followed.** The sender's client answers a redirect with the redirect itself, which reads as the provider being unavailable, with its status.
- **Nothing of an error answer is shown.** An error answer's body is read, at most 16 KiB, only to compare the provider's own code or tag with known words (`SUBSCRIPTION_TOKEN_INVALID`, `NO_MORE_CREDITS`, a message about credits). The text of every failure is Keystone's own.
- **A good answer is read up to `agents.web_search_max_bytes`**, as Tavily's is, and the search's timeout is `agents.web_search_timeout`.
- `verify`'s `no keys in output` gains a surface, `Brave, Exa and Serper` (`tools/verify/searchkinds.go`): the stored value is the key of a person's own provider and an install provider of each kind, and a stand-in answers in each kind's shape with a result, a refusal, a limit, a failure and a result that repeats the key in every field.

## Why

- **A key that follows a redirect leaves its provider.** A provider's address is a setting or an entry's endpoint, and a redirect is the provider's to send; nothing a search needs comes from one.
- **One sender** keeps the rule in one place, and each adapter reads only its own statuses.

## What it costs

- A provider that moves its API behind a redirect stops answering until its endpoint is changed. The failure's words give the redirect's status.
- Tavily's adapter (P7-06) follows redirects; its key is in `Authorization`, which Go drops on a change of host. It is left as it is.
