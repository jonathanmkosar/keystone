# Web interface

Folder: `web`

The web interface is a React and TypeScript app, built with Vite and compiled into the binary. The source is in `web/app/`. The built files are in `web/dist/`. `web/embed.go` embeds everything in `web/dist/`, and `web.Files()` hands it to the server with `index.html` at its root. `keystone serve` serves it (see [cli.md](cli.md)).

`web/dist/` is committed, so building Keystone needs Go only. Decision [0005](decisions/0005-web-built-files-committed.md) says why. Node is needed only by someone changing the interface.

## Building it

    cd web/app
    npm install
    npm run build

`npm run build` checks the types and then writes `web/dist/`, emptying it first. Commit the source change and the new `web/dist/` together, then run `go run ./tools/rebuild` so the binary carries them. `npm run dev` runs Vite's own server and passes every `/api` call to Keystone on `127.0.0.1:7900`, so the app can be worked on against a running `keystone serve`.

The packages are react, react-dom and react-router, with vite, its React plugin and typescript to build. There is no component kit and no Markdown package (decision [0150](decisions/0150-the-markdown-reader-is-written-here.md)).

## What is there today

The web shell (P4-01), the frame every screen sits in, and the chat screen (P4-04), which has its own section below.

| Part | File | What it does |
| --- | --- | --- |
| Entry | `src/main.tsx` | Mounts the app under the router. |
| Session and routes | `src/App.tsx` | Asks the server who is signed in, then shows one of four states: loading, the server is not ready, sign-in, or the shell with its routes. |
| Server calls | `src/api.ts` | Every call to the Keystone server. See "The API it calls". |
| Sign-in | `src/SignIn.tsx` | Lists the people on the install. The person picks their own and types a password. Also the "server is not ready" screen. |
| Frame | `src/Shell.tsx` | The sidebar with its starred and recent chats, the header and the main area. |
| Screens | `src/pages.tsx` | The list of every chat, the placeholder for screens later items build, and the page-not-found screen. |
| Chat | `src/Chat.tsx`, `src/Composer.tsx`, `src/Conversation.tsx`, `src/ChatTitle.tsx` | New chat, one chat's conversation, the box a message is written in, and the title with its menu. |
| Chat state | `src/chats.tsx`, `src/turns.ts`, `src/layout.ts` | The list of chats, the replies that are arriving, and the step from messages to what is drawn. |
| Model picker | `src/ModelPicker.tsx`, `src/modelPicks.ts`, `src/model-picker.css` | The menu in the composer that picks the model for a chat, and the picks and held chats it keeps. See "The model picker". The component imports its stylesheet, which is built into `keystone.css` with the others. |
| Reading a reply | `src/Markdown.tsx`, `src/sse.ts` | A model's Markdown as React elements, and the reader for server-sent events. |
| Theme | `src/theme.ts`, `src/ThemeSwitch.tsx`, `public/theme.js` | The choice of dark, light or follow the system. |
| Mark and icons | `src/Mark.tsx`, `src/Icon.tsx` | The mark and the line icons, drawn as in the mockups. |
| Styles | `src/styles.css`, `src/chat.css`, `vite.config.ts` | The frame, the chat screen, and the plugin that turns the design tokens into CSS variables. Both stylesheets are built into `keystone.css`. |

### Routes

Addresses are real paths (decision [0071](decisions/0071-client-side-routes-are-real-paths.md)).

| Path | Screen |
| --- | --- |
| `/` | New chat: the mark, a greeting by time of day and the person's name, and a composer. The first message makes the chat. |
| `/chats` | Every chat as a table, with search, filters, sorting, and select and delete (P4-06): see "The Chats screen" below. |
| `/chats/<id>` | One chat. |
| `/signin` | Goes to `/` when signed in. Signed out, every path shows sign-in and keeps its address. |
| `/search` | Goes to `/chats` with its search box ready, as the sidebar's magnifier does. |
| `/agents` | A placeholder saying the screen is not built yet. |
| `/operations` | Operations and Monitor (P11-03): see its section below. |
| anything else | Page not found, with a link back to the start. |

### The frame

Laid out as in `MainDark.html` and `MainLight.html`. The sidebar is 272 pixels wide and holds the mark and wordmark, a magnifier and a button to close it, New chat, the six places, the starred chats, the thirty most recent of the others with a link to the rest, and the signed-in person with a sign-out button. The theme switch left the sidebar at the owner's word (November 27, 2026): it is under Settings, General, and on the sign-in screen. Search and Chats are not rows either, also at his word: the magnifier in the sidebar's head opens the Chats screen with the cursor in its search box, and on a phone it is a round button (`.search-button` in `styles.css`). On a phone the drawer's head is the wordmark alone, at 30 pixels, and that round magnifier: no mark and no close button. The drawer takes all but 72 pixels of the screen (at most 360), and the page slides over into that strip with its edge rounded, its menu button still showing; a tap there closes the drawer. The main area has a header with the screen's title, and the content below it. On a chat the title is a button that opens the chat's menu.

Below 760 pixels wide the sidebar becomes a drawer over the page, opened from the header and closed by its own button, by the dimmed area beside it, or by moving to another screen.

### Colours and the theme

Every colour is a CSS variable generated from `design/tokens-draft.json` when the interface is built (decision [0070](decisions/0070-design-tokens-become-css-at-build-time.md)). No colour value is written in the app's stylesheets or components, with two exceptions the tokens have no name for: the dimmed area beside the drawer and the shadow under the composer and the menu are black at part strength. The person's bubble is halfway between the raised ground and the line, mixed in CSS, which is the mockups' value in both themes.

The theme switch offers dark, light and system. Dark leads, as in the design. The choice is kept in the browser under `keystone.theme`. `public/theme.js` applies it before the page paints, so a person who chose light on a dark system sees no dark flash; it is a separate file because the server's content security policy allows no inline script.

### Fonts

Source Serif 4 and IBM Plex Sans are bundled: six variable WOFF2 files for Latin and Latin Extended in `src/fonts/`, about 270 KB, with their rules in `src/fonts.css` and their licence beside them, served by Keystone itself (decision [0301](decisions/0301-the-reading-and-interface-fonts-are-bundled.md)). Fraunces and IBM Plex Mono are named and fall back to system fonts (decision [0072](decisions/0072-fonts-are-named-not-bundled.md)). Replies are 17px with headings in the sans at semibold (decision [0300](decisions/0300-reading-text-for-the-fonts-people-have.md)).

## The chat screen

P4-04, built against the P4-03 contract in [turns.md](turns.md). It is provisional: see "Provisional" below.

### What a person can do

- **Start a chat.** New chat shows the greeting and a composer. Sending makes the chat, sends the message and opens `/chats/<id>`. If the chat cannot be made, the message goes back into the box.
- **Open a chat.** The sidebar and `/chats` list them. The conversation is the chat's current branch.
- **Send and read.** Enter sends, and Shift with Enter makes a new line. The reply arrives as it is written: thinking and tool calls as steps in a card, then the text. While it arrives the send button is a stop button.
- **Stop.** The stop button asks the server to stop the turn. What had arrived is kept, with a line saying the person stopped it.
- **Rename, star, delete.** From the menu under the chat's title. Delete asks once more first.
- **Copy.** A whole reply, or one code block.

### How a reply is drawn

`src/layout.ts` turns messages into items, and `src/Conversation.tsx` draws them.

- A person's message is a bubble on the right. Files and images in it are shown by name.
- The record keeps a tool's results in a message with the role `user`, and a reply that used tools is several assistant messages. On the screen that is one reply: its steps in order, then its text.
- A thinking block is a step that opens to show the thinking. A reply that this page watched arrive says how long the thinking took; one read from the record says only that it thought, because the record does not keep the time. A model that kept its thinking to itself gets a step with nothing to open.
- A tool call is a step with a readable line, that opens to show what was sent and what came back. See "Tool steps" below.
- Text is read as Markdown by `src/Markdown.tsx`: paragraphs, headings, lists, quotes, rules, tables, code blocks with a copy button, and inside a line code, bold, italic, struck text and links. It builds React elements and never sets HTML from text. A link opens in a new tab, and only an `http`, `https` or `mailto` address becomes one. A code block with no closing fence runs to the end, which is what a reply looks like part of the way through.
- A stored reply that did not end by the model finishing says so under its text: the person stopped it, it broke off, it reached the length limit, or the model declined.

### Tool steps (P7-01)

Since the tool loop a reply's tools really run, and each step is drawn as it happens ([tool-loop.md](tool-loop.md)):

- **A readable line.** `src/steps.ts` turns a call's name, input and result into a sentence: "Searching your chats for “lighthouse”" while it runs, "Searched your chats for “lighthouse”" with "3 matches" on the right once it has answered, "Read “Planning the lighthouse walk”", "Checked the date and time". A tool with no words of its own reads "Used <name>: " and the first plain values it was sent. Decision [0204](decisions/0204-the-screen-writes-the-steps-line.md).
- **Running.** From its `tool_call` event to its `tool_result`, the row is in the present tense, pulses as thinking does, and says "running" on the right.
- **No result.** A row whose result is an error says what the model asked for, with a tag: "not run" or "stopped" in the plain tag, "failed" or "no such tool" in the error colour.
- **Collapsed by default.** A row opens to show the tool's name, what it was sent and what came back.
- **The step limit.** The note a turn writes at its step limit (a message with the stop reason `step_limit`) is drawn in a dashed box with the stop mark, as Keystone's words and not the model's.
- A reply's text between steps stays between them, in the order it was written.

It is drawn from the record, so a reload shows the same. Built from the tokens in `src/chat.css`, after the approved step rows of `MainDark.html`. Provisional until the owner approves the agent mockups (P1-05): the running row, the rows with no result, the open body and the step limit's note have no mockup.

### Web search and citations (P7-03)

The web tools' steps and the numbers a reply cites them by ([web-search.md](web-search.md)):

- **Rows.** `web_search` reads "Searching the web for “harbour museum opening hours”" while it runs, then "Searched the web for “…”" with "2 results" (or "1 result", "nothing found") on the right. `web_fetch` reads "Reading museum.example", then "Read “Harbour Museum: opening hours”" with the site on the right, or "Read museum.example" for a page with no title. Both have a globe icon. A search with no key says "Search the web for “…”" with "not set up" in the plain tag, and one in a chat held for sensitive content says "off in this chat"; the open row has the server's sentence.
- **Citations.** `src/citations.tsx` reads a reply's sources out of its web steps' results, in the form the server writes them: blocks of `[n] title` and the address, after the `[Outside content]` paragraph, read until the first block that is not one. The Markdown reader (`src/Markdown.tsx`) has one more pattern, `[n]` not followed by `(`, drawn by `Cite`: a small chip with the number that links to the source in a new tab, with the title and the site on hover and "Source n: title" for a screen reader. A `[n]` that names no source of its reply stays plain text, so a reply that used no web tool reads as it was written. `[1][2]` is two chips. Only an `http` or `https` address becomes a link.
- **Which sources.** Each reply has its own: `Conversation.tsx` wraps a reply in `CitationSources` with the sources of that reply's steps. A number keeps the first source given it. The chips are the same after a reload, because they are drawn from the record.

Built from the tokens in `src/citations.css`. There is no mockup for the chip yet, so it is provisional with the agent mockups (P1-05).

### Browser use (P10-03)

The browser's six steps ([browser-use.md](browser-use.md)): `steps.ts` reads "Opening harbour.example in the agent's browser", then "Opened “Harbour notices”" with the site; "Read “Harbour notices”" with the count of elements; "Click “Submit” in the agent's browser" on an approval card and "Clicked “Submit”" after; "Typed “Ada” into “Your name”"; "Took a screenshot"; "Went back a page". A refusal of the guard's reads "refused", and a call with the switch off "browser use off". Under a step that took a screenshot, `BrowserShot.tsx` shows it, as a link to the full size, at most 480 pixels wide and the full width on a phone (`browser.css`, from the tokens). It reads the screenshot's id only from the result's second line, which the server writes before any of the page's words. Nothing is added to the composer; the switch is a setting, drawn by Settings. Provisional with the agent mockups (P1-05).

### A turn, from send to end

`src/turns.ts` keeps the replies that are arriving, one for each chat, outside the screens (decision [0151](decisions/0151-arriving-replies-live-outside-the-screens.md)). So a reply goes on arriving while the person looks at another chat.

1. `POST /chats/{id}/turns` is sent with the text, and its answer is the stream. The person's message is shown at once, and takes its place in the record when the stream's `message` event brings it.
2. `thinking`, `text` and `tool_call` events build a draft reply, by `index`. A `message` event with the role `assistant` replaces the draft with the stored message. `tool_result` fills in its step.
3. `title` sets the chat's title in the header and the sidebar.
4. `error` is shown under the reply in a red-bordered box. The server's message is used as it is. An error that comes with none gets fixed words by its code.
5. `end` hides the stop button and ends the reading, without waiting for the server to close the stream. The screen then reads the chat again, because the record is the truth, and joins the next turn if one is waiting.

**A dropped stream.** A stream that closes without `end` lost its connection. The page waits a moment and calls `GET /chats/{id}/turns/{turn}/events` with the last id it saw in `Last-Event-ID`, and the reply goes on from the next event. It tries six times, waiting longer each time, and starts counting again whenever an event arrives. After that it says the connection was lost, and that the reply may have gone on without the page.

**Joining a turn.** A chat that is opened while one of its turns has not ended (the page was reloaded, or the message came from another window) reads that turn's events from the first.

**No answer to a send.** If the send gets no answer at all, the page cannot know whether the message arrived. It reads the chat, and joins the turn if there is one.

The stream is read with `fetch`, not `EventSource`: the first stream is the answer to a `POST`, and the page needs to see a refused stream's status and choose when to come back. `src/sse.ts` is the reader, and it was checked against `GET /api/v1/stream/test` with and without `Last-Event-ID`.

### The content security policy

The server sends `default-src 'self'`. The chat screen has no inline script and no `style` attribute in its markup, and loads nothing from another site. One thing sets a style from script: the composer's height, set on the element as the text grows, which the policy allows.

### The model picker

The second pass of P4-04. The button sits in the composer's row just before the send button, as in `MainDark.html` and `MainLight.html`: the model's label and a chevron. The mockups do not draw the menu it opens, so the menu is built from the tokens like the chat's own menu, and opens upwards. On a phone it is as wide as the composer.

**What it lists.** In a chat, the models the chat's model route returns (`GET /chats/{id}/model`, `models`), in its order, each by its label with a mark for its class: `local`, `hosted` or `trusted`. Local and trusted are marked in the "ok" colour, as on the Models and keys screen, because they may read sensitive content. A tick is on the model that will answer. A sentence at the foot says where that model comes from (this chat's last turn, the default setting, the first in the registry, or the test tenant), and a link goes to Models and keys.

**What cannot be picked**, shown greyed with the reason under the label:

- A model whose key is missing: `GET /api/v1/models` says `key_present` is false. Marked "key missing" in the error colour. If the model that will answer is one of these, the button shows a warning mark and says so.
- In a chat that is held to local or trusted models, every hosted model: "Not for this chat: it holds content from a sensitive space, which only a local or trusted model may read." See "A held chat" below.

**Keys.** Arrow keys move between the models that can be picked, Home and End go to the ends, Enter picks, Escape and a click outside close the menu.

**The new chat.** There is no chat yet, so there is no model route. The picker reads `GET /api/v1/models` and the `chats.default_model` value from `GET /api/v1/settings`, and works out the server's choice in the server's own order (`choice` in `internal/turn/model.go`): the default when it names a model a chat can use, or else the first such model by name. A chat can use an entry the registry does not refuse and that is not an embedding model; the server also asks that its adapter is in the build, which all four are today. When the registry holds none, as for the test tenant, there is no picker on the new chat screen and the first message goes as before.

**The test tenant.** Its model route answers with one model, "Recorded turns (test data)", and the source `test`. The picker shows that one entry, ticked, with a sentence that its replies are recorded test data whatever is picked. The server ignores a turn's `model` for it.

**The footnote** under the composer names the model that will answer, as before, and now follows a pick at once: `src/Chat.tsx` reads it from the same store as the picker.

#### How the choice sticks

The server keeps it. A turn that names a `model` and opens it writes that name on the turn's row (`Runner.work` in `internal/turn/runner.go`, as the turn leaves the queue). The chat's next turn that names none, and the chat's model route, take the model of the chat's newest turn that has one (`lastModel` in `internal/turn/store.go`, source `chat`). So the browser keeps nothing for good, and the choice follows the chat to any browser. Decision [0240](decisions/0240-a-pick-goes-with-the-next-message.md).

What the page does, in `src/modelPicks.ts`:

1. A pick is kept for the chat, outside the screens (so it survives opening another chat), and the button and the footnote show it at once. Picking the model the server would use anyway takes the pick back.
2. The next message names it: `POST /chats/{id}/turns` with `{"text", "model"}`. The pick is marked as sent.
3. When that turn ends, the picker reads the model route again and lets the pick go. From then on the server's answer is what it shows. Messages after that name no model.
4. A pick made on the new chat screen moves to the chat its first message makes, and goes with that message.

What the server does with a named model, so what the person sees:

- The model opens, whatever happens after: the chat keeps it. A reply that then fails at the provider (a wrong key, an endpoint that cannot be reached) still leaves the chat on that model, and the failure is shown under the reply.
- The model does not open (`model_unavailable`) or the turn is refused as `sensitive`: no model is written on the turn, the chat stays on the model it had, and the picker goes back to it when it reads the route.
- In a held chat whose fallback chain has a local or trusted model, a hosted pick is left out of the route and the fallback answers. The turn then records the fallback, and the picker shows it after the turn.

#### A held chat

A chat that was sent content from a sensitive space is held to local and trusted models for good ([knowledge-recall.md](knowledge-recall.md), decision [0211](decisions/0211-a-chat-sent-sensitive-content-is-held.md)). **The model route does not say so**: `ModelChoice` has no field for it, `reason` is set only when there is no model at all, and `source` says where the model came from. A held chat still lists its hosted models, and the server refuses a turn on one only when it is sent.

So the page learns it from that refusal. When a turn of the chat fails, the picker reads the turn (`GET /chats/{id}/turns/{turn}`) and, if its error code is `sensitive`, marks the chat as held. A marked chat shows its hosted models as not available, with the reason, and a sentence in the menu. The mark is kept in the browser's local storage under `keystone.chats.held`, a list of chat ids, because a held chat stays held. Decision [0241](decisions/0241-a-held-chat-is-learned-from-a-refused-turn.md).

What it cannot do: mark a chat before a turn has been refused, mark it in another browser, or notice a hosted pick that a local fallback answered without a refusal. The server can close all three by saying in `ModelChoice` that the chat is held: see "What comes next".

#### Its calls

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/chats/{id}/model` | `chatModel` | The list and the current choice, when a chat opens, when the menu opens, and after each turn ends. |
| `GET /api/v1/models` | `listModels` | Which models have their key stored, and the new chat's list. |
| `GET /api/v1/settings` | `listSettings` | `chats.default_model`, for the new chat. |
| `GET /api/v1/chats/{id}/turns/{turn}` | `getTurn` | Whether a failed turn was refused as `sensitive`. |
| `POST /api/v1/chats/{id}/turns` | `sendMessage` | With `model` when a pick waits to be sent. |

### Not built yet

Edit and retry with the branch switcher, feedback, attachments, the tools menu, the thinking switch, the context meter, share, the turn inspector, citations, sub-agent lanes, search, keyboard shortcuts beyond Enter and Escape, and motion beyond the mark that follows arriving text. A second message cannot be sent while a reply is arriving, though the server would queue it.

### Provisional

- The tokens are a draft, and the chat mockups wait for the owner's approval as a group (P1-04).
- The screen has run against Keystone's own chat routes only briefly, on a build of the P4-03 branch made outside the repo: see the journal, interface 002. Listing, opening, sending, the streamed reply, the title, rename, star and delete ran there. The dropped stream, the reload in the middle of a reply, stop and a failed turn were seen only against a stand-in for the contract, because the test tenant's recorded replies end too soon to interrupt by hand.
- New chat follows `Home.html`, which is a draft in the first round's colours.
- The model picker's button follows the mockups. Its menu, the class marks, the greyed rows with their reasons and every sentence in it have no mockup. It has run against Keystone's own routes with invented models whose endpoints answer nothing, and with the test tenant; never with a model that replies. See the journal, interface 004.

## The settings screens

P11-01. The routes and the screens are described together in [settings-screens.md](settings-screens.md). This section is what they add to the app.

| Part | File | What it does |
| --- | --- | --- |
| Screens | `src/Settings.tsx` | The seven screens under `/settings`, with their tabs: Profile, Appearance, Models and keys, General, and three that say they are not built yet. |
| Styles | `src/settings.css` | Cards, rows, forms, the switch and the pills. Built into `keystone.css` with the other two stylesheets. Every colour is a token. |
| The way in | `src/Shell.tsx` | The person's name at the foot of the sidebar is a link to `/settings`. |
| The theme | `src/App.tsx` | A theme chosen while signed in is also stored as the `appearance.theme` setting, and a stored theme is read at sign-in (decision [0192](decisions/0192-the-theme-is-a-setting-once-chosen.md)). |

`/settings` and every path under it show these screens. A path under it that is no screen goes to `/settings/profile`.

General has no code for any one setting. It draws a control for each setting `GET /api/v1/settings` marks `changeable`, chosen by the setting's type and options (decision [0191](decisions/0191-the-general-screen-is-drawn-from-the-route.md)).

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/settings` | `listSettings` | General, and the stored theme at sign-in. |
| `PUT /api/v1/settings/{key}` | `setSetting` | A control on General, and the theme switch. |
| `DELETE /api/v1/settings/{key}` | `resetSetting` | "Reset to default" on General. |
| `GET /api/v1/secrets` | `listSecrets` | The stored key names. |
| `PUT /api/v1/secrets/{name}` | `setSecret` | Storing a key. The answer is not read, and holds no value. |
| `DELETE /api/v1/secrets/{name}` | `deleteSecret` | Removing a key. |
| `GET /api/v1/models` | `listModels` | The models, and the choices the form offers. |
| `POST /api/v1/models` | `addModel` | Adding a model. |
| `PUT /api/v1/models/{name}` | `replaceModel` | Saving an edited model. |
| `DELETE /api/v1/models/{name}` | `removeModel` | Removing a model. |
| `GET /api/v1/profile` | `readProfile` | The profile's text and its limit. |
| `PUT /api/v1/profile` | `writeProfile` | Saving the profile. |

A refusal with a 4xx status other than 404 carries the server's words, and the screen shows them as a sentence. A 401 on any of these calls shows sign-in.

The screens are provisional: there is no settings mockup, and three open owner questions shape them. [settings-screens.md](settings-screens.md) lists what rests on each.

## Approvals (P7-02)

The approval card and the Approvals settings screen. The server's side, the rule and the routes are in [approvals.md](approvals.md). Provisional until the owner approves the agent mockups (P1-05).

| Part | File | What it does |
| --- | --- | --- |
| The card | `src/Approval.tsx`, `src/approval.css` | Keeps the `approval` events of every stream the page reads, by chat, outside the screens, and draws a card under the conversation for each call that waits: the title, why it asks, the server's summary, what it will be sent (opened on request), and **Approve** and **Not now**. Once answered, a line says what was answered. A chat's cards are forgotten when its turn ends. From the approved card in `MainDark.html` and `MainLight.html`, built from the tokens. |
| The waiting step | `src/Conversation.tsx` | The step row of a call that waits says what the model asked for, with "waiting for you", and does not pulse. |
| Step words | `src/steps.ts` | `toolTitle` for the card's title, and "declined" for a call the person declined. |
| The policy list | `src/ApprovalSettings.tsx` | `/settings/approvals`: every tool, whether it only reads and whether its result is Keystone's own, a select of always, ask and never, and "Reset to default". It replaces the placeholder. |
| The way in | `src/Chat.tsx`, `src/api.ts`, `src/Settings.tsx` | One line in `Chat.tsx` draws the cards. One line in `api.ts`'s stream reader passes `approval` events to `onApproval`, and the calls are added at the end of the file. One route in `Settings.tsx`. |

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `POST /api/v1/chats/{id}/turns/{turn}/approvals/{approval}/approve` | `answerApproval` | Approve on a card. |
| `POST /api/v1/chats/{id}/turns/{turn}/approvals/{approval}/decline` | `answerApproval` | Not now on a card. |
| `GET /api/v1/tools` | `listToolPolicies` | The Approvals screen. |
| `PUT /api/v1/tools/{name}/policy` | `setToolPolicy` | A policy chosen on that screen. |
| `DELETE /api/v1/tools/{name}/policy` | `resetToolPolicy` | "Reset to default". |

A reload in the middle of a wait joins the turn's stream from its first event, so the card comes back. A 409 on an answer says the call was already answered or its reply has ended. The screens have no inline script and no `style` attribute.

Seen in a browser on November 25, 2026, against a server built from this branch on port 7931 with a harness that registered test tools: the card asking after outside content, the input opened, Approve, a card that came back after a reload, Not now leaving the row "declined", and the policy list setting a tool to always. In light and dark, and at phone width.

## Connectors (P7-04)

Settings → Connectors and the tools menu in the composer. The server's side, the rules and the routes are in [connectors.md](connectors.md). There is no mockup for either: provisional with the agent mockups (P1-05).

| Part | File | What it does |
| --- | --- | --- |
| The screen | `src/ConnectorSettings.tsx`, `src/connectors.css` | `/settings/connectors`: each connector with its label, address or command, pills for its kind, its token and its outside mark, the tools it listed (each "reads" or "changes") or why it could not be reached, a switch, "Results are outside content", **Check now**, **Store a token** or **Replace the token**, and **Remove**, which asks once more. A form adds a remote connector: it is added unchecked, its token is stored as `connector.<name>`, and then it is reached, so a refused connector leaves no token behind. A section says how a program on this machine is added, from the command line. It replaces the placeholder. |
| The tools menu | `src/ConnectorMenu.tsx`, `src/connectors.css` | A plug and the number of connectors on, before the model picker. Its menu has a small switch for each connector in this chat, with its number of tools, "Off in Settings" or "Could not be reached", and a link to Settings → Connectors. On the new chat screen the switches are kept in the page, by person, and `applyNewChatConnectors` sends them for the chat the first message makes before the message is sent. A person with no connectors sees no button. It shares the model picker's menu classes. |
| Step words | `src/steps.ts`, `src/Icon.tsx` | "Loaded notes_search, notes_add" with "2 tools" for `load_tools`, and a plug icon. A connector's own tools read "Used notes_search: query “harbour”", as any tool this file does not know. |
| The way in | `src/Chat.tsx`, `src/api.ts`, `src/Settings.tsx` | In `Chat.tsx`: the import, the menu beside each of the two model pickers, and one line in `begin` that applies the new chat's switches. In `api.ts`: the calls, after the tool policies. In `Settings.tsx`: the import and the route. |

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/connectors` | `listConnectors` | The screen, and the new chat's menu. |
| `POST /api/v1/connectors` | `addConnector` | The add form, with `check: false`. |
| `PUT /api/v1/connectors/{name}` | `changeConnector` | The switch, the outside mark, and a token's name after one is stored. |
| `DELETE /api/v1/connectors/{name}` | `removeConnector` | Remove. |
| `POST /api/v1/connectors/{name}/check` | `checkConnector` | Check now, and after the add form. |
| `PUT /api/v1/secrets/{name}` | `setSecret` | A token. It is sent once and leaves the page whether or not it was stored. |
| `GET /api/v1/chats/{id}/connectors` | `chatConnectors` | A chat's menu. |
| `PUT /api/v1/chats/{id}/connectors/{name}` | `switchChatConnector` | A switch in a chat's menu, and the new chat's switches. |

Seen in a browser on November 25, 2026: see "Checked by" in [connectors.md](connectors.md).

## Connect Google (P12-02)

The Google section at the top of Settings → Connectors (`GoogleSettings.tsx`), and the page Google sends the browser back to, `/settings/connectors/google`, which is the same screen: it posts the code and state once, replaces the address with `/settings/connectors` so the code leaves the bar and the history, and reloads the list. The server's side is [google.md](google.md). `ConnectorSettings.tsx` draws a Google connector as "Google", "signed in", with no token field. `ApprovalSettings.tsx` marks a tool with `reaches_others`. The approval card shows each line of its summary on its own line (`approval.css`). `steps.ts` has words for the nine Gmail and Calendar tools, with a mail and a calendar icon.

| Call | Function | Where |
| --- | --- | --- |
| `GET /api/v1/google` | `googleStatus` | The Google section. |
| `POST /api/v1/google/start` | `startGoogle` | Connect Google, Connect again: the browser then goes to the answer's `url`. |
| `POST /api/v1/google/finish` | `finishGoogle` | The return page. |
| `DELETE /api/v1/google` | `disconnectGoogle` | Disconnect, after asking once more. |

Seen in a browser on November 25, 2026: see "Checked by" in [google.md](google.md).

## The turn inspector and pins (P5-04)

The panel that shows what went into a turn, and Pin beside Copy on every message. The server's side and the panel's contents are in [inspector.md](inspector.md). Provisional until the inspector mockup (P1-06): there is none, so it is built from the tokens and the patterns of the chat screen (decision [0273](decisions/0273-the-inspector-is-a-panel-opened-from-a-reply.md)).

| Part | File | What it does |
| --- | --- | --- |
| The panel | `src/Inspector.tsx`, `src/inspector.css` | Keeps which turn is open, outside the screens, and draws the panel on the right: the turn's totals, what the next message leaves out, each call's parts with their tokens, the summary read only, usage, and what recall found. Leave out next turn, Put back and Unpin. Closes with Escape. |
| The actions | `src/MessageActions.tsx` | Pin and Inspect beside Copy on a reply, Copy and Pin under a person's message. |
| Pins | `src/pins.ts` | A chat's pinned messages, by chat, outside the screens, read the first time a screen asks. |
| The way in | `src/Conversation.tsx`, `src/layout.ts`, `src/Chat.tsx`, `src/Icon.tsx`, `src/api.ts` | A reply item carries its turn and the message a pin goes on, and a person's item its id; the actions are drawn beside Copy; one line in `Chat.tsx` draws the panel; three icons (layers, pin, close); the calls at the end of `api.ts`. |

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/chats/{id}/turns/{turn}/inspect` | `inspectTurn` | Opening the panel, and reading it again when a turn of the chat ends. |
| `GET /api/v1/chats/{id}/pins` | `listPins` | A chat's pins, the first time its screen shows a message. |
| `PUT` and `DELETE /api/v1/chats/{id}/pins/{message}` | `setPin` | Pin, and Unpin in the panel. |
| `PUT` and `DELETE /api/v1/chats/{id}/skips/{kind}/{ref}` | `setSkip` | Leave out next turn, and Put back. |
| `GET /api/v1/chats/{id}/skips` | `listSkips` | Not called yet: the inspect route returns the skips. |

The bar that shows how full a call was is a `progress` element, so no style is set from script. Seen in a browser on November 25, 2026: see "Checked by" in [inspector.md](inspector.md).

## Background tasks (P8-01)

A card for each background task under way in the chat, and the note a task writes when it ends. The server's side is [jobs.md](jobs.md). There is no mockup for either: they are built from the tokens and follow the approval card's shape, a first draft until the agent mockups (P1-05).

| Part | File | What it does |
| --- | --- | --- |
| The card | `src/Tasks.tsx`, `src/tasks.css` | Lists the chat's tasks when it opens and whenever one of its turns ends (a turn may have started one with the tool), and draws a card under the conversation for each task under way: "Background task" with its title, where it is ("Using search_chats · 1 step done", pulsing while it works), "Waiting for your approval, in its chat" when a call waits, a link to its own chat ("Open its chat to watch its steps", or "to answer it"), and **Stop**. In the task's own chat the card says so and links back to the chat it was started from. Each card follows its task's stream and comes back after a dropped connection; when the task ends the chat is read again and the card goes. |
| The note | `src/Tasks.tsx` (`TaskNote`), `src/layout.ts`, `src/Conversation.tsx` | A message with the stop reason `task` is drawn as a reply of its own, not joined to the reply before it: "From a background task", a link to the task's own chat, the text, and Copy. `layout.ts` has one more branch, and `Conversation.tsx` one more kind of part. |
| Step words | `src/steps.ts` | `start_background_task` reads "Starting a background task, “title”", and its approval card "Start a background task, “title”". |
| The way in | `src/Chat.tsx`, `src/api.ts` | One line in `Chat.tsx` draws the cards. The calls are added at the end of `api.ts`. |

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/chats/{id}/tasks` | `listTasks` | The cards, and the note's link. |
| `GET /api/v1/chats/{id}/tasks/{task}/events` | `followTask` | Following a task under way, with `Last-Event-ID` after a dropped stream. |
| `POST /api/v1/chats/{id}/tasks/{task}/stop` | `stopTask` | Stop on a card. |
| `POST /api/v1/chats/{id}/tasks` | `startTask` | Not called by a screen yet: a task is started by asking for one, and the model's `start_background_task` asks first. |

Seen in a browser on November 25, 2026, against the server built from this branch on port 7971 with a data folder inside the worktree, as the test tenant: "Find out in the background when the lighthouse walk should start." drew the approval card for the start; once approved, the task ran in its own chat and its note came into the chat as a reply of its own, with the work chat in the sidebar. With a launcher (not kept) that registered a slow stand-in tool and a changing one: a running card and a waiting card side by side; the work chat with its running step and its card; Stop from the work chat, and the stopped note; and **the server killed while one task's tool ran and another waited for an approval, then started again: the page came back to the streams, the first task resumed and its result appeared in the chat, and the second still waited, and finished once approved in its chat.** In light and dark, and at phone width. The only console errors were the refused connections while the server was down.

## Scheduled tasks (P8-03)

`/scheduled`, `src/Scheduled.tsx` with `src/ScheduleForm.tsx`, `src/schedules.ts` and `src/scheduled.css`, following the draft `design/screens/Scheduled.html` in the tokens: a header with Pause all (Resume all while held) and New scheduled task; the zone the times are in, with a link to General, and what is wrong with the setting when it names no zone; a banner while every schedule is held; the list of schedules, each with its recurrence in words, its last run as a pill ("Done today", "Done, late today", "Failed Fri 27 Nov", "Running", "Waiting for you") and its next due time; and the chosen schedule (`?s=` in the address, so a reload keeps it): a switch, Run now, Edit, Delete with a confirmation, four facts (schedule and zone, model, budget, if a run is missed), the prompt, and its runs (when, result with why it was late or failed, how long it took, steps, cost, and Open or Answer for its chat). The form makes and changes a schedule. Times are drawn from the text the server sent, which carries the person's zone's offset, never in the browser's zone. While a run is under way the screen reads again every two seconds; otherwise when the soonest due time comes, and every 30 seconds. In a run's own chat the task card says the chat is the task's own and its last reply will be its result, and a note there has no link to itself (`src/Tasks.tsx`).

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/schedules` | `listSchedules` | The list, the switch and the zone. |
| `GET /api/v1/schedules/{id}` | `getSchedule` | The chosen schedule and its runs. |
| `POST /api/v1/schedules`, `PUT /api/v1/schedules/{id}` | `createSchedule`, `updateSchedule` | The form. |
| `DELETE /api/v1/schedules/{id}` | `deleteSchedule` | Delete, once confirmed. |
| `POST /api/v1/schedules/{id}/pause`, `…/resume` | `setScheduleEnabled` | The switch. |
| `POST /api/v1/schedules/{id}/run` | `runScheduleNow` | Run now. |
| `POST /api/v1/schedules/pause-all`, `…/resume-all` | `holdSchedules` | Pause all and Resume all. |

Seen in a browser on November 25, 2026, against the server built from this branch on port 7991 with a data folder inside the worktree (removed afterwards), as the test tenant: a daily schedule made for two minutes ahead ran at its time, and its run was a chat in the sidebar titled "Harbour brief, Wed 25 Nov 2026" with the prompt, its step and the reply; a weekdays schedule made for a minute ahead, the server stopped before its time and started again more than two hours later: **one run, "Done, late", "Missed at 14:39 because Keystone was not running. Ran once when it was back."**, and the other schedule, whose time had already run, ran nothing more; Pause all and the held banner, Resume all; Edit to Mondays and Thursdays; Run now, in a chat of its own. In light and dark, and at phone width, where a table that ran wider than the screen was fixed. No console errors.

## The switch that pauses every agent, and budgets (P8-05)

`src/AgentsSwitch.tsx` with `src/agents-switch.css`: one bar with the switch that pauses every agent and schedule, drawn under the Scheduled screen's header and above the task cards in a chat. Running, it says background tasks and scheduled runs go on by themselves, with **Pause all agents**; paused, it says they wait where they stopped and no schedule starts a run, how many are still stopping, with **Resume all agents**. The page keeps one copy of the switch, so both places agree, and a screen is told when it changes so it reads its tasks or schedules again. Settings → General draws the same setting, `agents.paused`, from the registry. A paused task's card reads "Background task, paused" with the server's words ("Paused: …" or "Held: …") and keeps Stop; a paused run's pill is "Paused". The schedule form has **Budget for each run, in US dollars**, empty for the person's `agents.schedule_budget_usd`, and the BUDGET fact says whether it is the schedule's own. Lines in `Tasks.tsx`, `tasks.css`, `Scheduled.tsx`, `ScheduleForm.tsx` and `schedules.ts`; the types and calls in `api.ts`, after `followTask`. See [unattended-spend.md](unattended-spend.md).

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/agents` | `getAgentsSwitch` | The bar. |
| `POST /api/v1/agents/pause`, `…/resume` | `setAgentsPaused` | Pause all agents and Resume all agents. |

Seen in a browser on November 25, 2026, against a server built from this branch on port 7951 with a data folder inside the worktree (removed afterwards), as the test tenant: with every agent paused, a task started in a chat showed the bar and a card "Background task, paused" with "Held: every agent is paused. It starts when they are resumed."; Resume all agents started it, and its note came into the chat. On Scheduled, the bar in both states; a schedule with a budget of its own of $0.50 showed "$0.50 a run, this schedule's own"; Pause all agents marked it held with the words that say which switch may hold it; the form's budget field; Settings → General's control for the switch. At phone width the bar wraps under its words. The only console error was the sign-in probe's 401.

## Wiki upkeep (P8-06)

`src/WikiJobs.tsx` with `src/wiki-jobs.css`: a section of the Scheduled screen, under its list, drawn once the list has loaded (one line and its import in `Scheduled.tsx`). A sentence says what the jobs do, then one row for each job: its title, its store (and, for Waymarks, the principal it appears as), its schedule in words, its model and its budget for each run; a pill (Off, On, Held, or Cannot run, red when the job is on and cannot run); a switch; and **Run now**, disabled while a run is under way or the job cannot run. A job that cannot run says why in the server's words, and when its token is what is missing, the grant to give its principal and the secret to store it as, with the runbook's name. Under each, its last run with its pill and a link to its chat, what Keystone wrote for it, and its next run. The section reads the jobs again every two seconds while a run is under way or its answer is being written, and every thirty otherwise; a change made here tells the screen, which reads its list again. The calls go through `call` in `api.ts`, which is not changed. There is no mockup: built from the tokens after `design/screens/Scheduled.html`, provisional on P1-05. See [wiki-jobs.md](wiki-jobs.md).

| Call | Used for |
| --- | --- |
| `GET /api/v1/wiki-jobs` | The section. |
| `POST /api/v1/wiki-jobs/{job}/on`, `…/off` | The switch. |
| `POST /api/v1/wiki-jobs/{job}/run` | Run now. |

Seen in a browser on November 25, 2026, against a server built from this branch on port 7951 with a data folder inside the worktree (removed afterwards), as the test tenant: with no store, every job said it cannot run and why; with the built-in store, the Waymarks jobs named the store and the built-in job the missing model; its switch turned on with the pill red; in the dark theme and at phone width (375 pixels, no sideways scroll). The only console errors were the sign-in probe's 401 and the connection refused while the server was restarted.

## Attached chats and handoff (P5-06)

The chips of the chats attached to a chat, the control that attaches one, and the handoff's button and links. The server's side is [attach-and-handoff.md](attach-and-handoff.md). There is no mockup for any of it, so it is provisional until the chat mockups cover it (P1-04); it is built from the tokens.

| Part | File | What it does |
| --- | --- | --- |
| The chips | `src/AttachedChats.tsx`, `src/attach.css` | Over the composer, a chip for each attached chat: its title, a link that opens it, its summary as the chip's tooltip, a `sensitive` mark (only a local or trusted model is given it) or an `incognito` one (no reply is given it, and its title is struck through), and a button that takes it off. The chat it continues from, after a handoff, is a copper chip that reads "Continued from …" and has no button. |
| The attach control | `src/AttachedChats.tsx` | "Attach a chat" (then "Attach") after the chips, a dashed pill that opens a picker upwards: a box that filters the person's chats by the words of their title and summary as they type, at most thirty, and a sentence on what attaching does. A pick attaches the chat and closes the picker. Disabled at `chats.attach_max`; nothing is drawn in an incognito chat. On a phone the picker is a sheet fixed at the top of what can be seen, with a Close button, and its box does not take the focus by itself: opening upwards with the keyboard raised, its box and list were pushed off the top (the owner's report, November 26, 2026). The sheet follows the visible part as the keyboard comes and goes (`visualViewport`), and its box is 16px so the phone's browser does not zoom the page. |
| The header | `src/ChatLinks.tsx`, `src/attach.css` | At the right of a chat's header, where the mockups put Share: "Continued from" and the parent's title as a link, "Continued in" and the newest handoff's title as a link (with +N for more), and **Hand off**, which is disabled while a reply streams and in an incognito chat. Hand off makes the new chat, puts it at the top of the sidebar, picks for it the model the parent would use next (sent with its first message, as a pick is), and opens it. On a phone the words go and the titles and the icon stay. |
| Shared state | `src/attached.ts` | A chat's answer of `GET /attached`, kept outside the screens so the header and the chips agree; read when a screen first asks and again when a turn of the chat ends, since a turn may have given an attached chat its summary. |
| Icons | `src/Icon.tsx` | `chatlink` (a bubble with a link) and `handoff` (out of one chat and on into the next). |
| The way in | `src/Chat.tsx`, `src/Shell.tsx`, `src/api.ts` | One line in `Chat.tsx` draws the chips over the composer, and one in `Shell.tsx` the header's links, each with its import. The calls are added to `api.ts` after the inspector's. |

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/chats/{id}/attached` | `listAttached` | The chips and the header's links. |
| `PUT`, `DELETE /api/v1/chats/{id}/attached/{other}` | `setAttached` | Attaching from the picker, and a chip's button. |
| `POST /api/v1/chats/{id}/handoff` | `handOff` | Hand off. |

Seen in a browser on November 25, 2026, against the server built from this branch on port 7961 with a data folder inside the worktree (removed afterwards), as the test tenant: the picker filtered to "ferry" and attached North ferry times as a chip; Hand off on the lighthouse walk opened a new chat with "Continued from Planning the lighthouse walk" in the header and as its chip; "What did we settle in the chat this one continues from?" was answered from the parent's summary by the recorded turn; the parent's header then read "Continued in" with the new chat's title. At phone width the header kept the titles and the icon. The only console error was the session check before sign-in. Not seen: dark theme, the `sensitive` and `incognito` marks, `too_many`.

## Memory and incognito (P6-07)

Provisional on the memory mockups (P1-06), which are not approved: built from the tokens alone. `/memory`, `src/Memory.tsx` with `src/memory.css`, is in the sidebar after Scheduled: the store's note on what forgetting does there; each space as a card, with its facts grouped by the reply they came from, a link to the chat (or "a chat since deleted"), Correct as an inline box and Forget after a question, its pages folded away, and a sensitive space's facts hidden until Show; beside them the profile, with a link to Settings → Profile, and the map. `src/Incognito.tsx`: a switch under the new-chat composer that makes the first message start an incognito chat, and a dashed copper mark at the top of an incognito chat. Leaving that chat's screen sends `POST /chats/{id}/close` a moment later (coming straight back cancels it, as React's strict mode does), and the page going sends it with `navigator.sendBeacon`. The calls are in `src/memoryCalls.ts`, apart from `api.ts`, which now exports `call`.

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/memory` | `memoryOverview` | The Memory screen. |
| `POST /api/v1/memory/forget`, `…/correct` | `forgetFact`, `correctFact` | Forget and Correct. |
| `POST /api/v1/chats/incognito` | `createIncognitoChat` | The first message of a new chat with the switch on. |
| `GET /api/v1/chats/{id}/memory` | `isIncognito` | Whether a chat wears the mark. |
| `POST /api/v1/chats/{id}/close` | `closeIncognito`, `closeIncognitoAsPageGoes` | Leaving an incognito chat. |

Seen in a browser on November 25, 2026, against the server built from this branch on port 7973 with a data folder inside the worktree, as the test tenant with the built-in store: the beehives facts listed with their chat, one forgotten and one corrected, each with the server's note; an incognito chat with its mark, deleted (404) once the person went to a new chat, and dropped from the sidebar. Light and dark, and at phone width. No console errors but the sign-in check's 401 and the 404 asked for.

## Uploads (P9-02)

The composer takes files: a plus button opens the file picker, and files dropped on the composer are taken too. Each is a chip with its ending, name, size and a remove button; a file `GET /uploads/limits` refuses is not taken, with the reason. A message may be files alone. The files are uploaded when the message is sent, and the message goes with their blocks first. In the person's message an image is a thumbnail from the content route and other files are downloads; a reply that did not see an image, or that a fallback wrote to see one, has a notice above it. The inspector lists each file's text in the attachments part and has a section, Images and files. `Attachments.tsx`, `uploads.ts` and `uploads.css`; small edits in `Composer.tsx`, `Chat.tsx`, `turns.ts`, `api.ts`, `layout.ts`, `Conversation.tsx` and `Inspector.tsx`. Provisional until the workspace mockups (P1-06): built from the tokens, after the composer's "Add files or a chat" button and the attached chat's chip in `design/screens`. See [uploads.md](uploads.md).

## Projects (P9-01)

The projects screen, a project's page, the projects in the sidebar, and the project chip in a chat's header. The server's side is [projects.md](projects.md). There is no mockup for any of it (the workspace mockups, P1-06, wait on the owner), so it is built from the tokens, after the settings screens' cards and the chat list's rows, and is provisional.

| Part | File | What it does |
| --- | --- | --- |
| The projects screen | `src/Projects.tsx` (`Projects`), `src/projects.css` | `/projects`: **New project** opens a form (a name, and instructions if wanted) that makes the project and opens its page; a card for each project with its name, the start of its instructions, its chats and files and when it changed; the archived ones apart, under "Archived (n)". It replaces the placeholder. |
| A project's page | `src/Projects.tsx` (`ProjectPage`) | `/projects/<id>`: "Projects /" and the name, which opens a menu (Rename, Archive or Bring back, Delete, which asks once more and says the chats are kept); a composer that starts a chat in the project, sends the message and opens the chat; the project's chats; and on the right, Instructions (Edit, with a count against the limit), Files (Add files, each file with its size and characters, a `sensitive` switch and a button that takes it off, and the server's reason for a file it refused) and Memory (the knowledge store space its facts go to, which can be changed). On a narrow screen the right column goes under the chats. |
| The sidebar | `src/Projects.tsx` (`SidebarProjects`) | "PROJECTS", the eight changed last that are not archived, between the starred chats and the recents, with "All projects" past eight. |
| The chip | `src/Projects.tsx` (`ChatProjectChip`) | After a chat's title: the project's name in a copper chip, or "Add to project". Its menu puts the chat in a project, moves it to another, takes it out, or opens the project. On a phone it is its icon alone. |
| Shared state | `src/projectStore.ts` | The list of projects and the project each chat is in, kept outside the screens, so the sidebar, the screens and the chip agree. |
| Icons | `src/Icon.tsx` | `file`, a page with a folded corner. |
| The way in | `src/App.tsx`, `src/Shell.tsx`, `src/api.ts` | In `App.tsx`: the import, the `/projects` route's element and the `/projects/:id` route. In `Shell.tsx`: the import, the header's title for a project's page, the sidebar's group and the chip. In `api.ts`: the types and calls, after the scheduled tasks'. |

Its calls, all in `src/api.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/projects` | `listProjects` | The screen, the sidebar and the chip's menu. |
| `POST /api/v1/projects` | `createProject` | The new project form. |
| `GET`, `PATCH`, `DELETE /api/v1/projects/{id}` | `readProject`, `changeProject`, `deleteProject` | The page, and its renaming, instructions, memory space, archiving and deleting. |
| `POST /api/v1/projects/{id}/files?name=` | `addProjectFile` | Add files: each file's bytes as the body, one at a time. A file larger than the limit is refused in the page first. |
| `PATCH`, `DELETE /api/v1/projects/{id}/files/{file}` | `markProjectFile`, `removeProjectFile` | The `sensitive` switch, and taking a file off. |
| `POST /api/v1/projects/{id}/chats` | `startProjectChat` | The page's composer. |
| `PUT`, `DELETE /api/v1/projects/{id}/chats/{chat}` | `moveChat` | The chip's menu. |
| `GET /api/v1/chats/{id}/project` | `chatProject` | The chip. |

The delete button of the project's confirmation sets its colours itself (`.project-danger`), because `scheduled.css` and `chat.css` both define `.button-danger` and the later one leaves the button's text the colour of its ground; the chat's own Delete confirmation shows the same fault.

Seen in a browser on November 25, 2026, against the server built from this branch on port 7962 with a data folder inside the worktree (removed afterwards), as the test tenant: a project made from the form, two text files added, a chat started from the project's page whose turn inspector showed the project part and the files sent with the attachments, the copper chip in its header, an older chat moved in from its chip and taken out again, a file marked sensitive, and the project deleted with its chat kept and its files gone from `files/`. In dark at desktop width and in light at phone width, where the chip was made its icon alone after it crowded the header. The only console error was the session check before sign-in. Not seen: the archived state, renaming from the page, the memory space form, and a refused file.

## Artifacts (P9-03)

A card in the reply for each artifact a model made or changed, the panel beside the chat, the publish page and the Artifacts screen. The server's side, the sandbox and the screen are in [artifacts.md](artifacts.md). Provisional until the workspace mockups (P1-06): built from the tokens.

| Part | File | What it does |
| --- | --- | --- |
| The card | `src/ArtifactCard.tsx` | Drawn in the reply's steps in place of the row of a `create_artifact` or `update_artifact` call that did not fail: the title, the type and the version, and a button that opens the panel. |
| The panel | `src/ArtifactPanel.tsx`, `src/artifacts.css` | Beside the chat from 1,100 pixels wide, over it below. Version picker, Preview, Source, Compare, Restore, Download, Copy, Copy link, Open alone, the sensitive notice. Which artifact is open is kept outside the screens, as the inspector's turn is. |
| The frame | `src/ArtifactFrame.tsx` | HTML, React, SVG and Mermaid in an `iframe` with `sandbox`, its permissions the server's filtered to an allowed list that never holds same-origin; Markdown with `Markdown.tsx` and code as a code block, in the app. |
| The publish page and the list | `src/ArtifactPage.tsx` | `/artifacts/<id>` alone, outside the shell, and `/artifacts`, which replaces its placeholder. |
| The renderers | `src/sandbox/`, `vite.sandbox.config.ts` | React with Sucrase, and Mermaid, built by `npm run build` into `dist/sandbox/react.js` and `mermaid.js` after the main build; the server writes them into the sandbox's pages. Never part of the main bundle. |
| The way in | `src/Conversation.tsx`, `src/Chat.tsx`, `src/App.tsx`, `src/ApprovalSettings.tsx`, `src/steps.ts`, `src/Icon.tsx` | The card in `Steps`; one line drawing the panel; the publish page drawn in place of the shell when the address matches, and the list's route; the Approvals screen's words for the two writing tools; words for a failed or asking call; three icons. |

Its calls are in `src/artifacts.ts`, through `api.ts`'s `call`: `GET /chats/{id}/artifacts` (`chatArtifacts`), `GET /artifacts` (`allArtifacts`), `GET /artifacts/{id}` (`readArtifact`), `GET /artifacts/{id}/versions/{n}` (`readVersion`), `GET /artifacts/{id}/compare` (`compareVersions`), `POST /artifacts/{id}/versions/{n}/restore` (`restoreVersion`), and the download and sandbox addresses as links and the frame's `src`. The main page's policy is unchanged: no inline script, no `style` attribute, nothing from another site.

`npm run build` now runs three builds: the main page, then the two renderers. It still empties `web/dist/` first.

## Styles (P9-05)

The style picker in the composer and the Styles settings screen. The server's side is [styles.md](styles.md). There is no styles mockup, so both are built from the tokens, after the model picker and the settings screens, and are provisional.

| Part | File | What it does |
| --- | --- | --- |
| The picker | `src/StylePicker.tsx` (`StylePicker`, `applyNewChatStyle`) | In both composers, between the tools menu and the model picker: a pencil and the style's name, the pencil alone on a phone. Its menu, drawn with the model picker's classes, lists every style with the first two lines of its instructions, a `default` tag and a `yours` tag in the soft copper, a sentence on where the style comes from, and "Manage styles". In a chat a pick is sent at once with `PUT /chats/{id}/style` and is used from the next message; while a reply is being written the rows wait. On the new chat screen the pick is kept, for each person, and given to the chat the first message makes before the message is sent, as the tools menu does. It is read again when a turn ends, since the first turn keeps the default as the chat's own. |
| The screen | `src/StyleSettings.tsx` (`StyleSettings`) | `/settings/styles`, a tab after Approvals: every style with its instructions, "Built in" or "Yours", the default marked "Default for new chats"; **Make default** on each other style; **Edit** (an inline form) and **Delete** (with "Delete <name>" and "Keep it") on the person's own; a form that makes a new one, with its character count and the count of the person's styles against the limit. The server's refusals are shown as sentences. |
| Calls | `src/styleCalls.ts` | The eight style calls, apart from `api.ts`, as the memory screens' are. |
| Look | `src/style-presets.css` | The picker's place in the row, its menu's width and tags, the screen's rows and forms, and the delete confirmation's own colours (`.style-danger`), since two stylesheets define `.button-danger`. Not named `styles.css`, which is the app's own stylesheet. |
| The way in | `src/Chat.tsx`, `src/Settings.tsx` | In `Chat.tsx`: the import, `applyNewChatStyle` after the tools menu's switches, and the picker in the two composers' `beside`. In `Settings.tsx`: the import, the tab and its route. |

Its calls, all in `src/styleCalls.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/styles` | `listStyles` | The screen, and the new chat screen's picker. |
| `POST /api/v1/styles` | `createStyle` | The new style form. |
| `PATCH`, `DELETE /api/v1/styles/{id}` | `changeStyle`, `deleteStyle` | Edit and Delete on the screen. The list is read again after a delete, since a default that named the style goes back to the shipped one. |
| `PUT /api/v1/styles/{id}/default` | `setDefaultStyle` | **Make default**. |
| `GET`, `PUT /api/v1/chats/{id}/style` | `chatStyle`, `chooseChatStyle` | The picker in a chat, and the new chat's pick once its chat is made. |

Seen in a browser on November 25, 2026, against the server built from this branch on port 7965 with a data folder inside the worktree (removed afterwards), as the test tenant: the new chat screen's picker on Normal, the default; Concise picked there and kept by the chat the first message made, whose turn inspector listed the Style part with its tokens; a style made on the screen, made the default and shown as such by the new chat screen's picker; a name a built-in style has refused with the server's words; the style renamed from its inline form; Formal picked from a chat's picker; the style deleted after its confirmation, with the default back on Normal. In light at desktop width, and in dark at phone width, where the picker is its pencil and its menu hangs from the composer. The console's only errors were the session check before sign-in and the refused name. Not seen: a style of the person's own whose words change a real model's reply (the test tenant's recorded turns answer whatever the style), and the limit reached.

## Operations and Monitor (P11-03)

The Operations page and the home screen's notice. The server's side is [operations.md](operations.md). There is no settings mockup (P1-07), so the page is drawn from `design/screens/Operations.html` and the notice after the "Running now" strip of `Home.html`, with the tokens, and both are provisional (decision [0463](decisions/0463-operations-is-a-page-of-its-own-and-the-home-notice-reads-the-watch-route.md)).

| Part | File | What it does |
| --- | --- | --- |
| The page | `src/Operations.tsx`, `src/operations.css` | `/operations`: a header with **Read again**, the switch that pauses every agent, four tiles (running now, failed in the window, model calls, spend this month with its bar and "at this rate, … by the end of the month"), **Needs attention** (each crossed limit, stuck or failed task, failing model and failed schedule, with where to look), **Room to grow** (each limit with its use, a bar with a tick at its watch level, a line of its trend, its growth a day, when it fills, and a pill; then the budgets of runs under way), **Models**, **Background tasks and runs**, **Schedules** and **Recent traces**, each trace's title opening its chat with the turn inspector on its turn (`openInspector`, then the chat's address). It reads the report again every minute while the tab is shown. Tables wider than a phone scroll inside their card. |
| The notice | `src/OperationsNotice.tsx` | On the home screen, under the composer: "Running out of room", a sentence for each limit at or past its watch level, and **See Operations**. Read once when the home screen opens; nothing is drawn when no limit is crossed or the server cannot say. |
| The calls | `src/operationsCalls.ts` | `readOperations` (`GET /api/v1/operations`) and `readWatch` (`GET /api/v1/operations/watch`), their types, and the words for amounts and sizes. Kept apart from `api.ts`. |
| The way in | `src/App.tsx`, `src/Shell.tsx`, `src/Settings.tsx`, `src/Chat.tsx`, `src/Icon.tsx` | A line each, with an import where needed: the `/operations` route; Operations in the sidebar after Memory; a Settings tab that goes to it; the notice in the new chat screen; and the `operations` icon, the pulse of the mockup's sidebar. |

Bars and trend lines are inline SVG whose sizes are attributes, coloured by classes that read the tokens (`--ok`, `--accent`, `--err` on `--sunk`), so the page has no `style` attribute and no chart library (decision [0432](decisions/0432-the-usage-bars-are-inline-svg.md)).

Seen in a browser on November 26, 2026, in the browser pane, against a stand-in on port 7942 that served the page built from this branch and answered the session and the two Operations routes from `operations.Read` and `operations.Watch` on the test tenant's data folder (inside the worktree, removed afterwards), with a week of generated snapshots, invented turns, a running task at 82% of its budget, and the test tenant's own disk watch level set to 1%: the notice on the home screen naming the disk and the task, **See Operations** opening the page with every section filled, the Settings tab going to `/operations`, in light at desktop width and in dark at phone width with no page that scrolls sideways (a hidden header word that escaped its scrolling card was fixed). The only console errors were the stand-in's 404s for routes it does not serve. The real server built from this branch was started on port 7941 with the same folder and answered 401 to both routes with no session; signing in to it was left to a person, so the page has not been seen against the real server, and a trace's link has not opened the inspector.

## Share links (P10-04)

The Share button in a chat's header, where the mockups draw it, and its popover (`ShareControl.tsx`, one line in `ChatLinks.tsx`); Settings → Shared links (`ShareSettings.tsx`, three lines in `Settings.tsx`); and the shared page at `/share/<token>` (`Shared.tsx`), which `main.tsx` draws in place of the app, so nothing asks who is signed in. The page reads the link with `credentials: "omit"`, imports nothing from `api.ts`, and draws text only: replies through `Markdown.tsx`, artifacts as source in a code block. Calls in `share.ts`; styles in `share.css`, from the tokens. Provisional: only the button has a mockup. See [share.md](share.md).

## Dictation (P10-01)

A microphone just before the send button in every composer, and a line under the text with the state, the words still being heard and why dictation stopped. The machine is `dictation.ts`, which has no imports and is tested by Node's own test runner (`npm test`, `web/app/test/`, decision [0501](decisions/0501-the-web-machine-is-tested-by-nodes-own-test-runner.md)); the hook and the two components are `DictationControls.tsx`, the look `dictation.css`, and the icon `mic` in `Icon.tsx`. In `Composer.tsx`: the import, the hook, the line under the text, the button after `beside`, `if (dictation.holdsSend()) return;` at the head of `send`, and Escape in `keyDown`. The settings are read with `listSettings`. The server now sends `Permissions-Policy: microphone=(self)`; the content security policy is unchanged. Provisional: there is no mockup of the button. See [dictation.md](dictation.md).

## Imported chats (P12-03)

Provisional on the mockups, which draw no imported chat. A chat brought in from New Keystone carries `imported` on its `Chat` ([import.md](import.md)). `Imported.tsx` draws it twice. `ImportedTag` is a small "imported" mark on the chat's row, in the sidebar (one line in `Shell.tsx`) and on the chats screen (one line in `pages.tsx`), whose tooltip says where the chat came from, how many turns it held, when, and which models answered there. `ImportedNote` is a line at the top of the chat's screen (one line in `Chat.tsx`, under the incognito mark) that says the same and, below it, the mark's note: what New Keystone recorded in the chat that Keystone counts and cannot show as steps. Both read the mark from the list of chats, which opening a chat refreshes. `api.ts` gained the `Imported` type and the field on `Chat`. Styles are in `imported.css`, from the tokens. The source passes `tsc --noEmit`. It has not been seen in a browser, and `web/dist/` is not rebuilt on the branch.

## Inline visuals (P9-04)

A chart or a diagram in a reply. `Markdown.tsx` records whether a fenced block's closing fence was found, exports `CodeBlock`, and offers each fenced block to a drawer when a screen gives one (`FenceDrawer`); with none it draws a code block, as before. `Conversation.tsx` wraps a reply's text in `<InlineVisuals streaming=…>` (`InlineVisuals.tsx`), the only place that gives a drawer, so a shared page, a task's note, a sub-agent's result and a Markdown artifact still show a `chart` or a `mermaid` block as text. A chart is checked and laid out by `chart.ts` and drawn as SVG elements; a diagram is drawn in an `iframe` with `sandbox="allow-scripts"` whose page is `GET /api/v1/visuals/diagram`, which the page talks to with `postMessage` through `visuals.ts`. `chart.ts` and `visuals.ts` import nothing and are tested by `npm test`, which now runs three files. `sandbox/mermaid.ts` gained a mode that waits for a diagram from the page that frames it. The look is `visuals.css`; sizes are SVG and frame attributes, since the policy refuses a style attribute. The content security policy is unchanged. Provisional: there is no mockup of a chart or a diagram in a reply. See [inline-visuals.md](inline-visuals.md).

## Research mode (P8-04)

The Research control beside the composer's pickers, the card of a research task under way, and the note it writes when it ends. The server's side, and what each looks like, is [research-mode.md](research-mode.md). There is no mockup for any of it: it is built from the tokens, the task card, the sub-agents' lanes and the artifact card, a first draft until the agent mockups (P1-05).

| Part | File | What it does |
| --- | --- | --- |
| The control and its line | `src/Research.tsx` (`ResearchControl`, `ResearchHint`), `src/researchCalls.ts` | A switch kept for the chat, or for the new chat screen, until the next message is sent. On, the line above the composer says what a task will run with, from `GET /research`. `sendAsResearch` starts the task with the message's words and reports that it took the message, so no turn is sent. |
| The card | `src/Research.tsx` (`ResearchCards`), `src/research.css` | Lists the chat's tasks when it opens and whenever one of its turns ends, reads each task under way about once a second, and draws its card: state, question, where it is and its spend, the plan, a lane for each part (`Lane`, from `Subagents.tsx`), the sources found, and Stop. When a task it saw under way has ended, the chat is read again. |
| The note | `src/Research.tsx` (`ResearchNote`), `src/Tasks.tsx` (`TaskNote`) | A message with the stop reason `task` whose id is a research task's `note_message_id` is drawn as the research's: its words with each citation a link (`CitationSources`), the report's card, which opens the artifact beside the chat (`openArtifact`), the sources cited, and the parts. `TaskNote` has three more lines for it. |
| The way in | `src/Chat.tsx` | The control in each composer's `beside`, the line above each composer, the cards under the conversation, one line in `sendHere` and five in `begin`. |

Its calls, all in `src/researchCalls.ts`:

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/research` | `researchTerms` | The line that says what a task will run with. |
| `GET /api/v1/chats/{id}/research` | `listResearch` | The cards, and telling a research task's note from a background task's. |
| `POST /api/v1/chats/{id}/research` | `startResearch` | The message sent while the control is on. |
| `GET /api/v1/chats/{id}/research/{research}` | `getResearch` | Reading a task under way. |
| `POST /api/v1/chats/{id}/research/{research}/stop` | `stopResearch` | Stop on a card. |

A part's lane makes the sub-agents' own calls, with the task's own chat as the chat ([sub-agents.md](sub-agents.md)).

## Settings at a glance (P11-04)

The General settings screen: each setting of the registry as a name, one short line and its value, opening in place for the long explanation, the box to change it in, its default and its key. [settings-screens.md](settings-screens.md) describes it under "How General is drawn". `src/GeneralSettings.tsx` draws it, `src/settingsView.ts` works out what a row shows and has the tests (`test/settingsView.test.ts`, run by `npm test` and by `verify`), `src/settings-general.css` has the rows, and `src/SettingsParts.tsx` holds what every settings screen shares. At phone width every box under `.settings` is 16px, so a phone's browser does not zoom the page when one takes the focus, and the screen has no overlay and takes no focus by itself.

## The Chats screen (P4-06)

Provisional on the mockups, which draw no such screen. `/chats` is a table of every chat, in `src/ManageChats.tsx` with its calls in `src/manageCalls.ts` and its styles in `src/manage-chats.css`; `pages.tsx` gives `Chats` from it and keeps `Soon` and `Missing`. A search box over titles and messages, five filter chips, the table (a checkbox, the title with its marks, a line of what the search found or the chat's summary, messages and last activity), loading as the end of the list comes into view, select all shown and select all that match, a bar with the count and Delete while anything is selected, a confirmation that says what goes, what stays and that there is no undo, and a note afterwards naming each chat that was left. [manage-chats.md](manage-chats.md) has each part, and what the screen does on a phone: rows of a few short lines with no sideways scrolling, no box that takes the focus by itself, 16px text boxes, and a confirmation placed in the visible part of the screen.

| Route | Call | When |
| --- | --- | --- |
| `GET /api/v1/chats/table` | `chatTable`, `matchingIds` | The first page when the search, a filter or the order changes and after a delete; the next page as the list's end comes into view; the ids for "select all that match". |
| `POST /api/v1/chats/delete` | `deleteChats` | Delete in the confirmation, once for every `delete_max` chats selected. |

Seen in a browser on November 27, 2026, in the browser pane, against the server built from this branch on port 7960 with a data folder inside the worktree (removed afterwards), as the test tenant with 137 invented chats added: at 1280 pixels in light and dark and at 375 pixels in light and dark, the table with its marks, a search with its words highlighted in titles and snippets, the Imported filter, the orders by messages and by best match, the list growing to all 132 rows as it was scrolled, select all shown and "Select all 140 chats", the bar, the confirmation, and a delete of nine chats of which one, with a sub-agent at work, was left and named. At 375 pixels nothing was wider than the screen, nothing had the focus when the page opened, both text boxes were 16px, a long title took two lines with the word found in it showing, and the confirmation sat at the foot of the screen with the focus on Keep. Not seen: a real phone, so the on-screen keyboard itself; the other four filters and the order by title, which the tests cover; a selection larger than one request deletes.

## The composer and header on a phone

At 760 pixels and under (where the sidebar is a drawer), the composer and a chat's header are drawn for a thumb, after the owner's example of another app's composer (November 27, 2026). Desktop is as before. Decision [0590](decisions/0590-on-a-phone-the-composer-and-header-controls-are-rows-of-a-sheet.md). Provisional: there is no mockup of either.

| What | Where it was | Where it is on a phone |
| --- | --- | --- |
| Attach files | The `+` icon in the composer's row | "Files or photos", a row of the sheet the round `+` opens; its state is how many files are ready |
| Attach a chat | The dashed "Attach a chat" pill over the composer | "Attach a chat", a row of the `+` sheet; its picker opens in the sheet. The chips of chats already attached stay over the box, compact; with none, nothing is drawn there |
| Research | The search icon in the composer's row | "Research", a row of the `+` sheet: on or off, and a tap switches it and closes the sheet |
| Connectors | The plug and its count in the composer's row | "Connectors", a row of the `+` sheet ("1 of 2 on"); its switches open in the sheet, and flipping one leaves the sheet open |
| Style | The pencil in the composer's row | "Style", a row of the `+` sheet with the style's name; its list opens in the sheet |
| The model | The model picker before the microphone | A chip beside the `+`, with the model's short name (what is in brackets at the end of its label goes); its menu hangs from the composer |
| Microphone and send | Square buttons at the right of the row | Round 44-pixel buttons at the right; send in the copper accent |
| Share, Hand off, the project, "Continued from", "Continued in" | Buttons, a chip and links in the header | Rows of a sheet behind one `...` button at the right of the header; Share's popover and the project's menu open in the sheet |

- **The sheets** (`PhoneSheet.tsx`, `phone-sheet.css`) rise from the foot of what can be seen, kept by `visualViewport` as the attach picker's is, over a scrim. Escape, a tap outside, and a choice close them; a control's row is the head of its own picker, and the other rows step aside while it is open. They are drawn into the body, outside the composer's form, and stay in the page while closed, so each control keeps what it read.
- **How a control gets there**: it asks `useSheet()`; inside a sheet it draws a `SheetRow` (icon, name, state) in place of its button and calls `done()` when a choice is made. The words of each state and the model's short name are `composerSheet.ts`, tested by `test/composerSheet.test.ts`.
- **The header** is the menu button, the title (ellipsised, with the room), and `...`; every control is 44 pixels. While the chat is renamed, the box takes the `...` button's room. Since P10-07 it floats over the page: see below.
- **Nothing takes the focus by itself on a phone** (the composer used to take it when a chat opened), and every text box is 16 pixels, so the browser does not zoom. Every target in the header, the composer, the chips and the sheets is at least 44 pixels.
- **The way in**: `Composer.tsx` takes the model picker as `model`, apart from `beside`, and draws the phone's row and sheet; `Chat.tsx` passes `AttachChatRow` in `beside`; `Shell.tsx` draws `ChatMenu` (in `ChatLinks.tsx`) in place of `ChatLinks` and the project chip; `AttachedChats.tsx` shares its picker (`ChatPicker`) between the pill and the row; `AttachButton`, `ResearchControl`, `ConnectorMenu`, `StylePicker`, `ShareControl`, `ChatProjectChip` and `ModelPicker` have their phone branch; `Icon.tsx` has `more`.

Seen in a browser on November 27, 2026, in the desktop app's browser pane, against the server built from the branch on port 7983 with a data folder inside the worktree (removed afterwards), as the test tenant with two invented connectors added: at 320, 375 and 430 pixels in light and dark, a chat with the header's sheet and the `+` sheet open, and Attach a chat, Connectors and Style opened in it. By a script in the page at each: nothing wider than the screen, no two header controls overlapping, no target under 44 pixels in the header, the composer, the chips or the open sheet, every box 16 pixels, and nothing focused after the page loaded. By hand: Style set to Concise, a chat attached from the sheet, a connector switched off, Research on and off, a file attached and sent with a message, the model's menu, a share link made and turned off, Hand off, and the two "Continued" rows. At 1280 pixels the composer and the header were as before. Not seen: a real phone (so not the on-screen keyboard), the microphone's own listening (pressing it would have asked for the microphone), and the photo picker itself (its click was caught by a script and a file given to it).

### The phone layout (P10-07)

The owner asked for the interface to be more like another app's phone screens (five pictures, November 27, 2026). Keystone took the layout and feel and kept its own mark, name, tokens and fonts. At 760 pixels and under only; desktop is as before. Decisions [0610](decisions/0610-the-phone-composer-is-one-element-idle-as-a-pill-and-expanded-as-a-card.md), [0611](decisions/0611-on-a-phone-the-header-is-round-controls-floating-over-the-page.md) and [0612](decisions/0612-on-a-phone-message-actions-are-icons-and-a-persons-wait-for-a-tap.md). Provisional on the owner's phone.

| Part | On a phone |
| --- | --- |
| Header | No bar and no border: a round menu button on the left (opens the drawer); a centred pill with the chat's title and, under it in small quiet text, its project; a pill on the right with the `...` button. A turn's working mark is not here: it is the turning mark at the foot of the reply (below), as the owner asked on November 27, 2026, since the other app's ring up there shows usage, not work. Translucent `--raised` with a blur behind, solid where the browser cannot blur. The conversation scrolls beneath, with a fade at the top. `Shell.tsx` (`float-header`), `ChatTitle` with `phone`. |
| Working mark | While a turn runs, the foot of its reply shows three copper arcs in the mark's three coppers turning round its keystone (`Spinner.tsx`, `.reply-spinner` in `chat.css`), with "Thinking" beside it until anything is written; after that the steps and the text say what is happening and the mark alone shows the turn is not done. It replaces the square dot and "Keystone is answering", on every width. When the reply's last thing is a running step (thinking or a tool call) or a note that it is waiting or connecting again, the mark starts that line instead of sitting under it, so the working and its words are one line; a thinking step's caret moves to the line's end (`SpinMark`). With reduced motion it stands still. |
| Usage ring | On a phone, a chat's action pill holds a ring beside the ... button (`UsageRing.tsx`, `usage-ring.css`) that fills as the month's spend nears the monthly cap: green on track, copper near the cap, red at it or when the cap does not read. It reads `GET /api/v1/usage` for this month on the way in and each time a turn in the chat ends. A tap opens a Usage sheet: close, read again, and a way to Settings, Usage; a card for this month (percent used, a bar, the spend against the cap, the day it starts again) and one for the memory pass's reserve. The sums are `usageShare.ts`, tested in `test/usageShare.test.ts`. No mockup: after the owner's example of another app, provisional. |
| New chat | The mark and "Good afternoon, Test" (the first name) in the middle of the free space; the composer at the foot as in a chat; Incognito is a round eye-off button at the top right (`IncognitoButton`, `aria-pressed`, the soft accent when on, and a line under the greeting). |
| Composer | Idle (empty, not focused, nothing attached): one pill, `+`, "Message Keystone", the microphone and send. Focused, or holding text, files or dictation: the card, the box over `+`, the model's chip, the microphone and send. One element in both (`is-idle` on the form), so the focus stays in the box; `composerExpanded()` decides. |
| The note under the composer | Gone on a phone. "Keystone can make mistakes..." is at the foot of the model's sheet; the model is on its chip. The test tenant's notice stays under the composer: "Test tenant: recorded test replies". |
| Message actions | Icons only, 44 pixels, each with an `aria-label` and a `title`. Under a reply: copy, pin, inspect. Under the person's own message: nothing until the bubble is tapped (a second tap hides them); a pinned one keeps them. |
| Messages | The person's a rounder bubble on the right (20-pixel corners, 16-pixel text); the reply full width, no bubble. |

- **Where the code is**: `Composer.tsx` (the state, the fold 120 milliseconds after the focus leaves, the row's `mousedown` that keeps the box focused), `Chat.tsx` (`Start` on a phone, the note, the `ResizeObserver` that keeps the end of the chat in view as the composer grows), `Shell.tsx` (`float-header`, `Working`), `ChatTitle.tsx` (`TitleProject`), `Incognito.tsx` (the new chat's incognito store and `IncognitoButton`), `Conversation.tsx` (`Said`), `MessageActions.tsx` and `Markdown.tsx` (the words in an `action-label` span), `ModelPicker.tsx` (the footnote), `Icon.tsx` (`menu`), `composerSheet.ts` (`composerExpanded`, `phoneGreeting`, `saidActionsShown`, `phonePlaceholder`, `footnote`) and `phone-sheet.css`.
- **Not in a media query**: the float header and the composer's states are keyed to classes the script adds at the phone's width (`.main.is-phone`, `form.composer.is-phone`), the same `useNarrow()` that chooses the markup.

Seen in the desktop app's browser pane on November 27, 2026, against the server built from the branch on port 9181 with a scratch data folder, as the test tenant: at 390 by 844 in dark and light, the new chat (idle, focused, incognito on), a chat in a project (idle, focused, with text and the focus gone, the title's menu, renaming, the `+` sheet, the model's sheet with the footnote, a message sent, a person's message tapped), the drawer and the Chats page; at 320 by 700 a chat. By a script: nothing wider than the screen, no target under 44 pixels in the header, the composer or under the messages, the box 16 pixels, nothing focused after a load. At 1280 by 800 the header, the composer, the note, the Incognito pill and the worded actions were as before. Not seen: a real phone, so not the keyboard, the blur on Safari or the safe areas; the spinner during a real turn (the test tenant's replies are too quick; it was drawn by hand in the page to check its place).

## The API it calls

Every server call is in `src/api.ts`. The shell's routes are described in [api.md](api.md), and the chat and turn routes in [turns.md](turns.md).

| Call | Request | Answer |
| --- | --- | --- |
| `GET /api/v1/signin/people` | | 200 `{"listed","people":[{"id","name"}]}`. Needs no session. |
| `POST /api/v1/session` | `{"tenant","password"}` | 200 `{"person":{"id","name"},"session":{...}}` and the session cookie; 401 refused; 429 wait |
| `DELETE /api/v1/session` | | 200 `{"signed_out":true}` |
| `GET /api/v1/session` | | 200 `{"person":{"id","name"},"session":{...}}`; 401 not signed in |

The chat screen's calls. The shapes are the contract's, and are not repeated here.

| Call | Function | Used for |
| --- | --- | --- |
| `GET /api/v1/chats` | `listChats` | The sidebar and the chats screen. Read at sign-in and after every turn ends. |
| `POST /api/v1/chats` | `createChat` | The first message of a new chat. |
| `GET /api/v1/chats/{id}` | `openChat` | Opening a chat, and reading it again when a turn ends. |
| `PATCH /api/v1/chats/{id}` | `renameChat`, `starChat` | The chat's menu. One field in each call. |
| `DELETE /api/v1/chats/{id}` | `deleteChat` | The chat's menu. |
| `POST /api/v1/chats/{id}/turns` | `sendMessage` | Sending a message with `{"text"}`, and `{"text","model"}` when the person picked a model, and reading the reply's stream. The turn's id is read from `X-Turn-Id`, or from the first `turn` event. |
| `GET /api/v1/chats/{id}/turns/{turn}/events` | `followTurn` | Coming back after a dropped stream, with `Last-Event-ID`, and joining a turn from its first event, with none. |
| `POST /api/v1/chats/{id}/turns/{turn}/stop` | `stopTurn` | The stop button. |

The model picker's calls are in its own section above: `GET /chats/{id}/model`, `GET /chats/{id}/turns/{turn}`, and `model` in the send body.

The contract's other routes are not called yet: `GET /chats/search` and `POST /chats/{id}/select`. Nor are `edit_of`, `retry_of` and `blocks` in the send body, or `starred`, `limit` and `offset` on the list.

`tenant` in the sign-in request is the tenant id of the person picked from the list. The session is an HttpOnly cookie the server sets; the page never sees or stores a token.

How the app reads an answer:

- A network error, a 405, a 200 that is not JSON, or a 404 whose body has no code or the code `no_route`: the server is not ready. At sign-in the app shows that state with a "Try again" button. On the chat screen the part that could not be read says so.
- A 404 with any other code, such as `not_found`: the chat, message or turn is not in this person's record. The chat screen says the chat is not here.
- A 401 on any chat call: the session has ended, and the app shows sign-in.
- An answer that is not a success carries the API's error body. Its `error` is written for a person and is shown where a call is refused: a title that is too long, an empty message.
- `queue_full` (429) on a send: the chat already has messages waiting.
- 401 on the session read: signed out, show sign-in. 401 on sign-in: wrong password.
- 429 on sign-in: too many wrong passwords, wait and try again.

## Routes in the browser

The app routes in the browser (decision [0071](decisions/0071-client-side-routes-are-real-paths.md)). `cmd/keystone/serve.go` answers a `GET` or `HEAD` for a path that has no file extension, is not under `/api`, and is not a file in `web/dist/` with the page, so reloading on `/chats` works. A missing file such as `/assets/gone.js` stays a 404, and so does an unknown path under `/api`.

## What comes next

- The rest of the chat screen: see "Not built yet" above. Edit, retry and the branch switcher come first, since the contract already has their routes.
- The chat screen run again against Keystone's own routes, once P4-03 is merged, with a reply slow enough to stop and to drop.
- Passkey sign-in in the browser, once `signin.passkeys` is proven (see [signin.md](signin.md)).
- A standing check that rebuilds `web/dist/` where Node is present and fails if the result differs from what is committed (decision 0005).
- Bundled font files.
- For the model picker, three changes in the chats lane's `internal/turn`, none needed for it to work:
  - Say in `ModelChoice` that the chat is held, and mark each model a held chat may not be sent to. The picker would then read it from the route, show it before any turn is refused and in every browser, and the mark in local storage could go.
  - Answer a new chat's model choice from the server, for example `GET /chats/model` with no chat. The new chat screen would then stop working the choice out itself, and the test tenant would see its one model there too.
  - Say `key_present` in `ModelInfo`, so the chat's own route is enough without reading the registry.

## Caching

Files under `/assets` carry their content's hash in their name and are sent with `Cache-Control: public, max-age=31536000, immutable`. Every other built file keeps its name from build to build (`index.html`, `keystone.css`, `theme.js`, `mark.svg`) and is sent with `Cache-Control: no-cache` and an `ETag` of its content, so a browser asks again each time and keeps its copy only when nothing changed (`cmd/keystone/serve.go`, `TestHandlerTellsBrowsersWhatToKeep`). Before this, a phone kept an old stylesheet after a release, and light mode went on showing the dark tile (November 27, 2026).

The page itself names each of those files with its content's version, as `/keystone.css?v=<version>` (the first 24 hex digits of its SHA-256, the same as its `ETag`). The server writes the versions into `index.html` once per binary, for `/` and every interface route. So a release gives the stylesheet and `theme.js` new addresses, and no cache between the server and the phone can go on serving the old ones, Cloudflare's included, whatever it does with `no-cache`. A request that names the current version is sent as `immutable`; an old or missing version is sent with `no-cache` (`TestThePageNamesTheStylesheetByItsVersion`). The page is still `no-cache`, so a reload always fetches the new names. Asked for by the owner on November 27, 2026.

On a phone the model picker opens as a sheet from the foot of the screen, like the composer's +, with rows of at least 48px (`ModelPicker.tsx`, `.model-sheet` in `model-picker.css`). Opening upward from the chip, its menu ran off the top of the new chat screen (the owner's report, November 27, 2026).

## Location per message (P10-06)

A "Use my location" control, asked for by the owner from his iPhone so a model asked about the weather need not ask which city he is in. A tap asks the browser for the position once; the position, rounded, goes with that one message and no other. The server's side is in [turns.md](turns.md), "Location per message". Decisions [0600](decisions/0600-a-location-travels-as-a-field-of-the-send-body-and-is-kept-beside-its-message.md) to [0603](decisions/0603-the-position-is-asked-for-on-a-tap-once-and-rounded-in-the-page.md). Provisional: there is no mockup, and the owner has not used it on his phone.

| Part | File | What it does |
| --- | --- | --- |
| The pure part | `src/location.ts` | Reads `location.share`, `location.decimals` and `location.timeout`; `askPosition` is the one call to `getCurrentPosition` (low accuracy, no cached position, the setting's wait), and rounds before it returns; every word the control shows. No imports, tested by `test/location.test.ts` with a stand-in geolocation (`npm test`). |
| The control | `src/LocationControl.tsx` (`useLocation`, `LocationButton`) | On a desktop a small map-pin button just after the attach button; on a phone a row of the `+` sheet, "Use my location", with its state ("finding…", "about 1 km", "needs a secure page") and the hint on a line under it. `tap` is the only place a position is asked for: never on load, never watched. A second tap while the browser is asked stops waiting. On a page that is not secure the control is dimmed with its reason and never asks. |
| The chip | `src/LocationControl.tsx` (`LocationChip`) | Over the box, beside the files' chips: "Location: about 1 km" with a remove button (44 pixels on a phone), "Finding your location…" while the browser is asked, or why there is no position, in plain words for a refusal, no position, a timeout, and a browser without geolocation. |
| The mark | `src/LocationControl.tsx` (`LocationMark`), `Conversation.tsx`, `layout.ts`, `turns.ts` | "Sent with location", small and muted, under the person's bubble, from `location` on the message in the record, and from the send itself until the record has it. Its title is the line the model was given. |
| The inspector | `src/LocationControl.tsx` (`LocationSection`), `Inspector.tsx` | A section, Location, with the line each call of the turn carried before the person's words. |
| The way in | `Composer.tsx`, `Chat.tsx`, `Projects.tsx`, `turns.ts`, `api.ts` | The composer's one send passes `location.take()`, which hands over the rounded position and lets it go, so the next message has none; `onSend` and `send` take it as a third and fifth argument; `sendMessage` adds `location` to the body only when there is one, so a plain message's body is as before. On a phone a location waiting, being found or refused counts as something held for `composerExpanded` (P10-07), so the composer opens as the card and the chip, a full line above the box, shows. |

The hint says where it goes: "Sends your approximate location (about 1 km) with this message only, to whichever model answers it, hosted models included." The server now sends a second `Permissions-Policy` line, `geolocation=(self)`, beside `microphone=(self)` (decision [0602](decisions/0602-the-position-is-allowed-by-a-second-permissions-policy-line.md)); the content security policy and every other header are unchanged. The look is `location.css`, from the tokens; the icon is `location` in `Icon.tsx`.

The settings, in `settings/location.json`, lane `location`, each the person's own:

| Setting | Default | What it does |
| --- | --- | --- |
| `location.share` | true | The composer has the control; off, it is not drawn and the server refuses a located message |
| `location.decimals` | 2 | Decimals kept, one of 1 (about 11 km), 2 (about 1 km), 3 (about 110 m), 4 (about 11 m) |
| `location.timeout` | 10s | How long the browser may take to find the position; 0s waits as long as the browser does |
| `location.line` | "The person's approximate location: {latitude}, {longitude} (rounded to about {distance}), shared with this message." | The line the model is given before the person's words |

Seen in a browser on November 27, 2026, in the desktop app's browser pane, in a tab of its own, against the server built from the branch on `127.0.0.1:7986` with a data folder inside the worktree (removed afterwards), as the test tenant, with `navigator.geolocation` replaced by a script in the page that answers an invented point in the open Atlantic (no real position was read). At 1280 pixels in light: no ask on load; a tap asked once (low accuracy, no cached position, 10 s) and drew the chip "Location: about 1 km"; the recorded bakery message sent with it got its reply, the chip went, the bubble had "Sent with location" with the line as its title, the record held `12.35, -45.68`, 2 decimals and the line on that message only, and the inspector's Location section showed the line; a refusal, a timeout and a browser without geolocation each gave their words, the last also dimming the button with its reason; the chip's remove button removed it; the next message had no mark and no location. In dark at 1280 the chip and the mark. At 375 pixels in light and dark: the `+` sheet's row "Use my location" with the hint under it, 52 pixels tall, its state "about 1 km" after a tap, the chip with a 44-pixel remove button, a message sent from there with its mark and its inspector section, nothing wider than the screen. `GET /` answered `Permissions-Policy: microphone=(self), geolocation=(self)`. The only console error was the sign-in probe's 401. Not seen: a real phone or a real position (so not Safari's own permission prompt, nor a real refusal or timeout), and a page that is not secure (127.0.0.1 counts as secure; the Node tests cover it).
