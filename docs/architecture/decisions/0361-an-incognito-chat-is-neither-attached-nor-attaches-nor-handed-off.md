# 0361. An incognito chat is neither attached, nor attaches, nor is handed off

November 25, 2026 · decided in P5-06

## The question

A chat can be made incognito (P6-06): its turns run no memory pass, and the plan's memory screens (P6-07) say incognito chats "keep no record". Attaching and handing off carry one chat's summary into another chat's calls, and so into that chat's history and its own recap and memory. What should an incognito chat do on either side, and what happens when a chat is made incognito after it was attached?

## The decision

An incognito chat is outside attaching and handoff altogether:

- it cannot be attached to a chat (`409 incognito`);
- it cannot attach one (`409 incognito`);
- it cannot be handed off (`409 incognito`);
- a chat made incognito after it attached chats carries none of them in its calls and offers no `open_attached_chat`; its links stay, and come back if the flag is turned off;
- an attached chat made incognito afterwards is left out of every call and of the tool, with no line about it, and the screen marks its chip so the person can take it off.

A flag that cannot be read counts as on. `internal/attach` holds the rule (`MayShow`, `Add`, `Handoff`) beside fixed rule 3's answer.

## Why

- Carrying an incognito chat's summary into an ordinary chat would put a record of it where the memory pass reads, which is what incognito is there to stop.
- An incognito chat that attaches an ordinary one would read the person's record without being part of it: harmless, but it makes "incognito" mean two things, and P6-07 is deciding what it keeps. The strict rule can be loosened later without anything having leaked; the loose one cannot be taken back.
- A handoff of an incognito chat could instead make an incognito handoff, as a background task from an incognito chat is incognito (P8-01). That needs a summary of the incognito chat to be written somewhere, which waits on what P6-07 decides incognito keeps; refusing is the answer that holds whatever it decides.

## What it costs

- A person in an incognito chat who wants to go on afresh starts a new chat by hand, with nothing carried.
- A chat made incognito loses its attached chats from its calls until the flag is off again.
