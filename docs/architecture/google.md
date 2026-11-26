# Gmail and Google Calendar

Folders: `internal/google` (the OAuth client, and the Gmail and Calendar tools as plain data and functions), `internal/google/googletest` (a stand-in Google for tests and `verify`), the connectors' side in `internal/connectors/google.go` and `googleroutes.go`, the migration `20261125T190000_google_connectors.sql`, and the screen in `web/app/src/GoogleSettings.tsx`

Gmail and Google Calendar are two connectors of P7-04's kind, named `gmail` and `calendar`, with a third transport, `google`. They sit in the person's `connectors` table like any other, so the tools menu, the switches per chat, the policies on Settings → Approvals, `load_tools`, the outside mark and the audit path are all [connectors.md](connectors.md)'s. Their tools are Keystone's own Go functions, run in process, not an MCP server. One Google sign-in serves both.

Built in P12-02. Decisions [0350](decisions/0350-gmail-and-calendar-are-built-in-connectors.md) to [0353](decisions/0353-a-change-to-an-event-names-its-guests-and-is-checked.md) say why it is built this way. It is provisional until the owner signs in to Google: only the stand-in has been reached.

## The tools

Each is offered as `<connector>_<name>`, by name until `load_tools` loads it, and marked as outside content unless the person marks the connector otherwise.

| Tool | Reads or changes | Asks |
| --- | --- | --- |
| `gmail_search` | Reads: Gmail's own search words, up to `agents.google_list_max` messages, each with its id, sender, recipients, date, subject and opening words. | Never, unless set to ask. |
| `gmail_read` | Reads one message: its headers, labels, attachments' names, and its text (the plain part, or the HTML part with the markup taken out). | Never, unless set to ask. |
| `gmail_draft` | Saves a draft in the person's mailbox. Nothing is sent. | By default, as any tool that changes something; after outside content, always. |
| `gmail_send` | Sends an email; with `draft_id`, rewrites that draft as given and sends it, so what is sent is what the card showed. | **Always**, whatever the policy: it reaches other people. Never refuses it. |
| `calendar_list_events`, `calendar_get_event` | Read the events of a calendar the person owns. | Never, unless set to ask. |
| `calendar_create_event` | Adds an event. | With no guests, by the person's policy (ask by default). With guests, **always**: Google sends each an invitation. |
| `calendar_update_event` | Changes an event's title, time, place, description or guests. | With no guests besides the person, by the policy. With guests, or a change that invites or takes off guests, **always**. |
| `calendar_delete_event` | Deletes an event. | With no guests, by the policy. With guests (the organizer counts, when it is not the person), **always**. |

**A call that reaches other people** asks whatever the person set (decision [0351](decisions/0351-a-call-that-reaches-other-people-always-asks.md)). `tools.Tool.ReachesOthers` says so from the call's input; the turn passes it to `approval.Decide` as `Others`, which asks with the reason `others` under `always` and `ask`, and still refuses under `never`. `tools.Run` refuses a call that reaches others unless its approval is the person's own answer to it, so a standing policy can never cover one even if `Decide` were wrong. A scheduled run or a background task that wants to send waits for the card in its chat like any turn.

**The approval card says exactly what will happen.** Each tool's `Describe` writes it from the call's input, one fact to a line:

    Send an email to Test Person Two <person.two@example.org>.
    Subject: “Re: Lunch on Friday?”
    In reply to message m7e8f9.
    It says: “Yes, noon by the harbour works. See you on Friday.”

    Add “Tea at the lighthouse” to your calendar: Sat 28 Nov 2026, 16:00–17:00 (America/Chicago).
    Google sends an invitation to keeper@lighthouse.example.

**A change to an event names its guests, and is checked.** `calendar_update_event` and `calendar_delete_event` take the event's current `title` and its `guests` (everyone a change reaches besides the person: each guest, and the organizer when it is someone else). Before it changes anything the call reads the event and refuses, changing nothing, when the title or the guests differ, and says which guests to name. So a call that leaves guests out to run under `always` changes nothing, and a card can never show fewer people than the change reaches (decision [0353](decisions/0353-a-change-to-an-event-names-its-guests-and-is-checked.md)). The change is sent with the event's `If-Match` etag, so an event that changed since it was read is not overwritten, and with `sendUpdates=all` when anyone is reached, `none` when nobody is.

Inputs are checked before anything is sent: every address one address (net/mail), at most 50 recipients, a subject of one line (no header can be smuggled in), ids of a fixed shape, times as RFC 3339 with an offset, a local time with an IANA `time_zone`, or a date for an all-day event. An unknown field is an error the model reads.

## The sign-in

OAuth 2.0, the authorization code flow with PKCE (S256), `access_type=offline`, `prompt=consent` so a refresh token comes back, and `include_granted_scopes=true` (decision [0352](decisions/0352-google-sign-in-returns-to-a-page-and-keeps-the-refresh-token-in-the-vault.md)).

**Scopes**, the narrowest that do the work:

| Scope | For |
| --- | --- |
| `https://www.googleapis.com/auth/gmail.readonly` | `gmail_search`, `gmail_read`, and reading the message a reply answers. |
| `https://www.googleapis.com/auth/gmail.compose` | `gmail_draft` and `gmail_send`. It cannot read mail. |
| `https://www.googleapis.com/auth/calendar.events.owned` | The calendar tools, on calendars the person owns, not calendars shared with them. |
| `openid`, `email` | Naming the account on Settings → Connectors. They give no access. |

A person can untick a scope on Google's consent screen; a service whose scopes were not all granted is not added, and the screen says so.

**The steps.**

1. Settings → Connectors, **Connect Google**: the page posts `POST /api/v1/google/start` with the services. The server makes a state and a PKCE verifier, keeps them in the person's own `google_pending` table for `agents.google_sign_in_window` (10 minutes), and answers with Google's sign-in address.
2. The browser goes to Google, the person signs in and allows the scopes, and Google sends the browser to the return address with `code` and `state`.
3. **The return address is a page, not an API route**: the page's own origin plus `/settings/connectors/google`, or `agents.google_redirect_url` when the admin set one. The session cookie is SameSite=Strict, so a browser coming back from Google does not send it; the page, once loaded, posts `POST /api/v1/google/finish` with the code and the state, with the session, and takes them out of the address bar. No door route is added.
4. The server finds the state in the signed-in person's own table (another person's state is not there), takes it out (it is used once), trades the code with the verifier and the same return address at Google's token endpoint, stores the refresh token in the person's vault as `connector.google`, keeps the account's address and the granted scopes in `google_accounts`, and adds or switches on the `gmail` and `calendar` connectors for what was granted, with their tools. A connector of another kind already named `gmail` or `calendar` is left alone and named on the screen.

**Tokens.** The refresh token is in the vault, never in a setting, a log, a result, an event or the audit log. `reachFor` in `internal/connectors/open.go` is the one place a token is read: for a Google connector it reads the refresh token, the client's id (`agents.google_client_id`) and secret (`google.client_secret`), and gives the call a function that hands over an access token, kept in memory (a cache shared by the server's pool and routes) and refreshed a minute before it would stop working. The refresh token, the client's secret and every access token handed out are taken out of every result and error, as "(the token, removed)". A refresh Google refuses (`invalid_grant`) asks the person to connect again; **Check now** on the screen asks for a fresh token and keeps the problem.

**Disconnect** (`DELETE /api/v1/google`) revokes the refresh token at Google, removes the two connectors and their tools' policies, and takes the sign-in out of the vault and the tables.

**Where Google is reached.** Every Google address is a setting, and is dialled as web_fetch dials (`web.Dial`): public addresses only, checked as they connect, no proxy, no redirect. `GoogleOptions.OnThisMachine`, which only tests and `verify` set, lets the stand-in on this machine be reached; it is not a setting.

## The OAuth client

Keystone ships with no OAuth client. The owner makes one in the Google Cloud console and gives it to Keystone:

- its **id** as the tenant-admin setting `agents.google_client_id`, for each person who connects (`go run ./tools/tenant setting set <tenant-id> agents.google_client_id <id>`);
- its **secret** in that person's vault as `google.client_secret` (`go run ./tools/tenant secret set <tenant-id> google.client_secret`, from standard input).

Until both are there, Settings → Connectors says Google is not set up and names what is missing, and `start` answers `409 not_set_up`. The steps for the owner are in the [Google runbook](../runbooks/google.md).

## The routes

In `internal/connectors/googleroutes.go`, added to the connectors' list. Each acts for the signed-in person.

| Route | What it does |
| --- | --- |
| `GET /api/v1/google` | `GoogleStatus`: set up or not and what is missing, connected or not, the account, the granted scopes, each service (granted, added, on, its tools, a problem), and the return address. Never a token. |
| `POST /api/v1/google/start` | `{"services": ["gmail", "calendar"]}` (none means both). Answers `{url, redirect}`. |
| `POST /api/v1/google/finish` | `{"state", "code"}` or `{"state", "error"}`. Answers `GoogleStatus`. |
| `DELETE /api/v1/google` | Disconnect. Answers `GoogleStatus`. |

| Code | Status | Meaning |
| --- | --- | --- |
| `not_set_up` | 409 | The OAuth client's id or secret is missing. |
| `no_redirect` | 400 | No return address: the request had no page origin and `agents.google_redirect_url` is empty. |
| `bad_state` | 400 | The state was not started by this person, was used, or is older than the window. |
| `refused` | 400 | Google sent the browser back with an error, such as `access_denied`. |
| `google_refused` | 502 | Google's token endpoint did not take the code. The message names Google's short error code at most. |

## On the screen

**Settings → Connectors** gains a Google section above the list (`GoogleSettings.tsx`): when Google is not set up, what the admin must add and the return address to register; else **Connect Google** with a choice of Gmail and Calendar, then "Connected as" the account, each service with whether it was allowed, **Connect again** and **Disconnect**. The page at `/settings/connectors/google` finishes a sign-in and returns to the list. The `gmail` and `calendar` connectors are listed with the others, marked "Google", with their switch, outside mark and **Check now**; their token is the sign-in, so the screen offers no token field for them.

**Settings → Approvals** marks a tool whose calls can reach other people: "Asks every time it sends to or tells other people, whatever is set here."

**The approval card** shows each line of the summary on its own line.

## Settings

In `settings/agents.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.google_client_id` | empty | tenant-admin | The OAuth client's id. Empty: not set up. |
| `agents.google_redirect_url` | empty | tenant-admin | The return address, as registered on the client. Empty: the page's origin with `/settings/connectors/google`. |
| `agents.google_auth_endpoint` | `https://accounts.google.com/o/oauth2/v2/auth` | install | Google's sign-in page. |
| `agents.google_token_endpoint` | `https://oauth2.googleapis.com/token` | install | The token endpoint. |
| `agents.google_revoke_endpoint` | `https://oauth2.googleapis.com/revoke` | install | The revoke endpoint. |
| `agents.google_gmail_endpoint` | `https://gmail.googleapis.com/gmail/v1` | install | The Gmail API. |
| `agents.google_calendar_endpoint` | `https://www.googleapis.com/calendar/v3` | install | The Calendar API. |
| `agents.google_timeout` | 20s | install | How long one call to Google may take. |
| `agents.google_sign_in_window` | 10m | install | How long a sign-in may take from start to finish. |
| `agents.google_list_max` | 25 | install | The most messages or events one search or list returns. |

## Tables

In the migration `20261125T190000_google_connectors.sql`, which also makes the three connector tables again with `google` among the transports (SQLite cannot change a check in place); every row is kept.

| Table | Holds |
| --- | --- |
| `google_accounts` | One row at most: the account's address, the granted scopes, when it connected. |
| `google_pending` | Each sign-in started and not finished: its state, PKCE verifier, return address and services. Used once; gone after the window. |

## Checked by

- `go test ./internal/google`: the sign-in address (PKCE S256, offline, the scopes), a whole sign-in against the stand-in with the account's address, a refresh, a revoke and a refused refresh; a wrong verifier and a wrong client secret refused without repeating what was sent; search and read (plain, HTML stripped, attachments named), a calendar-only sign-in refused mail; a draft and a send of exactly what the card shows, a draft sent by id, a subject that needs encoding, and every refused input (a two-line subject, an address that is not one, 51 recipients, an unknown field, a draft id that is a path) never reaching the stand-in; listing and reading events; a personal event and an event with a guest (`sendUpdates` none and all); a move of a personal event; an update that leaves out, mis-names or mis-titles guests changing nothing; a delete of an event someone else organises naming the organizer.
- `go test ./internal/connectors`: the checks of a Google connector; the migration keeping a connector, its tools and its switches, with cascades; Connect Google through the routes: not set up and what is missing, no return address, an unknown service, PKCE and the return address from the page's origin, a state from nowhere and another person's state refused, the sign-in stored and both connectors added, a state used twice, `access_denied`, a partial grant adding Gmail alone, Check now, Disconnect revoking at the stand-in and leaving nothing; no route answer holding a token or the secret; the tools offered through the pool with their flags, an access token reused while fresh and refreshed when about to expire, another person's store refused; an echoed token taken out of results and errors; a revoked sign-in asking to connect again, in a call and in a check; the stand-in on this machine refused when not meant.
- `go test ./internal/turn` (`google_test.go`), the item's proof on the real routes: drafting and sending an email asks (the draft by policy, the send because it reaches others) and runs once approved, with the card's recipient, subject and words; a send set to always still asks, declined is not sent, approved is; a personal calendar change asks at its default and runs without asking set to always; an invitation set to always asks; an update set to always that leaves out the guests changes nothing; the approvals screen marks the tools that can reach others; no token in any event or audit entry. It was seen to fail with `Others` taken out of the turn's call to `Decide`.
- `go test ./internal/tools/...`: `Decide` asks for a call that reaches others under every policy and refuses it under never; `CheckReach` takes only an answer; `tools.Run` refuses a call that reaches others without an answer, under a standing approval, and as a tool that says it only reads.
- `verify`:
  - `calls recorded` has a step (`tools/verify/google.go`): the test tenant connects Google through the API against the stand-in and runs `fixtures/test-tenant/turns/google.json`: search, draft and send with `gmail_send` set to always (the draft asks with the reason `outside`, the send with `others`, nothing reaches the stand-in while it waits); a personal event with `calendar_create_event` set to always runs without asking; an invitation asks with `others`. Every call is in the audit log, and no token is in an answer, an event or the audit log.
  - `approval` checks the rule for `Others` over every policy, and that a standing approval never covers it.
  - `no keys in output` has a Google surface: a stand-in that takes the stored value as the refresh token and repeats it in its results, its errors and its token answers. It must be in no result, error, kept problem, audit entry or file.
  - `no secrets` knows Google's shapes of a client secret, an access token, a refresh token and an API key.
  - `offline`: `internal/google` and `internal/google/googletest` are on the list of folders that may use the network.
  - The `calls recorded` step was seen to fail with the `Others` case taken out of `Decide`: the send asked with the reason `outside` only, and the invitation was refused by `tools.Run`'s second guard instead of asking.
- In a browser, on November 25, 2026, against a server built from this branch on port 7993 by a harness (not committed) with a data folder inside the worktree and the stand-in Google on port 7994, whose sign-in page consented at once, signed in as the test tenant: the Google section offered Connect Google; pressed, the browser went to the stand-in and came back to `/settings/connectors/google`, which finished the sign-in, cleared the address bar to `/settings/connectors`, and said "Connected as person.one@example.com" with Gmail and Calendar on, and the two connectors listed as Google, signed in, with 4 and 5 tools. The Approvals screen marked `gmail_send` as asking every time it sends to other people; it and `calendar_create_event` were set to always there. The recorded Gmail turn searched, then asked for the draft ("Asking because this chat read outside content, from gmail_search") with each line of the card on its own line, then asked for the send, set to always ("Asking because this reaches other people…"), and sent once approved. The personal event ran without asking; the invitation asked, and "Not now" left its step "declined". Disconnect asked once more and left Connect Google. In dark at phone width, the connected section and, after a restart with no client, the not-set-up section with what the admin adds and the return address. The console's only error was the sign-in probe's 401. The harness and the data folder were removed.

## Not here yet

- Attachments (read or sent), labels, archiving and marking read; threads beyond a reply's `In-Reply-To`.
- Calendars the person does not own, and editing every occurrence of a recurring event (an occurrence is an event of its own).
- Choosing per call whether guests get an email: a change that reaches guests always tells them.
- Google's push notifications; a check for new mail is a scheduled task's search.

## Provisional

- **No real Google has been reached.** The calls follow Google's documented REST shapes, and the stand-in was written from the same reading. The owner's first sign-in is the live check.
- **The screens** have no mockup and wait on the agent mockups (P1-05).
