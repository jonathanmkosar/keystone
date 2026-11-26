# 0492. A sign-in for a person is in that person's log, whoever sent it

November 26, 2026 · decided in P13-02

## The question

The brief for the isolation drill asks that each refused attempt be in the second person's own log, and that nothing about it appear in the first person's log or data. One attempt cannot meet both: the second person trying to sign in as the first. The sign-in door has no session (P2-05): it writes its entry in the log of the person the sign-in is for, and a wrong password counts toward that person's wait. Should the drill treat this as a hole?

## The decision

**No. A sign-in for a person belongs in that person's log**, whoever sent it and whatever cookie came with it: it is the person who needs to see that someone tried their password. The drill tries two such sign-ins (with the second person's password and session cookie, and with a guessed password), and requires each to be refused with 401, to set no cookie, and to change the first person's data in exactly two ways: two `refused` entries for `POST /api/v1/session` in their log, and their count of wrong passwords. Anything else is a way in.

## Why

- The second person's session proves nothing about who typed the password; a door that wrote into the caller's log would let anyone keep attempts out of the target's sight by sending a cookie.
- The alternative, a log for the install, does not exist yet, and the person tried is the one who must be told.

## What it costs

- Another person on the install can make the first person wait by sending wrong passwords. This is already listed under "Not here yet" in [api.md](../api.md) (a limit on how often the door may be tried), and becomes pressing with phone access from outside (P10-05).
