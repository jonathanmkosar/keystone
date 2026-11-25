# 0295. Starting a task changes something, so it asks; and a task starts no other

November 25, 2026 · decided in P8-01

## The question

A model may start a background task with a tool. The tool writes only inside Keystone. Is it a tool that only reads, which runs without asking, or one that changes something? And may a task start tasks of its own?

## The decision

**`start_background_task` changes something.** Under the approval policy it asks by default, the person may set it to always, and it always asks once the chat has read outside content. Its result is Keystone's own words, so running it brings no outside content into the chat.

**A task cannot start another.** A start from a task's own work chat is refused, by the route and by the tool. A person may have at most `agents.task_open_max` tasks waiting or running.

## Why

- A task runs tools and spends money without the person watching. That is a side effect in every sense that matters, so the person decides, once, whether a model may start one without asking.
- A web page that tells the model to start a task would otherwise have a way to run work unattended. After outside content the start always asks, and the task's own chat carries the mark (0294).
- Tasks that start tasks have no natural end and could use the person's money in a widening tree. One level, and a few at a time, keeps it bounded until sub-agents (P8-02) give nested work its own design.

## What it costs

- A model that would start a task gets an approval card first, unless the person sets the tool to always.
- Work that would split into tasks of its own must wait for sub-agents.
