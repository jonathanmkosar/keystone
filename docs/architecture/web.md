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
| `/chats` | Every chat, starred first, then the one with the newest message first. |
| `/chats/<id>` | One chat. |
| `/signin` | Goes to `/` when signed in. Signed out, every path shows sign-in and keeps its address. |
| `/search`, `/projects`, `/artifacts`, `/agents`, `/scheduled` | A placeholder saying the screen is not built yet. |
| anything else | Page not found, with a link back to the start. |

### The frame

Laid out as in `MainDark.html` and `MainLight.html`. The sidebar is 272 pixels wide and holds the mark and wordmark, a button to close it, New chat, the six places, the starred chats, the thirty most recent of the others with a link to the rest, the signed-in person with a sign-out button, and the theme switch. The main area has a header with the screen's title, and the content below it. On a chat the title is a button that opens the chat's menu.

Below 760 pixels wide the sidebar becomes a drawer over the page, opened from the header and closed by its own button, by the dimmed area beside it, or by moving to another screen.

### Colours and the theme

Every colour is a CSS variable generated from `design/tokens-draft.json` when the interface is built (decision [0070](decisions/0070-design-tokens-become-css-at-build-time.md)). No colour value is written in the app's stylesheets or components, with two exceptions the tokens have no name for: the dimmed area beside the drawer and the shadow under the composer and the menu are black at part strength. The person's bubble is halfway between the raised ground and the line, mixed in CSS, which is the mockups' value in both themes.

The theme switch offers dark, light and system. Dark leads, as in the design. The choice is kept in the browser under `keystone.theme`. `public/theme.js` applies it before the page paints, so a person who chose light on a dark system sees no dark flash; it is a separate file because the server's content security policy allows no inline script.

### Fonts

The stylesheet names IBM Plex Sans, Fraunces, Source Serif 4 and IBM Plex Mono and falls back to system fonts (decision [0072](decisions/0072-fonts-are-named-not-bundled.md)). No font file is bundled yet and nothing is loaded from another site.

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

Edit and retry with the branch switcher, feedback, attachments, the tools menu, the thinking switch, the context meter, share, the turn inspector, citations, approval cards, sub-agent lanes, search, keyboard shortcuts beyond Enter and Escape, and motion beyond the mark that follows arriving text. A second message cannot be sent while a reply is arriving, though the server would queue it.

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
