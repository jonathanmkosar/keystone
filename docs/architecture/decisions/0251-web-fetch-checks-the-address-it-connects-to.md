# 0251. web_fetch checks the address it connects to, after resolving the name and on every redirect

November 25, 2026 · decided in P7-03

## The question

`web_fetch` reads a page at an address the model chooses, and the model may have read that address in a web page. Keystone runs on the owner's own machine, on his home network, beside a router, a Mac mini gateway and whatever else answers there. How is `web_fetch` kept from being pointed at any of it?

## The decision

**Only public addresses are read, and the check is made on the address really connected to.**

- Keystone resolves the name itself. Every address the name resolves to must be public, so a name that points at the internet and at the home network both is refused. The address dialled is the one that was checked, and the socket checks it once more as it connects (`net.Dialer.Control`).
- A redirect is followed by the same client, so it is resolved, checked and dialled the same way. At most `agents.web_fetch_max_redirects`, and each must be `http` or `https`.
- Not public: loopback, private, link-local, the shared range 100.64.0.0/10 (carrier NAT, and Tailscale), unspecified, multicast and broadcast, documentation, benchmarking and reserved ranges, IPv4 and IPv6; an IPv4 address written as IPv6 is read as IPv4; NAT64, 6to4 and Teredo, which can carry a private IPv4 address.
- A bare name and names under `.local`, `.lan`, `.home`, `.internal`, `.home.arpa` and `localhost` are refused before any lookup, so the refusal is clear.
- The environment's proxy is never used, because a proxy would hide the address really connected to.

## Why

- A check of the address as written misses a name that resolves to a home address, and a check made before connecting misses a name whose answer changes between the check and the connection (DNS rebinding). Checking the address that is dialled, and dialling the address that was checked, closes both.
- A redirect is the easiest way round a check made once: a public page that answers with a redirect to the router. Every hop is dialled anew, so every hop is checked.
- The owner reaches his Mac mini over the shared range, so that range is as private as 192.168/16 here.
- The search endpoint is not checked this way. It is a setting the admin chooses, and it may be a stand-in or a proxy on this machine.

## What it costs

- No page on the home network can be read, even one the owner wants. A tool for that would be a different tool, with its own approval.
- A site reached only through a proxy cannot be read.
- The router's own public address is public. A router that answers its admin page on its public address to a request from inside could be reached. That is a router's fault, and outside what an address check can know.
- The tests serve pages on this machine, so the fetcher's check is a field the package's own tests can widen. Nothing outside the package can.
