# 0402. Deleting a project keeps its chats

November 25, 2026 · decided in P9-01

## The question

When a person deletes a project, what becomes of the chats in it?

## The decision

**They are kept, as ordinary chats.** Deleting a project removes the project, its knowledge files' rows and each file nothing else holds. The rows that put chats in it go with it (`ON DELETE CASCADE` on `project_chats`), and the chats stay in the sidebar and the chat list. Their next calls carry nothing of the project. Facts the memory pass kept in the project's space stay in the knowledge store.

A chat is put in one project or none, and is moved out of a project, or into another, without being touched.

## Why

- A chat is a record of what was said, and the plan keeps the chat record apart from everything else. Losing conversations because a folder was deleted is the surprise a person would least expect.
- A person who wants the chats gone can delete them, one by one, before or after; a person who wanted them kept could not get them back.
- It is the same choice the knowledge store makes: deleting a project does not reach into the store.

## What it costs

- A person who meant to delete everything has to delete the chats too. The screen says that the chats are kept before it deletes.
