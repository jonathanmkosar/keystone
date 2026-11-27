# 0603. The position is asked for on a tap, once, at low accuracy, and rounded in the page before anything holds it

November 27, 2026 · decided in P10-06

## The question

The composer's "Use my location" control asks the browser for the phone's position. When may it ask, how exactly, how long may it wait, and what does the person see when it cannot?

## The decision

- **Only on a tap.** `askPosition` in `location.ts` is the one call to `getCurrentPosition`, and `LocationControl.tsx` calls it once, in `tap`, which only a click on the control reaches. Nothing asks on load, on a timer or when a chat opens, and nothing watches the position (`watchPosition` is used nowhere). The `location` standing check reads the source for all three.
- **Once, at low accuracy, not cached**: `enableHighAccuracy: false`, since the position is rounded to about a kilometre anyway and a coarse fix is faster and lighter on the battery; `maximumAge: 0`, so the place is where the phone is now; `timeout` from `location.timeout` (10s), or no limit for 0s. The browser's own permission question, the first time, does not count against the wait.
- **Rounded in the page** to `location.decimals` before the control holds the position, and only the rounded latitude and longitude are sent. The server rounds again (decision 0600).
- **With one message.** The rounded position waits in a chip over the box, "Location: about 1 km", with a remove button, until the message is sent; the composer takes it with its one send, and the next message has none unless the control is tapped again. A second tap while the browser is asked stops waiting.
- **Plain words** for a refusal ("Keystone may not use your location…"), no position, a timeout, and a browser without geolocation. On a page that is not secure (plain http), the control is dimmed and says why; it never asks.
- **The hint says where it goes**: "Sends your approximate location (about 1 km) with this message only, to whichever model answers it, hosted models included." It is the desktop button's title and, on a phone, a line under the row in the `+` sheet, since a phone shows no title.
- **Behind a switch**: `location.share` (on). Off, the control is not drawn and the server refuses a located message.

## Why

- A tap is the person's consent for that message. Asking at any other time would read a position they did not choose to share.
- The pure part has no imports and takes the geolocation as an argument, so Node's own test runner proves the rounding, the words and the refusals with a stand-in, and no test or check ever reads a real position.

## What it costs

- One tap per message that should carry the location: there is no "always send it" switch. The owner chose per message.
- A coarse fix can be off by more than the rounding on a phone without GPS lock; the line says "approximate".
