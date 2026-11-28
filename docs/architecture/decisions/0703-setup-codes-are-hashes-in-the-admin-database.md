# 0703. Setup codes are kept as hashes in the admin database, good once for one person, and a new code is how a password is replaced

November 28, 2026 · decided in P15-02

## The question

An admin adds a person with a name and is given a one-time setup code, good for `admin.setup_code_for`; the person opens `/setup`, enters it and sets their own password, and the admin never sees or sets it. A new code can be asked for. Where are codes kept, what do they look like, and what may a code do?

## The decision

- **The code** is 20 characters from 32 that cannot be mistaken for one another (no I, O, 0 or 1), shown in groups of four: 100 random bits. Spaces and dashes are ignored and case does not matter when it is typed.
- **Where.** `data/admin/admin.db`, table `setup_codes` (migration `20261128T110000_setup_codes.sql`): the SHA-256 of the code under a fixed label, the person's id, the issuing account, when made, when it expires, when used and when ended. The code itself exists only in the reply to the admin who made it.
- **Once, for one person, until it expires.** The door `POST /api/v1/setup` finds the person by the code, refuses a disabled person, checks the password against the rules, then uses the code in one statement that matches the code, the person, unused, not ended and not expired, so two uses at once cannot both win; then it sets the password (which ends every session) and opens one. Every refusal of a code is the same answer, 401 `refused`. A new code ends every earlier unused code of that person.
- **Both logs.** The code is made through `adminview`, so the person's log has the admin's pair of entries with `{"key":"setup_code","new":"made"}`, and the admin's log has the call. The setup itself is in the person's own log as their call, with the code and the password scrubbed.
- **Passkeys at setup** wait for `signin.passkeys`, which is off as shipped: a person adds one from Settings afterwards.

## Why

- Codes are made by admins for people, so the admin database is their natural home, beside the account that made each; the door reads it the way the admin's own sign-in does. A copied admin database holds no code that works.
- The person's id is in the row, not the code, so the code can be short enough to read out, and the door never needs to open every person to find whose it is.
- A new code is the only way back for a person who forgot their password, on an install with no mail. The same code that sets up a new person does it.

## What it costs

- **An admin can set a person's password,** by making a code and using it. That is also an admin reading the person's data, which A2 rules out. The guards are that the person sees "setup code made" with the admin's name in Settings → Admin access, their old password stops working, and every session they had ends. This is an owner question in the journal.
- A code sent over a chat app is as safe as that app for its life; 24 hours is the default.
