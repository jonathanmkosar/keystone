# 0581. Every request of the browser is decided by Keystone, and a page sends only while an approved click or typing is under way

November 27, 2026 · decided in P10-03

## The question

The approval rule makes the agent's own clicks and typing ask. But a page runs scripts: opening a page, which only reads and so does not ask, could send its own form as it loads, post in the background, or load a frame that does. A planted page could cause a side effect with no tool that changes anything ever being called. How is that kept out, and where are the address rules (no blocked host, nothing on this machine or the home network) enforced for a browser that loads many things for one page?

## The decision

**Every request the browser makes is paused at the browser and decided by Keystone.** The `Fetch` domain is enabled on the browser itself, not on a tab, so a request of any tab, frame, popup or worker is paused before it leaves. The pool's policy then decides it:

1. `guard.CheckRequest`: only `http` and `https`, and the browser's own schemes for reading; no user name or password in the address; no host on `agents.browser_blocked_hosts`; and **a method other than GET, HEAD or OPTIONS only while `Sending` is set**.
2. The host: web_fetch's rule (decision 0251), with `web.IsPublic` and `web.IsLocalName`: a literal address must be public, and a name must not mean this machine or a home network and must resolve only to public addresses. A verdict is kept for a minute.

`Sending` is set only while `browser_click` or `browser_type` runs, which is only once the person has approved that call, and cleared when it returns. A refused page is aborted, so the tab keeps the page it shows; a refused send is told to the model in the result ("The page tried to send something to … with POST, and Keystone stopped it").

Downloads are denied at the browser (`Browser.setDownloadBehavior`), a file chooser is intercepted and never answered, a dialog is dismissed, and a window a page opens is closed as it appears.

## Why

- **It closes the gap the approval rule cannot see.** A side effect a page's own script causes is not a tool call. With every send held to an approved click, opening the planted page in the tests sends nothing, though it submits its form on a timer, posts with `fetch` and loads a frame that posts.
- **One place for every rule.** Redirects, frames and subresources are each a paused request, so the block list and the public-address rule hold for all of them, not only the address the model asked for.
- **Aborted, not failed.** A failed page shows the browser's error page in place of the one the agent was reading; an aborted one leaves it.

## What it costs

- **Some pages read less.** A site that reads with POST (some search boxes, some single-page apps) gets nothing back for it while the agent only reads. The result says what was stopped.
- **Address checks are made apart from the browser's own lookup.** Keystone resolves the name, the browser resolves it again to connect; a name that changes its address between the two (DNS rebinding) is not caught as web_fetch catches it. The profile holds no sign-ins, so what such a page reaches is what any page on the network could, and the block list still holds.
- **A WebSocket is not paused** by the `Fetch` domain. A page's own script can open one to its server. It acts as nobody: the profile is empty.
- A script's GET requests are not sends and go, as a link does.
