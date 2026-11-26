# 0360. An attached chat comes in as its recap, and the rest through a tool made for each call

November 25, 2026 · decided in P5-06

## The question

The plan: "Attach a chat. Another chat comes in as its summary, and the agent can open the rest." Which summary, made when, and how does the model open the rest? A chat already has two kinds of summary: compaction's, which stands for the oldest turns of one branch inside a call (P5-02), and the recap, one short card for the whole chat (P5-05). `read_chat` already reads a chat, but it is in the server's registry, gives the last forty messages, and reads any chat of the person's.

## The decision

**The recap is the summary.** Each attached chat comes into the context builder's `attached` part as its title, id, the date of its last message and its recap (`Summary:`, `Topics:`, `Decisions:`). In the first call of a turn, an attached chat with no recap, or whose current branch has moved past the message its recap was made from, is given one by the recap code itself (`makeRecap`), on the summary route, under the turn's request id, while the turn waits up to `chats.attached_summary_wait`. A recap that cannot be made in time is left for the next turn; the chat is carried without it.

**The rest comes through `open_attached_chat`**, a tool offered only while the chat carries an attached chat. It reads only chats attached to the chat the call is in, a page at a time from the start (`chats.attached_page_messages`, `chats.attached_page_max_chars`), and says how many pages there are. Like the past-chat tools (decision [0323](0323-past-chats-reach-a-call-for-the-least-of-its-route.md)) it is made for each call, for the least of the call's route, kept on the turn, and run through a registry of its own, so it is recorded and checked by the approval rule as every tool is.

**A page holds the person's words and the replies.** A tool call is named; a tool's result and a model's thinking are left out, as `read_chat` leaves them out. The tool is read-only and marked as Keystone's own content.

## Why

- The recap is the plan's "summary, topics, decisions" of a chat, already short, already sensitive-marked, already searchable. Compaction's summary is of part of one branch and is made for the window, not for another chat.
- Making it with the existing code keeps one way to write a recap, one prompt, one place where a held chat's recap goes only to a model that may read it.
- Waiting in the first call only, and only when it is missing or stale, costs one summary call the first time a chat is carried, and nothing after while it does not change.
- A tool of its own, scoped to what is attached, says what it is for, and its pages reach the start of a long chat, which `read_chat`'s last forty messages do not.
- Leaving tool results out keeps the result Keystone's own content. A tool result in an attached chat may be a web page or an email: given here as inside content, it would let a side effect run without asking after outside content was read, which fixed rule 2 forbids. Marking the whole tool as outside content instead would make every later side effect in the chat ask after merely reading one's own chat.

## What it costs

- The first turn that carries a new or changed chat waits for a summary call, up to thirty seconds by default.
- What a tool found in the attached chat is reachable only as the reply in that chat said it.
- A chat with no recap and a summary model that is down is carried as its title alone until the model is back.
