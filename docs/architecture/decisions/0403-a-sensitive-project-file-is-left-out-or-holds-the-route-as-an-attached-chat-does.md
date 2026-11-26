# 0403. A sensitive project file is left out, or holds the route, as an attached chat does

November 25, 2026 · decided in P9-01

## The question

Fixed rule 3: content from a sensitive space reaches only models marked local or trusted. A project's knowledge file is not from the knowledge store, so it has no space and no label. May a file be sensitive, and if so, what does a call of a chat in the project carry of it?

## The decision

**A person may mark a file sensitive**, and a marked file is held to the rule as content from a sensitive space is:

- **A call is built for the least of its route.** A sensitive file reaches a call only when every model the call's route may reach may read sensitive content (`projects.MayShow`). Otherwise it is left out, and the call is given a pinned line saying how many files were left out, with no name.
- **The chat it goes into is held first.** `recall.Hold` marks the chat before the call is made; if the hold cannot be kept, the file is left out. From then on the chat goes only to models that may read sensitive content.
- **It holds the route when the chat's own model may read it.** `Runner.sensitive` counts a project with a sensitive file for a chat whose own model may read it, so the route keeps to models that may, and a hosted fallback is left out. A chat on a hosted model is not moved: its calls leave the file out.

**The instructions are not marked.** They are the person's directions for the project and go to every model; something private belongs in a file that is marked.

## Why

- It is decision 0363's answer for an attached chat, and recall's for a page: one rule for every way content reaches a call, which the person can predict.
- A file is marked by the person, not by where it came from, because a file has no space; the mark is on the file, so one sensitive file does not hold back the rest of the project.
- Holding the chat follows from the chat record: what a call was given stays in the chat and is sent again later.

## What it costs

- A hosted model in a project chat with a sensitive file answers without it, and knows only that one was left out.
- A chat that carried a sensitive file once is held for good, as a chat that read a sensitive page is.
- A project with a sensitive file keeps every chat on a local or trusted model from going to a hosted fallback, even for a question the file has nothing to do with.
