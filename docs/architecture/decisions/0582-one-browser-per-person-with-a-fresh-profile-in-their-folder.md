# 0582. One browser for each person, with a fresh profile in their own folder and one tab that belongs to a chat

November 27, 2026 · decided in P10-03

## The question

Where does the agent's browser keep its profile, how many are there, how long do they live, and which chat sees which page?

## The decision

- **One browser per person at a time**, started by their first browser call and closed `agents.browser_idle_timeout` after their last (5 minutes as shipped), and with Keystone (decision 0580).
- **Its profile is `browser-profile` in the person's tenant folder**, made new and empty at each start (what a killed browser left is removed first), checked by `guard.CheckProfile` to be that folder and empty, and removed when the browser closes. It is never any of the person's own browser profiles: the browser is started with `--user-data-dir` set to it and nothing else, and sync, sign-in, extensions, background networking and the machine's proxy are off.
- **No cookie but its own.** When it starts the profile must hold no cookie at all; after every call each cookie must be for a host the browser itself asked for since it started (`guard.CheckCookies`). Either failing closes the browser and says why.
- **Backups leave the folder out** (`tenant.BrowserProfileFolder`): its files are held open while the browser runs, and it is gone when it closes.
- **One tab, owned by the chat that last opened a page.** Another chat must open its own page before it reads, clicks or types; the person's calls run one at a time.

## Why

- **Nothing of the person's can be in it.** A profile made empty at each start, in Keystone's own folder for that person, holds none of their sign-ins, passwords, history or cookies; the cookie check proves it after each call rather than trusting it.
- **Each person's own folder** keeps one person's browsing apart from another's, as the tenant rule keeps everything else.
- **One tab** makes the window during which a page may send (decision 0581) belong to one page and one approved call, and keeps one chat from reading what another opened.

## What it costs

- A browser starts afresh after each idle time, a second or so, and loses what pages it was on and the cookies sites gave it.
- Two chats of one person using the browser at once take turns, and the second must open its page again.
- A browser that holds a site's cookie across chats of the same person within its idle time is shared by them; it is the agent's, never the person's.
