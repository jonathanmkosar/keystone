# 0541. Charts are drawn by the app's own code, and diagrams in a frame of no origin

November 26, 2026 · decided in P9-04

## The question

A reply may hold a chart and a diagram, and the item's rule is that nothing a model writes runs in the app's own page, whose policy is `default-src 'self'` and must not loosen. A chart could be drawn by a library or by hand; a diagram is Mermaid source, and Mermaid turns text into SVG with a parser megabytes large. Where does each run?

## The decision

**They are drawn in two different places, because they are two different kinds of thing.**

- **A chart is data, and the app draws it.** `chart.ts` checks the block (decision [0540](0540-a-chart-is-a-small-json-object-in-a-fenced-chart-block.md)) and lays it out as plain numbers and short texts; `InlineVisuals.tsx` builds SVG elements from that with React. No markup is ever set from text, so a label is only ever a text node, and no model-written script, style or colour exists. No chart library is added: `verify` refuses one by name.
- **A diagram is a program for Mermaid, and Mermaid never runs in the app's page.** It runs in a sandboxed frame of the artifact sandbox's kind (decision [0422](0422-the-sandbox-is-a-page-of-no-origin-under-its-own-policy.md)): a page of no origin, under a policy that fetches and sends nothing, at Mermaid's `strict` level.

**The diagram's page is the same for every diagram.** `GET /api/v1/visuals/diagram?theme=` (a new package, `internal/visuals`) serves a page that holds the Mermaid renderer the binary already has and no diagram. The app hands it the block's source and the theme's colours with `postMessage`, and the frame answers with one of three messages: `ready`, `drawn`, or `problem` with a text.

- **Its policy is the sandbox's, tightened.** `visuals.Policy` is `artifacts.Policy` for a Mermaid artifact with two changes: `script-src` names the page's one script by its SHA-256 digest in place of `'unsafe-inline'`, and the `sandbox` directive is `allow-scripts` alone. An artifact's page needs `'unsafe-inline'` because a model wrote its script; this page's script is Keystone's and nothing else, so even markup that slipped through Mermaid's own cleaning could run nothing.
- **The frame's permissions are fixed** at `allow-scripts`, in the page (`frameSandbox`) and in the server's policy (`visuals.FramePermissions`). It is not the `artifacts.sandbox` setting: that setting is about scripts a model wrote, and here none run. Whether diagrams draw at all is `visuals.draw`.
- **Each side reads only what it expects.** The frame hears only its parent (`e.source === window.parent`) and reads a draw request through `readDrawMessage`, which keeps a source text, a theme of two, and thirteen colours that must each be a hex colour. The page hears only that diagram's own frame (`e.source === frame.contentWindow`) and reads through `readFrameMessage`: one of three kinds, and for a problem a text cut to 600 characters, shown as text. A frame cannot ask the page for anything.
- **The renderer is the artifact sandbox's one file.** `sandbox/mermaid.ts` gains the waiting mode (the page's body says `data-ks-inline`), so the binary holds Mermaid once. A build whose renderer does not know the messages serves a page that says so and runs no script.
- **The route needs a session**, like every route but the doors, though the page holds nothing of any person.

## Why

- **Drawing charts in the app** makes them part of the page: they follow the theme through the tokens, lay themselves out at the real width so text stays 12px on a phone, cost nothing to load, and are reachable by a screen reader as an image with a sentence and a table. In a frame they would be a second document per chart with none of that.
- **The app's own SVG is enough** for five chart types: decision [0432](0432-the-usage-bars-are-inline-svg.md) already draws bars by hand. A chart library would be the largest thing on the page, would need its colours mapped to the tokens and a check that it loads nothing, and would bring a configuration language to keep a model out of.
- **Mermaid cannot be made safe by checking its input.** Its languages are many and its output is markup it builds itself; its `strict` level is a good guard and not one to rest the person's session on. The frame's missing origin is the guard: whatever Mermaid is talked into, it has no cookie, no storage, no API and no network.
- **One page for every diagram** means megabytes of renderer are sent once and kept by the browser (an ETag, asked each time), nothing of a reply is ever in an address or kept on the server, and a diagram draws while its reply is still being written, before any message is stored.
- **Not the artifact's own route**: that serves a stored version of an artifact, and a reply's block is not one. Saving a diagram as an artifact is a button (`POST /api/v1/chats/{id}/visuals/diagrams`), which makes an ordinary Mermaid artifact with versions, download and its own link.

## What it costs

- A frame per diagram, each reading megabytes of script. It is made only when the diagram comes near the window, and a diagram whose frame does not answer within `visuals.diagram_wait` is shown as its source with a note.
- `postMessage` to a frame of no origin cannot name its target, so the source is sent with `"*"`. What is in the frame can only be a page of this Keystone: the main page's `default-src 'self'` refuses the frame any other address.
- The diagram cannot use the app's fonts: the frame loads nothing, so its text is the system's sans.
- Two code paths for two kinds of visual, and a second small protocol to keep.
