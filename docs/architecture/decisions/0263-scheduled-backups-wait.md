# 0263. Scheduled backups wait for a key no one has to type, and for background jobs

November 25, 2026 · decided in P13-04

## The question

The item's scope says backups are "off by default", and the brief asked for a setting that turns scheduled backups on, if a schedule is simple to add now. Is it?

## The decision

**Not now.** A backup is made only when a person runs `tenant backup` and gives the passphrase. There is no schedule and no setting for one. Backups are therefore off by default in the plainest way: nothing runs unless asked.

## Why

- A schedule runs with no one there to type the passphrase. It would need the passphrase stored, which the item rules out, or a different kind of key: the backup sealed to a public key whose private half the person keeps away from the machine, as age's recipients work. That is a format change of its own (decision 0260 left a byte for it) and a choice about where the private key lives.
- Running work on a timer belongs to the background jobs being built now (P8-01). A backup timer written here would be a second scheduler, made before the first exists.
- Where scheduled backups go, how many are kept and how a failure is reported are questions for the owner, and none rests on what was built here.

## What it costs

- A backup happens only when the owner remembers to make one. The runbook gives the command and suggests a date in the file's name.
- The Windows task scheduler could run the command, but only by piping the passphrase from a file on the same machine, which is no better than storing it. The runbook does not suggest it.
- The next item that takes this up builds the public-key format and a job on P8-01's scheduler, and adds the setting then, off by default.
