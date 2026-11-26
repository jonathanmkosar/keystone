# 0472. A link shows words, images and names, and never a tool's result

November 26, 2026 · decided in P10-04

## The question

A chat's record holds more than its words: the model's thinking, each tool's input and result, uploaded images and files, and the artifacts its replies made. A shared link must show "nothing else of the person's". What of the record may a stranger see, and how is it drawn so nothing in it can run?

## The decision

A view holds, and the page draws:

- **Words.** The person's text, and the replies' text, drawn through the app's Markdown reader, which builds React elements and never sets HTML. Keystone's own notes (a stopped reply, the step limit) as notes.
- **Tool steps by name.** Each tool call as its tool's name and whether it failed, in words on the page ("Searched the web"). Never what it was sent, never what it gave back.
- **Images, through the link.** An image in one of the person's messages, fetched through the share token's image door, only when it is an upload of that chat known by its bytes as an image, served under `default-src 'none'; sandbox`; and only while `share.images` is on, as it ships. Other files by name only.
- **Artifacts as their source.** A version a step of the chat made, as text in a code block, never run: no sandbox page and no frame. The guard checks the artifact is of the link's chat before the content is read. `share.artifacts` turns this off.

Never: the model's thinking, tool inputs or results, the person's name, the model, or anything from outside the one chat.

## Why

- A tool's result is where the rest of a person's data comes into a chat: `search_chats` and `read_chat` return their other chats, the knowledge lookups return their memory, Gmail and Calendar their mail and events. Showing results would show other chats and memory, which the item forbids. The reply's words are what the person chose to share; what the model read to write them is not.
- A result from outside content (a web page, an email) could hold markup meant to run. Showing no result at all is simpler and safer than showing it escaped.
- Thinking is the model's working, which a person rarely reads before sharing; it can restate the profile, the map or recalled pages. Leaving it out costs a reader little.
- An image is the person's own upload, already served as an image to them under a policy that allows nothing; serving it through the token, only for a message the view shows, adds no new way in. Files are named and not served: a PDF or an HTML file is the kind of thing that runs.
- An artifact in the sandbox is drawn from a page of no origin with its renderers written in; doing that for a stranger is a larger surface than the item needs, and its source shows what was made.

## What it costs

- A reply that leaned on web results has its citation numbers but no sources: the sources live in the results. A later item could let a person show a step's sources.
- A shared chat with an HTML or React artifact shows its code, not the page.
- Images make the doors serve bytes. A person who would rather not switches `share.images` off; every link follows at once.
