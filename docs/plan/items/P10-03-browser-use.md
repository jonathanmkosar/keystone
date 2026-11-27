# P10-03. Browser use

Status: doing (agents)
Lane: agents
Depends on: P7-02
Owner gate: none
Scope: The agent drives a separate headless browser profile with none of the person's sign-ins; every page read is outside content.
Proof: The profile holds no cookies from the person's browsers; a page with planted instructions cannot cause an unapproved side effect.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (agents lane, November 27, 2026)

Branch `agents/p10-03-browser-use`, cut from `reboot` at 014284e. Asked for by the owner today. New code goes in new files; every line changed in a shared file is listed in the journal.

1. **The rules, a new package `internal/tools/browser/guard`**, listed in `tools/verify/fixedrules.go` as a guard of rule 2 (and rule 4 for the record of each call), importing nothing of Keystone's. It holds: which actions are side effects (click and type) and which only read (open, read, screenshot, back); the address check for a page (http and https, a host, no user name, not on the block list); the request check every request of the browser passes (only GET, HEAD and OPTIONS unless an approved click or type is under way, so a page cannot send a form on its own; no scheme but http and https, and the browser's own; no blocked host); the field check (no typing into a password, payment, one-time-code or sign-in field, nor into any field of a form that holds a password; nothing typed that looks like a key or password; no click on a file chooser or on a sign-in form's button); the cookie check (every cookie the profile holds is from a host this browser itself asked for, and there are none at start); and the launch check (headless, the debugging pipe only, never a port or an address, a profile folder that is fresh, empty and inside the person's own tenant folder).
2. **The browser, `internal/tools/browser`.** A Chromium-family browser already on the machine (`agents.browser_path`, else Edge's and Chrome's usual places), started headless with `--remote-debugging-pipe`, so the DevTools protocol runs over two pipes only this process holds: no port is opened at all (on Windows the pipe handles are passed with `--remote-debugging-io-pipes`; tried against Edge 158 before planning). Raw CDP in Go, no new dependency. One browser per person, with one tab, owned by the chat that last opened a page; closed after `agents.browser_idle_timeout`, and killed with Keystone (a Windows job object). The profile is `browser-profile` in the person's tenant folder, made new at each start, removed at close, and left out of backups. Every request is intercepted at the browser level (`Fetch`), checked by the guard and by web_fetch's address rules (public addresses only), downloads are denied, dialogs are dismissed and new windows closed.
3. **Six tools**, offered only when `agents.browser_use` (per person, off by default) is on: `browser_open`, `browser_read` (text cut to `agents.browser_text_max_chars`, with numbered elements), `browser_click` and `browser_type` (side effects: not read-only, so they ask, and always after outside content), `browser_screenshot`, `browser_back`. None is `Inside`: every result opens with the outside-content mark. Off in a chat held for sensitive content, as the web tools are. Calls and pages counted through a turn from the chat record (`agents.browser_max_steps`, `agents.browser_max_pages`). Each call is recorded by `tools.Run` as every call is. Screenshots are kept in a new table `browser_shots`, gone with the chat, and served by `GET /api/v1/browser/shots/{shot}`; the result names the shot and the step shows it.
4. **The proof**: guard tests; a fake driver (pages in memory, the same request policy) proving the rules on any machine; tests that drive the real browser against pages served on this machine and skip with a reason when none is found: the profile holds no cookies at start and none it was not given; a page with planted instructions and a script that sends its form on load sends nothing; a password field is refused; a download is refused. In a real turn with the fake provider (`internal/turn`), the planted page's click asks and, declined, never sends. A standing check `browser use` (fake driver on the real server with a recorded turn; the real browser too where one is found). The drill gets `{shot}`.
5. **Settings** in `settings/agents.json`: the switch, the path, idle time, page timeout, pages and steps a turn may use, the text limit, the element limit, the block list, the window's width and height (the screenshot's size).
6. **The screen**: the switch is a tenant setting, so Settings draws it; the six steps' lines in `steps.ts` and the screenshot under a step, from the tokens; provisional on the agent mockups (P1-05).
7. **The write-up**: `docs/architecture/browser-use.md`, decisions from 0580, the journal `2026-11-27-agents-014.md`, the status.

