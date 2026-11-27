# 0580. The agent's browser is a browser already on the machine, driven over its DevTools pipe with no new dependency

November 27, 2026 · decided in P10-03

## The question

Browser use needs a real browser that runs a page's scripts, and a way for Keystone to drive it. Which browser, and how is it driven: a Go library such as `chromedp`, or the DevTools protocol by hand; over a debugging port, or something narrower?

## The decision

**A Chromium-family browser already installed** (Microsoft Edge first, then Google Chrome, then Chromium, in their usual places, or the one `agents.browser_path` names), started headless, **driven over its DevTools pipe** (`--remote-debugging-pipe`) **with the protocol written by hand in Go**, in `internal/tools/browser/cdp.go` and `chromium.go`. No dependency is added.

On Windows the pipe's two ends are handles the browser inherits, named by `--remote-debugging-io-pipes`, and passed to the browser alone through the new process's handle list (`SysProcAttr.AdditionalInheritedHandles`). On Unix they are its file descriptors 3 and 4. The browser and everything it starts are in a Windows job object that ends them when Keystone's handle to it closes, or in a process group of their own on Unix. The guard builds and checks the command line (`guard.Args`, `guard.CheckArgs`): headless, the pipe, one profile and no switch that opens a port, an address, another profile, an extension or a proxy.

## Why

- **No port at all.** A debugging port on `127.0.0.1` can be reached by every program on the machine, and anything that reaches it drives the browser. A pipe is held by Keystone's process only, which is what "never with remote-debugging open to anything but this process" asks for. Tried against Edge 158 on this machine before the plan was written; the browser writes no `DevToolsActivePort` file, and a test checks that.
- **No dependency.** `chromedp` would bring `cdproto`, `sysutil`, a websocket library and `easyjson`, and it speaks to a port or a websocket. The part of the protocol Keystone uses is a dozen methods and five events; a reader and writer of NUL-separated JSON is about two hundred lines, and `golang.org/x/sys`, already a dependency, has the job object.
- **The machine's browser, not a downloaded one.** Edge is on every Windows machine and stays up to date by itself; downloading a browser build would be a large file from outside to keep current. The profile is Keystone's own either way (decision 0582).

## What it costs

- The protocol code is Keystone's to keep: a method Chromium renames or removes breaks it, and the real-browser tests are what notice. They run in `go test` and in the `browser use` check wherever a browser is installed.
- The pipe needs a browser that has `--remote-debugging-io-pipes` on Windows; Edge and Chrome of the last few years do.
- There is a moment between starting the browser and putting it in the job object; a child it started in that moment is not ended with Keystone. `Browser.close` and the idle close end it in every ordinary case.
