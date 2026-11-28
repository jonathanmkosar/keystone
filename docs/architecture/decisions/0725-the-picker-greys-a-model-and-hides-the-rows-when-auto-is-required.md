# 0725. The picker greys a model it may not pick, and hides the rows when Auto is required

November 28, 2026 · decided in P14-05

## The question

The spec's screens: "A model the person may not pick is greyed, with the reason. The switch that takes a chat off Auto is hidden when the person may not use it." The picker has no switch: Auto is a row of the menu and picking a model's row is what takes a chat off Auto (P14-04). What does each look like, and how does the browser keep from sending a pick the server refuses?

## The decision

- A model outside `ModelChoice.allowed` stays in the menu, greyed and disabled, with the line "Your admin has not allowed this model to be picked by hand." A missing key still comes first, then a held chat, then the policy, so each row says one reason.
- While Auto is required, the model rows are the switch: they are hidden, Auto is the only row, and one line says "Your admin keeps your chats on Auto, so Keystone picks the model for each message." The link to Models and keys stays.
- The new chat screen works the same choice out from the two settings, as it does the ladder.
- A pick the choice no longer allows is let go when the choice is read (`rememberChoice`), so it is not sent. A refusal that comes all the same shows the server's sentence as the turn's problem, and the picker reads the choice again when the turn ends.
- The words are in `policyWords.ts`, a module with no imports and a Node test, as the words of Auto are (decision 0501).

## Why

- Greying keeps the person's registry visible: they can see the model is there and why they cannot pick it, rather than wonder where it went.
- Hiding rows that would all be refused is the spec's hidden switch in the form the picker has.

## What it costs

- Provisional on the owner's answers on admin and views (A1 to A5) and the chat mockups (P1-04), which draw neither state.
