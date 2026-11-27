# 0585. Browser use is off until each person switches it on, and opening and reading a page do not ask

November 27, 2026 · decided in P10-03

## The question

Who decides whether the agent may use a browser, and which of its six tools ask before they run?

## The decision

- **`agents.browser_use`, a switch each person sets for themselves, off as shipped.** With it off the six tools are not offered (`internal/turn/browser.go`), so a turn offers exactly what it offered before, and a call the model makes anyway is refused by the tool, which reads the same switch, before any browser starts. Settings draws the switch with the person's other agent settings, as it draws every setting of the `tenant` scope.
- **Opening a page, reading it, a screenshot and going back only read**, as `web_fetch` does, and run without asking under the default policy. A person can set any of them to ask on Settings → Approvals.
- **Clicking and typing change something**: never read-only, so they ask under the default policy, and always once the chat has read a page, whatever the person has set (fixed rule 2). Every browser tool's result is outside content, so the first page a chat opens marks it.
- **A chat held for sensitive content cannot use the browser**, as the web tools cannot (decision 0252): an address or a typed word would leave Keystone.

## Why

- **A browser is a bigger thing to allow than a fetch.** It runs a page's scripts, and its clicks and typing reach the world; a person should choose it, and the shipped install should behave exactly as before.
- **Reading asks nothing elsewhere.** A browser that asked before every page would be used for nothing; what a page can do on its own while being read is held by decision 0581.

## What it costs

- An address the model opens can carry words out of Keystone, as `web_fetch`'s can. A held chat cannot use the browser at all; any other chat's words are the person's own to send.
- A person must find the switch before the agent can use a browser; the model says so when it tries.
