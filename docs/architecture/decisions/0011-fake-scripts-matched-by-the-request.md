# 0011. The fake provider finds its script from the request alone

November 24, 2026 · decided in P3-03

## The question

The fake provider plays scripts. When a request arrives it has to decide which script answers, and which step of that script the request has reached. A turn that uses a tool calls the model twice or more, so the second call must get the second step.

There are two ways to do it. The fake can remember: it plays replies in the order they were written, or it keeps a counter for each chat. Or it can work everything out from the request in front of it.

## The decision

The fake keeps no memory. The request alone decides.

- **The script** is chosen by the last message the person wrote, which is the last user message that is not a set of tool results. A script's `when` holds up to four conditions: the whole text of that message, a part of it, a part of the system text, and the model's name. Every condition that is set must hold. Text is compared without regard to letter case or the spaces around it.
- When several scripts match, **the one with the most conditions wins**. A tie goes to the earlier script, by file name and then by place in the file. An empty `when` matches everything and loses to anything more exact, so it is a catch-all.
- **The step** is the number of assistant messages since the person's last message, plus one. A tool loop adds the assistant's reply and the tool results to the request before it calls again, so the request itself says how far the script has got.
- A tool call keeps the id its script gives it, unless the request already holds that id. Then it gets `-2`, `-3` and so on.

The reasons:

- The same request always gets the same stream. A test does not depend on what ran before it, and tests can run at the same time.
- It survives everything a turn can go through: a retry, an edit that branches the chat, a queued turn that finishes after a restart, two chats at once. A counter would be wrong after any of them.
- A person adding a use case to the test tenant writes the message and the replies, and nothing else. Most scripts need one condition, `user_text`.
- "Most conditions wins" lets a catch-all and an exact script sit in any files in any order, which matters when the test tenant's scripts and the examples are loaded together.

## What it costs

- A script cannot fail once and then work, because the second try looks the same as the first. Fallbacks do not need it: a fallback goes to another model, and the `model` condition gives that model its own script. A retry against the same model is tested inside each adapter, on recorded exchanges. If a later item needs it, the honest way is a condition on the request, not a counter.
- Two turns in one chat with the same message get the same script. That is right for a scripted model, and the id rule keeps the second use valid.
- The step count trusts the request to hold the turn so far. If the context builder ever drops assistant messages from the middle of a turn, the count is wrong. It must not, because a real provider would lose the tool calls its results answer.
- A script cannot yet look at what a tool returned or at which tools are on offer. A condition can be added to `when` without changing the files that exist.

Two scripts with the same conditions are refused when they are loaded, because the second could never be chosen.
