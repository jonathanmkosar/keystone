# 0501. The dictation machine is tested by Node's own test runner, with no new package

November 26, 2026 · decided in P10-01

## The question

The item's proof, that dictated text lands in the composer and is never sent without the person's send action, is about code in the web interface. `web/app` had no test setup. A test runner is a package (Vitest, Jest) and a decision; a check of the built bundle from Go cannot run the code. How is it proven, and how does `verify` keep it proven?

## The decision

- **The machine is a file with no imports**, `web/app/src/dictation.ts`: the phases, the handling of the recognizer's events, the timers, the messages, inserting words at a point, and reading the settings. The browser's recognizer, the clock and the composer's insert are given to it. The React part, `DictationControls.tsx`, only joins it to the page.
- **Node's own test runner** runs `web/app/test/dictation.test.ts` against it with a fake recognizer and a fake clock: `npm test` in `web/app`, which is `node --experimental-strip-types --test test/dictation.test.ts`. Node 22.6 and later read TypeScript by removing its types, so the file is loaded as it is, with no compiler and no package. The test file is outside `src`, so `tsc` and the build do not see it, and it imports `../src/dictation.ts` with its extension, as Node needs.
- **The `dictation` check in `verify`** runs those tests when `node` is on the path, and fails on a Node older than 22.6. Without Node it runs only its source checks, which always run: the dictation files name no send and no network call, the machine imports nothing, the composer's `send` asks `dictation.holdsSend()` before its one call of `onSend`, the server's two headers, and the five settings.

## Why

- No package is added, so nothing new comes from the network and nothing needs updating.
- The machine is where the rules are, and it is tested whole; the composer's part that could send is one line, which the check reads.
- Keystone still builds with Go alone (decision 0005); only someone changing the interface needs Node, and every machine that has built the interface so far has it.

## What it costs

- Without Node, `verify` says `ok` for `dictation` having run the source checks only. A machine with Go alone proves less.
- The test file is not type-checked: Node removes types without checking them. The machine it tests is checked by `tsc` as part of `src`.
- The machine is written in TypeScript that Node can strip: no `enum`, no `namespace`, no parameter properties.
- The React part (the caret, the focus, the settings read) is not unit-tested; it was checked in the browser pane with a stand-in recognizer.
