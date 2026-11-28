# 0714. Each admin change of a setting is a pair of its own in the admin log and in the person's log, with the key, the old and the new value

November 28, 2026 · decided in P15-03 · builds on decision [0644](0644-adminview-is-the-only-way-to-another-person-and-writes-their-log-first.md)

## The question

"Each change is in both audit logs with its old and new value." The admin door already writes each admin call in the admin log before and after, with no body. How do the old and the new value get into both logs, in what order, and what of a change that is refused, or a change to the install, which is no person's?

## The decision

- **A person's change** (`adminview.ChangePersonSetting`):
  1. The request is checked first: the admin account is there now, the person is on the install, the key is a setting, the admin may change it for one person (`tenantsettings.MayChange` with the role `admin`), and the value fits. A refusal reads and writes nothing of the person's; the door's pair in the admin log records it with its status.
  2. The admin log gets an entry of its own, `started`, with the admin's name, the person's id, the key and the new value. If it cannot be written, nothing is changed.
  3. The person's log gets `started` with the admin's name, the key and the new value, as every admin read of a person does (decision 0644). If it cannot be written, nothing of theirs is read.
  4. The old value is read and the change made through `tenantsettings.Set` or `Clear`.
  5. The person's log gets the end, `ok` or `error`, with the old value too; then the admin log the same.
- The entries' `sent` field is `{"by", "person", "key", "old", "new"}`; `old` is left out of a `started` entry, whose old value is not read yet, and is there in the end entry even when it is empty. The person's Settings → Admin access reads `by` and `key` from it.
- **An install change** (`adminview.ChangeInstallSetting`) is in the admin log as its own pair, with the key, the old and the new value and whether it waits for a restart. It is in no person's log: it is no person's, and a default reaches a person only where they have not chosen.
- **A read** of a person's settings is in their log as a read, `{"by", "read": ["settings"]}`, as the people read is.

## Why

- The door's pair says the call happened and how it ended; the change's own pair says what changed, in the same shape in both logs, with the same request id, so the two can be read side by side.
- Checking before touching the person keeps a refused attempt out of their log and their store.

## What it costs

- A change is four entries in the admin log (the door's two and its own two), and the admin isolation walk, which counts the door's pair, calls every admin route with an empty body, which a change route refuses before its own pair.
