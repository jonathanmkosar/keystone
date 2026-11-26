# Importing your chats and routines from New Keystone

New Keystone is the earlier app. This brings one person's chats and routines from it into Keystone: the chats open and are found by search like any other, and each routine becomes a schedule that is **paused** until you switch it on, the morning brief among them. How it works, and what is left behind, is in [`docs/architecture/import.md`](../architecture/import.md).

Nothing here changes New Keystone or its database, and nothing is sent to any model. You can run every step again: what was brought once is not brought twice.

The commands print counts, model names and dates. They never print a title, a routine's name or a word of a message, so their output can be read by anyone helping you.

## Before you start

1. **Keystone is on the build that has the import.** The import adds tables to your database, and an older server refuses a database that holds tables it does not know. So release the new build first ("Release a new build" in [start-at-sign-in.md](start-at-sign-in.md)), then import. The server can keep running while you import.
2. **Your tenant id**: `go run ./tools/tenant list`.
3. **Where New Keystone keeps its database.** It is one file, `workspace.sqlite`, in New Keystone's state folder: `%LOCALAPPDATA%\KeystoneDev\first-slice\` unless New Keystone was started with `--state <folder>`.
4. A backup of your Keystone tenant, if you want a way back: [backup-and-restore.md](backup-and-restore.md). An imported chat can also simply be deleted.

Run the commands from the repo's folder.

## 1. Copy the database

New Keystone keeps its database in write-ahead mode: while it runs, its newest turns sit in a second file, `workspace.sqlite-wal`. Copying `workspace.sqlite` alone in the file explorer can miss them, and can catch the file half-written. Keystone refuses such a file, and says so.

Make a whole copy with Keystone's own command. New Keystone can keep running:

    go run ./tools/tenant import-copy "$env:LOCALAPPDATA\KeystoneDev\first-slice\workspace.sqlite" D:\Imports\new-keystone-2026-11-26.sqlite

It reads the database, writes one file that holds everything in it at that moment, reads the copy back, and says it is sound. It never replaces a file: give a new name each time. It only reads New Keystone's database, and leaves no file of its own in that folder.

Two other ways that are just as safe:

- With the `sqlite3` program: `sqlite3 workspace.sqlite ".backup 'D:\Imports\new-keystone.sqlite'"`.
- Stop New Keystone, wait until `workspace.sqlite-wal` is gone or empty, and copy `workspace.sqlite`.

The copy holds your chats in plain text. Keep it somewhere only you can open, and delete it when you are done.

## 2. Check

    go run ./tools/tenant import --check <tenant-id> D:\Imports\new-keystone-2026-11-26.sqlite

It writes nothing. Read what it prints:

- **chats**: how many chats your one continuing New Keystone chat becomes, with routine runs and builds as chats of their own. New Keystone had no separate chats, so Keystone starts a new one wherever more than an hour passed between two messages. If that makes too many or too few, change the gap and check again:

        go run ./tools/tenant setting set <tenant-id> chats.import_chat_gap 3h

- **schedules**: how many routines become schedules, and whether the morning brief is among them.
- **Left out**: turns still under way (bring them with a later copy), and routines Keystone has no schedule for: ones that only ran when asked, ones never scheduled, deleted ones, one-time ones in the past.
- **The copy's other tables**: notebook entries, drafts, decisions and built apps are counted and not brought. Notes New Keystone sent to Waymarks are already in your knowledge store.
- **The time zone**: New Keystone kept routine times in America/Chicago. If you have no time zone set in Keystone, the import sets `agents.time_zone` to that, so 07:00 stays 07:00.

If the copy holds more than one person's workspace, the check lists them with their turn counts and asks for one: add `--workspace <name>` after `import`.

### One choice to make first: sensitive content

New Keystone marked nothing as sensitive, and sent every chat to whichever model it was set to. So imported chats are ordinary chats by default: any of your models may continue them, and the past-chat search finds them for any model.

If your old chats hold things that should only ever reach local or trusted models, say so **before** the import. A held chat stays held for good:

    go run ./tools/tenant setting set <tenant-id> chats.import_hold wiki    # only chats in which New Keystone read your Waymarks wiki
    go run ./tools/tenant setting set <tenant-id> chats.import_hold all     # every imported chat

The check's "held chats" line says how many chats that would hold.

## 3. Import

    go run ./tools/tenant import <tenant-id> D:\Imports\new-keystone-2026-11-26.sqlite

It prints what it brought, in the same shape as the check. Reload Keystone in the browser:

- Your old chats are in the list at their own dates, each with a small **imported** mark, and a line at the top of the chat saying where it came from, when it was said, which model answered there, and what New Keystone recorded in it (web searches, wiki lookups) that Keystone counts and cannot show as steps.
- Search finds them at once, by their titles and their words, and so does Keystone's own past-chat search when you ask it about something you discussed before. Each chat gets its short summary the first time you continue it. To make one now for a chat, without continuing it: `keystone api POST chats/<chat-id>/recap` (this calls your summary model).
- A reply you want to carry on from: just write in the chat. It continues on your default model here.

The import is in your audit log as `import_new_keystone`.

If the import stops partway (the machine sleeps, the window closes), run the same command again. Whole chats that were written are kept, and the rest are brought.

## 4. Switch on the morning brief

Every imported schedule is paused, so nothing runs before you have looked at it.

1. Open **Scheduled**. The morning brief is there with the name, the prompt and the time it had: daily at 07:00.
2. In New Keystone the brief was put together by code from Gmail and Calendar. Here a schedule's run is a chat: the prompt is sent to a model, which uses your tools. So before switching it on:
   - connect Gmail and Calendar if you have not ([google.md](google.md));
   - read the prompt, and edit it to say what you want in the brief (for example: today's calendar, then unread mail that needs an answer);
   - choose the model the runs use, or leave it on the default.
3. Press **Run now** once and read the result in its chat. Anything that would send mail or change the calendar asks you first, in that chat.
4. Turn the schedule's switch on. It is due at its next time, in your time zone; a time that has already passed today does not run late.

Do the same for any other schedule you want back. Delete the ones you do not.

When the brief runs here, pause or delete it in New Keystone, so you do not get two.

## Later

- **A newer copy.** Make a new copy and import it: only the turns since the last import are brought, as new chats.
- **Deleting.** An imported chat or schedule you delete stays deleted: a later import does not bring it back.
- **Undoing the whole import.** Delete the imported chats and schedules, or restore the backup from before.
