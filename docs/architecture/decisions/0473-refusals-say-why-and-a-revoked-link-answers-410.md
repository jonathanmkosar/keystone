# 0473. A refused link says why: 410 when turned off or expired, 403 when its chat is held or incognito

November 26, 2026 · decided in P10-04

## The question

A link can stop showing its chat in four ways: its person turns it off, its time runs out, its chat is held for content from a sensitive space, or its chat is made incognito. The item asks for a revoked link to be refused "with 404 or 410, and say which", and for a held chat either not to be shared or for its link to say it is refused. What does each answer, and what may the answer say?

## The decision

| Case | Status | Code |
| --- | --- | --- |
| No such link: a token of the wrong shape, for no tenant on the install, re-addressed, forged, or of a deleted chat | 404 | `not_found` |
| Turned off | 410 | `revoked` |
| Expired | 410 | `expired` |
| The chat is held for content from a sensitive space | 403 | `sensitive` |
| The chat is incognito | 403 | `incognito` |

- **A held chat is never shared**: making a link to one is refused (409, `sensitive`), and a link made before the chat was held is refused when it is opened. The hold is read from the record on every call. The same for an incognito chat.
- **Turning off keeps the row**, so the link can answer 410, and the person's list keeps it, marked turned off. A link of a deleted chat goes with the chat and answers 404.
- Each refusal carries a sentence for the person who opened the link, which says why it shows nothing and nothing of the chat. A refusal of a found link is recorded in that tenant's audit log as refused; a token that finds nothing is not, since it names no tenant for certain.

## Why

- 410 says the link was real and is closed for good, which is true and is what someone who was sent it needs to know: ask again, do not retry. It tells nothing to someone without the token, who gets 404 like any guess.
- 403 for a held chat follows the recommended answer, "refused with a reason". It tells a holder of the link that the chat now holds sensitive content, and nothing of that content. Refusing at the door rather than revoking the link when the chat is held needs no change in the turn package, and holds even for a chat held by a path not yet written.
- Fixed rule 3 says sensitive content reaches only local or trusted models; a stranger's browser is neither, so no setting can open a held chat's link, and the guard that refuses it reads no setting.

## What it costs

- Someone holding a link learns which of the four happened. For a held chat that is the fact that it holds sensitive content.
- A turned-off link's row stays in the tenant's database until the chat is deleted. It holds a hash, two times and a count.
