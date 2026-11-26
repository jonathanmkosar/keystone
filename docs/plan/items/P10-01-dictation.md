# P10-01. Dictation

Status: provisional
Lane: interface
Depends on: P4-04
Owner gate: none
Scope: Speech into the composer for review before sending, borrowing New Keystone's voice code.
Proof: Dictated text lands in the composer and is never sent without the person pressing send.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (interface lane, November 26, 2026)

Branch `interface/p10-01-dictation`, cut from `reboot` at a3d2864. The chat screen it depends on (P4-04) is built and merged; its only open part is the mockups' approval, so the integrator decided to build this now. New code goes in new files; every line changed in a shared file is listed in the journal.

1. **The machine, a new `web/app/src/dictation.ts`** with no imports, so Node can run it as it is: a controller made from a recognizer factory, a clock, the settings and one outward call, `insert(words)`. Phases `idle`, `listening` (with "waiting for the microphone" until the browser says it started) and `finishing`. A final phrase is inserted as it arrives; the words still being heard are shown apart, never in the box. `finish` (the button, Enter, the send button) stops listening and waits for the last words up to `voice.finish_wait`; `stop` (Escape) stops at once and keeps what was heard; the person typing in the box stops listening and keeps the box as they left it; the browser ending, an error or `voice.max_listen` ends it too. Nothing in it can send: it is given no send, and `holdsSend` makes the composer's send action finish the transcript instead while dictation runs, as New Keystone's did. A plain message for each browser error (no microphone, permission refused, no speech, language not supported, the service unreachable or blocked), and a reason when the browser has no speech recognition or the address is not secure. Also the pure helpers: inserting words at a point without removing anything, reading the settings and Go durations.
2. **The screen, a new `web/app/src/Dictation.tsx` and `dictation.css`**: `useDictation` wires the browser's `SpeechRecognition` (or `webkitSpeechRecognition`) to the machine, keeps the insertion point at the cursor, puts the caret after the words and the focus in the box, and reads the settings through `GET /api/v1/settings`; `DictationButton` (a microphone, `aria-pressed`, a label for each state, dimmed with its reason when not available) just before the send button; `DictationStatus` (a dot and the state in words, the words still being heard, the message) under the text. In `Composer.tsx`: the hook, the button, the status line, `holdsSend` at the head of `send`, and Escape. A `mic` icon in `Icon.tsx`. Built from the tokens; the button's place and look are provisional on the mockups, which do not draw it.
3. **Settings, a new `settings/voice.json`**: `voice.dictation` (the button shown), `voice.language` (empty: the browser's own), `voice.interim` (the words still being heard shown), `voice.max_listen` (2m) and `voice.finish_wait` (5s, install).
4. **Headers**: `Permissions-Policy: microphone=(self)` on every answer in `cmd/keystone/serve.go` (one added line) with a test; the content security policy unchanged. A decision record on where the audio goes: in Chrome and Edge the browser sends it to its maker's speech service, and Keystone receives only the words. A local transcriber (the Mac mini) is a later option behind the recognizer seam, not built.
5. **The proof**: unit tests of the machine with a fake recognizer and a fake clock in `web/app/test/dictation.test.ts`, run by Node's own test runner (`npm test`, no new package; a decision record): dictated words land in the box at the cursor, in order, without removing what was there; no send happens on stop, on silence, on an error, at the time limit or on Enter during listening, and the person's send then sends the box. A standing check, `dictation`, in a new `tools/verify/dictation.go` (one line in `main.go`'s list): it runs those tests when Node is present, and always checks the source (the dictation files name no send and no network call, the composer holds its send for dictation before `onSend`), the header and the settings.
6. **The write-up**: `docs/architecture/dictation.md` with its `componentDocs` line and overview row, decisions from 0500, the journal, the status.

Left for later: voice mode (P10-02); a local transcriber; dictation in other boxes than the composer.

### Result (interface lane, November 26, 2026)

Provisional on the mockups for the microphone button only: the chat mockups (P1-04) do not draw it, so its place and look are built from the tokens. The machine, the settings, the header and the proof are complete.

- Built as planned: `dictation.ts` (the machine, no imports), `DictationControls.tsx` (the hook, the button and the status line), `dictation.css`, the `mic` icon, and in `Composer.tsx` the hook, the two components, `holdsSend` at the head of `send`, and Escape. `settings/voice.json` with `voice.dictation`, `voice.language`, `voice.interim`, `voice.max_listen` and `voice.finish_wait`. `Permissions-Policy: microphone=(self)` in `cmd/keystone/serve.go`. Decisions 0500 to 0502.
- Changed from the plan: the React part is `DictationControls.tsx`, not `Dictation.tsx`, because Windows does not tell `Dictation.tsx` and `dictation.ts` apart and `./Dictation` would have found the machine.
- The proof: 15 tests of the machine with a fake recognizer and clock, run by Node's own test runner with no package (`npm test` in `web/app`): dictated words land in the box at the cursor, in order, removing nothing; Enter or Send during listening, silence, an error, the time limit and the finishing wait each end with the words in the box and nothing sent, and the person's next send sends them. With `holdsSend` made to answer false, the send test fails. A new standing check, `dictation`, runs them when Node is present and always reads the source, the header and the settings; `verify` is 36 of 36. In the desktop app's browser pane, with a stand-in recognizer in the page, Enter during listening sent no turn and the next Enter sent the box. No real microphone was used.
- `web/dist/` is not committed: `npm ci && npm run build` in `web/app`.
- See `docs/journal/2026-11-26-interface-008.md` and `docs/architecture/dictation.md`.

