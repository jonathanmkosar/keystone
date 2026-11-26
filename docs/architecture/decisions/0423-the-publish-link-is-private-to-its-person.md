# 0423. "Publish" is a stable link on this Keystone, private to its person

November 25, 2026 · decided in P9-03

## The question

The plan gives artifacts "a local publish link". Keystone is public to no one: every path is behind the owner's Cloudflare Access, and every route needs a session. What does publishing mean here, and who can open the link?

## The decision

Every artifact has a link, `/artifacts/<id>`, and `?version=n` for one version. It is a page of the app that shows the artifact alone, outside the app's frame, with its versions, download and copy. It reads the artifact through the API with the person's session, so it opens only for the person who owns it, signed in. Anyone else is shown sign-in; another person on the same Keystone, signed in, is told it is not here (404). There is nothing to switch on: no artifact is ever made public, so a link needs no act of publishing and no undoing. The panel's **Copy link** gives the whole address, **Open alone** opens it in a tab.

## Why

- What a person wants from a "local publish link" is an address that stays: to bookmark, to open on a phone through their own way in, to keep beside other work. That needs no new door.
- Fixed rule 1: a route that served an artifact without a session would be the first door to a tenant's data. The `tenant isolation` check lists every door, and none is added.
- A link anyone could open is a share link, which is P10-04's, with its own decisions about expiry and revocation.

## What it costs

- The link is no use to anyone but its person. Someone who wants to show an artifact to another person downloads it, until share links exist.
- An artifact of a held chat opens on its link like any other, with its sensitive notice; nothing keeps it off a screen someone else can see.
