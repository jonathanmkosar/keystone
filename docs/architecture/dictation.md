# Dictation

Files: `web/app/src/dictation.ts`, `web/app/src/DictationControls.tsx`, `web/app/src/dictation.css`, `web/app/test/dictation.test.ts`

P10-01. Speech into the composer, for the person to review before sending. Borrowed from New Keystone's `keystone/web/voice.js`: the browser's own speech recognition, the review-before-send flow, the wait for the last words, and the wording of each error. Voice mode is P10-02.

## What a person sees

- **The microphone**, just before the send button in every composer (new chat, a chat, a project's page). Its label says what it will do: "Dictate a message", "Stop dictation", "Finishing the transcript". Listening, it is filled in copper and pulses; finishing, it is in the soft copper and waits. Its tooltip says where the audio goes.
- **A line under the text** while it works: a dot (grey until the browser says it has started, then copper and pulsing), what it is doing ("Waiting for the microphone…", "Listening. Enter or the microphone finishes, Escape stops.", "Finishing the transcript…"), and, in italics, the words still being heard. After it stops, the line says why, in the error colour for an error. The line is a polite live region, so a screen reader hears the state and the reasons, not every interim word.
- **The words** go into the box at the cursor, phrase by phrase, and nothing is sent: see decision [0502](decisions/0502-dictation-fills-the-box-and-never-sends.md) for each way it stops.
- **When it cannot**: in a browser with no speech recognition (Firefox today) or on an address that is not secure, the button is dimmed and still focusable, its tooltip gives the reason, and pressing it says the reason on the line. With `voice.dictation` off, or when the settings cannot be read, there is no button.

### What it says

| When | Sentence |
| --- | --- |
| No speech recognition in the browser | This browser cannot turn speech into text. Try your keyboard's own dictation, or type your message. |
| Not a secure address | Dictation needs a secure address: https, or this computer's own. |
| `not-allowed` | Microphone permission was refused. Allow the microphone for this site in the browser, then try again. |
| `audio-capture` | No microphone is available. |
| `no-speech` with nothing heard | No speech heard. Try the microphone again. |
| `language-not-supported` | This browser cannot transcribe the language “xx”. Change voice.language on Settings → General. |
| `network` | The speech service could not be reached. |
| `service-not-allowed` | This browser blocked speech recognition. |
| Any other code | Listening stopped. |
| The recognizer will not start | Could not start listening. |
| Stopped, with words heard | Review the words before sending. |
| The time limit | Stopped after 2 minutes, the longest dictation listens at one go. Review the words before sending. |
| No last words in time, none heard | Speech did not finish. Try dictation again or type your message. |

An error after words were heard adds "Review the words before sending."

## How it is built

| Part | File | What it does |
| --- | --- | --- |
| The machine | `src/dictation.ts` | `createDictation`: the phases `idle`, `listening` and `finishing`, the recognizer's events, the time limit and the finishing wait, the messages, `holdsSend`. `insertAt`, `readVoiceSettings`, `durationMs`. No imports: it is given the recognizer factory, a clock, the settings, the browser's language, `insert` and `changed`, and calls out through those two alone. |
| The screen | `src/DictationControls.tsx` | `useDictation(area, text, setText)`: makes the machine with the browser's recognizer and `setTimeout`, reads the settings, keeps the insertion point, puts the caret after inserted words and the focus in the box, stops on a change of the text it did not make, and finishes when the page is hidden. `DictationButton` and `DictationStatus`. Named apart from `dictation.ts`, since Windows does not tell the two apart by case. |
| The look | `src/dictation.css` | The button's states, the line, the dot, the interim words. Tokens only; no motion with reduced motion. |
| The icon | `src/Icon.tsx` | `mic`. |
| The way in | `src/Composer.tsx` | The hook, `DictationStatus` under the text, `DictationButton` before the send button, `holdsSend` at the head of `send`, and Escape in the box. |

The machine's `Recognizer` is the part of the browser's `SpeechRecognition` it uses. It is the seam a local transcriber would fill (decision [0500](decisions/0500-dictation-uses-the-browsers-speech-recognition.md)); none is built.

The recognizer is always asked for interim results, so Escape and the finishing wait can keep the words still being heard; `voice.interim` decides only whether they are shown.

## Where the audio goes

To the browser. In Chrome and Edge, the browser sends it to its maker's speech service; Safari uses Apple's recognizer. Keystone receives no audio: the words reach it only in a message the person sends. Every answer of the server carries `Permissions-Policy: microphone=(self)` (`cmd/keystone/serve.go`), so only Keystone's own pages may ask for the microphone; the content security policy is unchanged. Decision [0500](decisions/0500-dictation-uses-the-browsers-speech-recognition.md).

## Settings

In `settings/voice.json`, lane `voice`, read by the screen through `GET /api/v1/settings` each time a composer opens. The four a person may change are drawn on Settings → General.

| Key | Default | Scope | What it does |
| --- | --- | --- | --- |
| `voice.dictation` | true | tenant | The microphone button is drawn. |
| `voice.language` | empty | tenant | The language tag dictation listens for; empty is the browser's own. |
| `voice.interim` | true | tenant | The words still being heard are shown under the box. |
| `voice.max_listen` | 2m | tenant | The longest one go listens. 0s: no limit but the browser's. |
| `voice.finish_wait` | 5s | install | How long it waits for the last words after it is stopped. |

## Checked by

- `web/app/test/dictation.test.ts`, run by `npm test` in `web/app` with Node's own test runner and no package (decision [0501](decisions/0501-the-web-machine-is-tested-by-nodes-own-test-runner.md)), with a fake recognizer and clock and a composer that, like `Composer.tsx`, asks `holdsSend` before it sends: words land at the cursor in order and remove nothing; Enter or Send during listening, silence, an error, the time limit and the finishing wait each end with the words in the box and nothing sent, and the person's next send sends them; Escape keeps the words still being heard; typing stops it; each error's sentence; a browser without recognition or an insecure page cannot start; the language and interim settings; settings and Go durations read from the route's list.
- `TestHandlerAllowsTheMicrophoneToItsOwnOriginOnly` in `cmd/keystone`.
- The `dictation` check in `verify`: the tests above when Node is present, and always the source: no send and no network call in the dictation files, no import in the machine, `holdsSend` before the one `onSend` in `Composer.tsx`, the two headers, and the five settings.
- In the desktop app's browser pane on November 26, 2026, against a server built from the branch on port 7983, as the test tenant, with a stand-in recognizer put in the page in place of the browser's: the words appended after typed text, the interim words under the box, Enter during listening finishing with no turn sent, the caret after the words and the focus in the box, the next Enter sending; Escape keeping the words still being heard, in dark at phone width; the dimmed button and its reason without speech recognition. No real microphone was used.

## Not here yet

- A local transcriber (decision 0500).
- Dictation into other boxes than the composer.
- Voice mode, spoken replies (P10-02).
- Seen with a real microphone and a real speech service.
