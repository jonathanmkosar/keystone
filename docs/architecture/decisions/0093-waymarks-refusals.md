# 0093. What each Waymarks refusal becomes

November 25, 2026 · decided in P6-03

## The question

The interface has five errors. Waymarks answers with HTTP statuses and codes of its own, and sometimes the thing at the address is not Waymarks at all. Which becomes which? The choice matters most for `ErrUnavailable`, which tells a chat to carry on without the store, and for `ErrNotFound`, which tells a caller to skip one page.

## The decision

- **No connection, no answer inside the timeout, a redirect, an answer that is not Waymarks' JSON or is too large, a 401, a 5xx: `ErrUnavailable`.** A refused token is a store this handle cannot use, and a chat should carry on.
- **A store that is not set up is `ErrUnavailable` from `Open`**, naming the setting or the secret that is missing.
- **403 for a call that names a page or a source: `ErrNotFound`.** Waymarks does not hide whether a page exists: it answers 403 for one in a space the grant does not reach. A page may cite a source in such a space. For this handle that source is not there, and the caller should skip it and go on.
- **403 for a search that names a space: no hits.** The interface says a search of a space the store does not hold finds nothing.
- **403 for anything else: `ErrUnavailable`.**
- **400 and 413: `ErrInvalid`. 409 `stale`: `ErrStale`. 404 and 410 for a page or a source: `ErrNotFound`.**
- **No redirect is followed.**
- **Nothing is retried, and no failure is remembered.** The next call tries again.

## Why

- A caller acts on the kind of error. "Carry on without the store" and "skip this one" are the two things it can do, and each refusal is sorted by which of them is right.
- A redirect would send a token to an address the tenant did not set.
- Retrying inside the adapter would stretch the timeout the tenant set. Whether to try again belongs to the caller, which knows whether a person is waiting.
- Remembering a failure would keep a store that came back out of use.

## What it costs

- A token that Waymarks suspended answers 403 everywhere. Reads by id then say "not found" where "unavailable" would be truer. The catalog and an unnamed search still say unavailable, and recall asks those first.
- With no memory of a failure, a turn that makes several calls to a dead instance waits for each. With the default timeout, a closed port fails at once and a silent host costs five seconds a call. P6-05 should stop asking for the rest of a turn after the first `ErrUnavailable`.
