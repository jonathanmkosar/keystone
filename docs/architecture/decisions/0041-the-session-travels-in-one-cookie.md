# 0041. The session travels in one cookie, for the browser and the command line alike

November 24, 2026 · decided in P2-05

## The question

A session token is a bearer token: whoever holds it is the person. How does a client present it, how is it kept away from a script running in the page, and what does the command-line client do?

## The decision

- Sign-in answers with the token in a cookie named `keystone_session`. The token is in no response body, and no route returns it later.
- The cookie is `HttpOnly`, `SameSite=Strict`, limited to the path `/api/`, and ends when the session does. It is `Secure` when the request came over HTTPS or when the `server.secure_cookie` setting is on.
- The cookie is the only way to present a session. There is no `Authorization` header and no token in a query string.
- A write (any method but `GET`, `HEAD` and `OPTIONS`) that a browser says came from another site is refused, using Go's `http.CrossOriginProtection`.
- The command-line client signs in through the same door, reads the cookie from the answer, and keeps the token in a file in the user's profile, readable by that user only. It sends the token back as the same cookie, and only to the server that gave it.

## Why

- A script in the page cannot read an `HttpOnly` cookie. If a chat ever renders something hostile, there is no token in the page's reach to send away. Keeping the token out of every body means there is nothing for a page to store either.
- `SameSite=Strict` stops another site from making a signed-in request at all. The cross-origin check is a second lock on writes, for a browser that mishandles the first.
- One way to present a session is one thing to check, to log safely and to walk in `verify`. A second way, such as a bearer header for the command line, would be a private path of the kind the plan rules out.
- The command line is a client like any other. Sending the cookie costs it four lines.

## What it costs

- `server.secure_cookie` ships off, because Keystone is reached over plain HTTP on the home network today. On that network the cookie can be read by anyone who can watch the traffic. The setting goes on when Keystone is reached over HTTPS, which the phone access decision (P10-05) settles.
- `SameSite=Strict` means a link to Keystone from another site arrives without the cookie on its first request. The page then loads and its own calls carry the cookie, so the person sees the page and not a sign-in screen. If that ever proves wrong, the page can ask who is signed in after it loads.
- The command-line client's token is on disk in plain text, protected by the file's permissions, as most command-line tools keep theirs. On Windows the profile folder's own permissions are what protect it. Wrapping it with the operating system's key store, as secrets are (decision 0006), is possible and was left out.
- A client that is not a browser sends none of the headers the cross-origin check reads, and is let through. That is right: the check protects a browser's cookie, and such a client has to hold a token of its own.
