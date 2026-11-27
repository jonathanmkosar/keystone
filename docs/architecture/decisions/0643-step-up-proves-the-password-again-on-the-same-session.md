# 0643. Step-up proves the password or a passkey again, on the same session, for admin.step_up_for

November 27, 2026 · decided in P15-01 · provisional on the owner's answer A1

## The question

The spec asks the admin area to ask for the password or a passkey again, and to stay open for `admin.step_up_for`. Where is that kept, how does it share the protection sign-in already has, and how does a wrong password answer?

## The decision

- **A column on the session.** A migration adds `admin_until` to `signin_sessions`. `signin.Session` carries it as `AdminUntil`, read with the session by `Check` on every call, so the shell knows it with no second read.
- **`signin.StepUp(store, sessionID, password, options, lasts)`** checks the password exactly as sign-in does, through the same function, so a wrong password counts towards the same wait (`signin.failures_before_wait`, `signin.failure_wait`) and a wait refuses it unread. A good one sets `admin_until` to now plus `lasts`, never past the session's own end, and clears the count.
- **A passkey**: `signin.BeginPasskeyStepUp` hands out a challenge of its own purpose, `step-up`, which a sign-in answer cannot use and a step-up answer cannot sign in with; `signin.PasskeyStepUp` checks the answer as a passkey sign-in does. Passkeys stay behind `signin.passkeys`.
- **`signin.EndStepUp`** clears it, for `DELETE /api/v1/admin/step-up`. Signing out or a new password ends the session and the step-up with it.
- **`admin.step_up_for`**, 15 minutes as shipped, for the install. The server reads it with the other API settings.
- **The answers.** A wrong password or passkey is **403 `refused`**, never 401, because the session is still good and a client that read 401 as "signed out" would throw it away. A wait is 429 `wait` with `Retry-After`, as at sign-in. Passkeys switched off are 400 `passkeys_off`.

## Why

- The step-up is a property of one sign-in on one device. A column beside the session's end is the plainest place, and ends with it.
- Sharing the count means the step-up is not a second door to guess a password through.
- A purpose of its own for the challenge keeps the two flows from answering each other's prompts.

## What it costs

- A stolen session token with a fresh step-up opens the admin area until it ends. The cookie is HttpOnly and SameSite=Strict, and the window is fifteen minutes.
- Passkey step-up is proven against the test authenticator only, like passkey sign-in, until a browser uses it.
