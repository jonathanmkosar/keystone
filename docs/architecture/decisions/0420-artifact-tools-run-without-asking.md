# 0420. The artifact tools run without asking, after outside content too

November 25, 2026 · decided in P9-03

## The question

A tool that is not marked as only reading "changes something, inside Keystone or outside it", asks by default, and always asks once the chat has read outside content (P7-01, P7-02). Making or changing an artifact writes to the person's own artifacts folder. Should `create_artifact` and `update_artifact` ask like a tool that changes something, or run like one that reads?

## The decision

They run without asking. All three artifact tools set `ReadOnly` and `Inside`, so the approval rule gives them the default `always`, and they do not ask because a chat read outside content. A person can still set each to ask or never on Settings → Approvals, which says "Writes only to your artifacts in Keystone, keeping every version." `ReadOnly` here means what the approval rule reads it as: nothing outside Keystone changes and nothing is lost.

## Why

- Fixed rule 2 is about a side effect outside Keystone. An artifact is the model's reply in another shape: the reply's words are kept in the person's record without asking, and an artifact's are kept in the person's folder the same way. Nothing leaves Keystone, and no one else sees it: the publish link is the person's alone (decision 0423).
- Nothing is lost. A change is a new version and every version is kept, so a change the person did not want is one restore away.
- What an artifact can do is held by its frame, not by an approval: it is drawn in a page of no origin that fetches and sends nothing (decision 0422), so content read from outside cannot use one to reach the person's session or send anything out.
- Asking would make the most common use, "find this out on the web and write it up", ask every time, and a card a person approves without reading protects nothing.

## What it costs

- A model misled by a page it read can still make an artifact the person did not ask for, or change one. They see it as a card in the reply, and every version is kept.
- The flag's name says "reads" for two tools that write. The screen says what they do, and the tool loop's doc says what the flag means for these tools.
