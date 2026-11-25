# 0311. A remote connector's address is public HTTPS or the person's own local server, checked as it connects

November 25, 2026 · decided in P7-04

## The question

A remote connector is an address Keystone sends requests to, with the person's token. The owner's home network holds a model gateway, a NAS and a router, and Keystone runs on a machine inside it. Which addresses may a remote connector have, and how is that held when names can be made to resolve anywhere?

## The decision

**Two kinds, each checked both ways.**

- Not marked local: the address must start with `https://`, must not be a name that only means the local network (`localhost`, a bare name, `.local`, `.lan`, `.home`, `.internal` and the like), and every address its name resolves to must be on the public internet.
- Marked local by the person ("This is my own server, on this machine or my home network"): it may be `http://`, and every address its name resolves to must be on this machine or a private network, the shared range 100.64.0.0/10 included (Tailscale's). A public address marked local is refused, so the mark cannot be used to send a token over plain HTTP to the internet.

**Checked as it connects**, with web_fetch's dialler, exported for this as `web.Dial`, `web.IsPublic` and `web.IsLocalName` (decision 0251): Keystone resolves the name, checks every answer, dials the address it checked, and the socket checks it again as it connects. The entry is checked again each time it is reached, so a stored entry that would now be refused is not reached.

**No proxy, no redirect.** A redirect from the server is an error, never followed. The address comes from the person's record only; nothing a model, a tool result or a server says can change it.

**An error never holds the address**, in case a person wrote a token into it.

## Why

- A connector's server is outside content's author: it must not be able to point Keystone at the home network, by a redirect or by a name that resolves there.
- The rules are the fetcher's own, so there is one list of what is not the public internet, and a range added there is added here.
- The local mark is the person saying what they know: their Mac mini, their NAS. Requiring that it is in fact local keeps the mark honest.

## What it costs

- A public server with no HTTPS cannot be a connector.
- A name that resolves to both the internet and a home address is refused either way.
