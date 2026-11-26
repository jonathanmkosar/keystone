# 0500. Dictation uses the browser's own speech recognition, and Keystone receives only the words

November 26, 2026 · decided in P10-01

## The question

Dictation turns speech into text in the composer. Something has to hear the audio and write the words: the browser, a service Keystone calls, or a transcriber on the owner's own machines. Where does the audio go, what does Keystone receive, and what must the server's headers allow?

## The decision

- **The browser's own recognizer**: `SpeechRecognition`, or `webkitSpeechRecognition` in Chrome, Edge and Safari, used continuously with interim results, in the language of `voice.language` or the browser's own. This is how New Keystone dictated (`keystone/web/voice.js`).
- **Where the audio goes.** In Chrome and Edge the browser sends the audio to its maker's speech service (Google's or Microsoft's) and gets the words back; Safari uses Apple's recognizer, on the device or Apple's service. Keystone never receives audio: the page receives the words from the browser, puts them in the composer, and they reach Keystone only when the person sends the message, like typed words. The button's tooltip and the `voice.dictation` setting's description say so, and `voice.dictation` off removes the button.
- **The header.** Every answer carries `Permissions-Policy: microphone=(self)`: Keystone's own pages may ask for the microphone, and no page of another origin framed in them may. The content security policy is unchanged, `default-src 'self'`: the recognizer is the browser's, and the page opens no connection for it. Artifacts are drawn in frames of no origin (decision 0422), which this policy gives no microphone.
- **A browser without it** (Firefox today), or a page on an address that is not secure, shows the button dimmed with the reason; it stays focusable and says why when pressed.
- **A seam for later.** The machine (`web/app/src/dictation.ts`) is given a `Recognizer`, the part of the browser's interface it uses: language, start, stop, abort, and start, result, error and end events. A local transcriber, for example a speech model on the owner's Mac mini behind the home-network gateway, would be another thing of that shape that records with `getUserMedia` and posts the audio to Keystone; none is built, and nothing else would change in the composer.

## Why

- It works today with no key, no model and no new package, in the browsers the owner uses, and New Keystone proved the flow.
- The person decides what is sent to a model, as with typed text: dictation only fills the box (decision 0502).
- A local transcriber keeps audio on the owner's machines, but needs a model served on the gateway, a route that takes audio, and a recording path in the page. It is a better privacy answer later, and voice mode (P10-02) chooses its provider in its own item.

## What it costs

- In Chrome and Edge, what the person says is heard by the browser maker's service. That is the browser's doing, under the browser's terms, and the person can switch the button off.
- Dictation needs the browser's service: offline, Chrome answers `network`, which the composer explains.
- Each browser recognizes differently: punctuation, capitals and how long it waits in silence are the browser's.
