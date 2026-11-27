# 0602. Keystone's own pages may ask for the position, by a second Permissions-Policy line beside the microphone's

November 27, 2026 · decided in P10-06

## The question

Dictation (decision 0500) made the server send `Permissions-Policy: microphone=(self)`. The location control needs the browser's geolocation on Keystone's own pages, and on no other origin's. How is it added, changing nothing else in the headers?

## The decision

- `cmd/keystone/serve.go` keeps its line `h.Set("Permissions-Policy", "microphone=(self)")` and adds one after it: `h.Add("Permissions-Policy", "geolocation=(self)")`. Every answer, the page, its files and the API, carries both lines.
- A browser reads a header sent on two lines as their values joined with a comma, so the policy it applies is `microphone=(self), geolocation=(self)`.
- The content security policy and every other header are unchanged. `TestHandlerAllowsTheLocationToItsOwnOriginOnly` checks the joined policy and that each other header has its one value; the dictation test, which reads the first line, still finds `microphone=(self)`; and the `location` standing check reads the line in the source.

## Why

- The policy as one value would change the dictation check's line in `tools/verify`, which this item may only add to. Two lines are the same policy to a browser (HTTP's rule for a header that is a list) and leave the microphone's line, its test and its check exactly as they were.
- `(self)` is the narrowest grant that works: the page itself may ask, and a frame from another origin, such as the sandboxed diagram frame, may not.

## What it costs

- Two lines where one would do; a proxy that keeps only the first line of a header would drop the position's grant. A top-level page has geolocation by default, so the control would still work there; only the explicit grant would be lost.
