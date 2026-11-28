# 0712. A person's default from the admin is followed by everyone who has not chosen, and a person's own choice stays theirs

November 28, 2026 · decided in P15-03 · provisional on the owner: the spec assumes install-wide defaults for each person's own settings

## The question

The spec assumes the admin "sets install-wide defaults for the settings that are each person's own. A new person starts with them. A person's own choice stays theirs, and the admin cannot change it." Is a default copied into a new person's settings when they are added, or is it the default that anyone without a choice of their own follows?

## The decision

The second. A person default is the default in force for every person who has no value of their own (decision 0711): a new person starts with it, and a person who never chose follows a later change. A person's own value always wins, and the admin cannot set or clear it: `tenantsettings.MayChange` refuses the role `admin` on a `tenant` setting, as it did.

Nothing is written into a person's database when they are added or when a default changes.

## Why

- A copy would be stored as the person's own value, with `set_by` saying the person chose it when they did not, and the admin's later change would not reach them.
- It is what a default means on a person's General screen already: "Reset to default" goes back to it.

## What it costs

- A person who has kept a shipped default without touching it sees it change when the admin sets a person default. The person's screen shows the new value as the default, with no "Changed" mark.
- If the owner wants the copy instead (a default only for people added after it), it is a change in `installsettings` and a migration-free write at `Install.Create`.
