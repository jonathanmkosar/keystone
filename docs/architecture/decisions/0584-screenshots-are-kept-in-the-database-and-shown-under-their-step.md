# 0584. Screenshots are kept in the person's database, named by the result, and shown under their step; the model reads text

November 27, 2026 · decided in P10-03

## The question

The browser's tools take screenshots for the person to see in the chat. A tool returns text (`tools.Tool.Run`), and the chat screen draws a step from the call's name, input and result (decision 0204). Where is the image kept, how does the screen find it, and does the model see it?

## The decision

- **A table, `browser_shots`, in the person's own database**: the JPEG, the chat, the call, the page's address and title. Its rows go with their chat (`ON DELETE CASCADE`) and are in the person's backups with the rest of the database.
- **The result names it** on its second line, right after the outside-content line and before any of the page's words: `Screenshot: <id>`. The screen reads only that header, so a page's text that holds such a line shows nothing.
- **`GET /api/v1/browser/shots/{shot}`** serves it, as `image/jpeg`, `nosniff` and a sandboxing policy, from the signed-in person's database only. The isolation drill asks for one of the first person's as the second.
- **The model reads text**, not the image: `browser_read` gives the page's text and its numbered elements. `browser_screenshot` tells it a screenshot was taken and shown.

## Why

- **The database is already the person's**, isolated, backed up and cleaned with the chat; files would need their own cleanup and isolation.
- **The result is the contract** with the screen, as the web tools' sources are (decision 0250), and the header is Keystone's own words.
- **Text first.** Sending an image back with every later call of a chat costs far more than its text, and a model that can read the page's words and elements has what it needs to act. Changing `tools.Tool` to return images touches a guard of rule 2 for every tool.

## What it costs

- A model cannot look at a page's layout, a picture or a chart; a page drawn on a canvas reads as almost nothing. Sending the screenshot to a model that can see is listed under "Not here yet".
- Screenshots add to the database: one for each open, click, typing, back and screenshot, about a hundred kilobytes each at the shipped size.
