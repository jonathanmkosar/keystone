# 0502. Dictation fills the box at the cursor and never sends; Send and Enter while it listens finish the transcript

November 26, 2026 · decided in P10-01

## The question

Spoken words are recognized in pieces: interim guesses that change, then final phrases. Where do they go in the composer, what happens to what the person already typed, and what do Enter, the send button, Escape, silence and typing do while the microphone is on? The item asks that nothing is ever sent without the person pressing send.

## The decision

- **Final phrases go into the box as they arrive, at the cursor**, where the caret was when dictation started, each after the one before, with a space between them and the text around them. Nothing in the box is removed or replaced: a selection is left as it is and the words go after it. The caret is put after the words and the focus left in the box.
- **Interim words are shown apart**, in italics under the box, never in it, when `voice.interim` is on.
- **No path sends.** The machine is given no way to send. The composer's `send`, which Enter and the send button both call, first asks `dictation.holdsSend()`: while dictation listens it finishes the transcript instead and nothing is sent; while it finishes, nothing happens. Once dictation has stopped, the box is the person's: the next Enter or Send sends it. This is New Keystone's review-before-send flow.
- **Ways it stops:**

| What | How it stops | The words still being heard | What it says |
| --- | --- | --- | --- |
| The microphone again, Enter, Send, `voice.max_listen`, the page hidden | Stops listening and waits up to `voice.finish_wait` for the last words | Kept, when they do not become final in time | "Review the words before sending." |
| Escape | At once | Put in the box as they stand | "Stopped listening. Review the words before sending." |
| The person types in the box | At once | Left out: the person has taken over | "Stopped listening because you typed in the box." |
| The browser ends it (silence) or reports an error | At once | Kept | The review sentence, or the error's own sentence |

## Why

- Inserting at the cursor is what typing does, so the person's draft keeps its shape, and words dictated after a sentence follow it.
- Showing interim words in the box and then changing them would move the person's text under them; apart, they are plainly not yet theirs.
- Making Send and Enter finish rather than send means a person who speaks and presses Enter, as they would after typing, gets to read the words first, which is the item's point. Holding the send entirely would leave them unsure why Enter did nothing; finishing tells them.
- Escape keeps what was heard: losing spoken words is worse than a guess the person reads before sending anyway.

## What it costs

- A person who means to send at once presses Enter twice.
- Typing while the microphone is on stops it; dictating and typing at the same time is not possible.
- A final phrase lands in the box while the person may be reading it, which moves the caret to after it.
