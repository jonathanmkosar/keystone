# P10-06. Location

Status: provisional
Lane: interface + chats
Depends on: P4-04
Owner gate: none
Scope: Asked for by the owner on November 27, 2026 ("add GPS"), as he chose: per message. A "Use my location" control behind the composer's + asks the phone for its position once, and the location, rounded to about a kilometre, goes with that message only, so the model can answer weather and nearby questions.
Proof: A message sent with location carries the rounded position into the model's request and the record, a message without it carries none, and nothing reads the position without the person's tap.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (interface lane, November 27, 2026)

Branch `interface/p10-06-location`, cut from `reboot` at a0def61. No owner gate; provisional until the owner has used it on his phone.

1. **The message**: `location: {"latitude", "longitude"}` on the send body, rounded again by the server to `location.decimals` and refused while `location.share` is off, on a retry, or out of range; kept beside the message in a new table `message_locations` with the line the model is given; `location` on the message in the record and the stream, and in the inspector's reply. A field, not a block kind (decision 0600).
2. **The model's request**: one text block before the person's words in that message, from `location.line`, written once when the message is placed and sent the same in every later call, so the standing start and the history cache as before (decision 0601). No reverse geocoding.
3. **The header**: a second `Permissions-Policy` line, `geolocation=(self)`, beside `microphone=(self)`, with a test (decision 0602).
4. **The control**: `location.ts` (no imports: settings, `askPosition`, rounding, words) and `LocationControl.tsx` (the hook, the button beside the attach button or the `+` sheet's row, the chip over the box, the "Sent with location" mark, the inspector's section); asked only on a tap, once, at low accuracy (decision 0603).
5. **Settings**: `settings/location.json`: `location.share`, `location.decimals`, `location.timeout`, `location.line`.
6. **The proof**: Go tests in `internal/turn/location_test.go`, a header test, Node tests in `web/app/test/location.test.ts`, a standing check `location`, and a browser check at 375 pixels and desktop, light and dark, with an emulated position.
7. **The write-up**: web.md, turns.md, decisions 0600 to 0603, the journal, the status.

### Result (interface lane, November 27, 2026)

Provisional until the owner has used it on his phone: there is no mockup of the control, the chip or the mark, and no real phone or real position was used. Everything else is built and proven.

- **Built as planned**: the send body's `location` field, rounded again by the server and refused when it cannot be taken; `message_locations` (migration `20261127T150000_message_locations.sql`); `location` on the message in the record, the stream and the inspector's reply; the line, one text block before the person's words in that message, written once and sent the same in every later call; `Permissions-Policy: geolocation=(self)` as a second line beside the microphone's; `location.ts`, `LocationControl.tsx`, `location.css` and the `location` icon; `settings/location.json` with `location.share`, `location.decimals`, `location.timeout` and `location.line`. Decisions 0600 to 0603.
- **Changed from the plan**: none in substance. The browser check found that `inspectTurn` in `api.ts` passes on only the fields it knows, so the inspector's Location section did not show; it now passes `location` too.
- **The proof**: `internal/turn/location_test.go` (a located message carries the rounded line into the request, the audit log, the record and the inspector, and the next call sends it byte for byte with the standing start unchanged; a plain message's request and stored input are byte for byte as in a chat where no location was sent; the rounding; the server's rounding to the setting and its refusals; another tenant reads nothing; leaving an incognito chat deletes its location); `TestHandlerAllowsTheLocationToItsOwnOriginOnly`; 8 Node tests in `web/app/test/location.test.ts`; the standing check `location`; and a browser check at 1280 and 375 pixels, light and dark, with an emulated position (`docs/architecture/web.md`, "Location per message").
- `web/dist/` is not committed: `npm ci && npm run build` in `web/app`.
- See `docs/journal/2026-11-27-interface-012.md`.
