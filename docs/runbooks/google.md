# Switching on Gmail and Google Calendar

Keystone ships with no Google OAuth client. The owner makes one in his own Google Cloud project, gives Keystone its id and secret, and then each person connects their own Google account from Settings → Connectors. How it works is in [`docs/architecture/google.md`](../architecture/google.md). In the steps below, `keystone.example` stands for the address Keystone is reached at; the real hostname is kept out of this repo.

The Google Cloud console's screens move from time to time. The names below are the console's as of November 2026.

## 1. Make the OAuth client (once, in the Google Cloud console)

1. Pick or make a project at `console.cloud.google.com`.
2. **APIs and services → Library**: enable the **Gmail API** and the **Google Calendar API**.
3. **Google Auth Platform → Branding**: an app name (Keystone) and your own support and contact address.
4. **Audience**: **External** for a personal Google account (Internal only exists for a Google Workspace organisation, and then needs none of the rest of this step). Add each person who will connect as a **test user**.
   - In **Testing**, Google lets test users sign in without a review, but a sign-in that holds Gmail or Calendar scopes stops working after **seven days**, and Keystone will say "connect Google again".
   - To avoid that, press **Publish app** to put it **In production** without asking for verification. Google then shows an "unverified app" warning at each sign-in, which you pass with **Advanced → Go to Keystone**; an unverified app is limited to 100 users, plenty for a household.
5. **Data access**: add the scopes Keystone asks for, and no others:
   - `https://www.googleapis.com/auth/gmail.readonly`
   - `https://www.googleapis.com/auth/gmail.compose`
   - `https://www.googleapis.com/auth/calendar.events.owned`
   - `openid` and `.../auth/userinfo.email`
6. **Clients → Create client**: application type **Web application**, a name such as Keystone. Under **Authorized redirect URIs** add, exactly:
   - `https://keystone.example/settings/connectors/google` (through the tunnel, from the phone or anywhere), and
   - `http://localhost:9080/settings/connectors/google` if you will ever connect from a browser on the machine Keystone runs on. Google accepts plain `http` for `localhost` only, so connect through `localhost` and not `127.0.0.1` there.

   No JavaScript origin is needed. Create it, and keep the window with the **client id** and **client secret** open.

## 2. Give Keystone the client (for each person who will connect)

The tenant id comes from `go run ./tools/tenant list`. From the main checkout, with the data folder Keystone uses:

    go run ./tools/tenant setting set <tenant-id> agents.google_client_id <the client id>

Copy the client secret, then pipe it in, so it is never on the command line or in the shell's history:

    Get-Clipboard | go run ./tools/tenant secret set <tenant-id> google.client_secret

Repeat both for each person. One client serves the whole household. Nothing needs a restart: Keystone reads both when the person connects. If the page ever says it cannot tell the return address, set it outright:

    go run ./tools/tenant setting set <tenant-id> agents.google_redirect_url https://keystone.example/settings/connectors/google

## 3. Connect (each person, in the browser)

1. **Settings → Connectors**. The Google section now offers **Connect Google**.
2. Leave **Gmail** and **Calendar** ticked (or untick one), and press **Connect Google**.
3. Google asks you to choose your account and to allow each scope. Allow them all: a box left unticked leaves that service off, and the page says so.
4. Google sends you back to Keystone, which says "Connected as" your address and lists the `gmail` and `calendar` connectors with their tools.

Then, in a chat: "Search my mail for the harbour office" should list messages; asking Keystone to send an email shows a card with the recipients, the subject and the words, and nothing is sent until you press **Approve**, whatever Settings → Approvals says.

## 4. Disconnect

**Disconnect** in the Google section revokes the sign-in at Google, removes the two connectors and their tools' policies, and takes the sign-in out of your vault. You can also remove Keystone's access at `myaccount.google.com/permissions`; Keystone then asks you to connect again.

## Taking over a sign-in from New Keystone

If New Keystone on this machine is already connected to Google Calendar, Keystone can take that sign-in over without a new consent, for Calendar only (Gmail needs the Gmail API, its two scopes and Keystone's redirect address on the client, steps 1 to 3). New Keystone keeps the client as `calendar-client.dpapi` in its settings folder and the sign-in as `google-calendar.dpapi` in its state folder, both readable only by the Windows account that made them.

1. Give Keystone the same client (step 2), from `calendar-client.dpapi`.
2. Pipe the refresh token from `google-calendar.dpapi` into `go run ./tools/tenant connector google <tenant-id> <account address> openid email https://www.googleapis.com/auth/calendar.events.owned`. Read the file with Windows' own `ProtectedData` in a script, so the token is never shown.
3. `go run ./tools/tenant connector check <tenant-id> calendar` asks Google with it. Settings → Connectors then shows Calendar connected.

Both programs can use the sign-in side by side. Disconnecting in either one revokes it at Google for both.
## When something goes wrong

| The page says | What to do |
| --- | --- |
| Google is not set up on this install | Step 2 is missing for this person: the setting, the secret, or both. |
| Google did not accept Keystone's OAuth client | The client id or secret was mistyped, or the client was deleted. Store them again. |
| `redirect_uri_mismatch` on Google's page | The return address shown in the Google section is not registered on the client exactly. Add it in step 1.6. |
| Google no longer accepts your sign-in | The sign-in was revoked, or it expired after seven days in Testing. Connect again, or publish the app (step 1.4). |
| This sign-in was not started here, or took too long | Press Connect Google again; a sign-in has ten minutes. |
