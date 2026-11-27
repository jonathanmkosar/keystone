# 0583. A click or a typing names the element by its words, and a page whose element says something else is not touched

November 27, 2026 · decided in P10-03

## The question

`browser_click` and `browser_type` ask the person first. The approval card is drawn from the call's input alone (decision 0220), and an element's number means nothing to a person. How does the card say what will be clicked, and what keeps a page from changing what that number points at between the read and the click?

## The decision

**Both tools take the element's number and its `label`**, the words `browser_read` listed for it. The card reads "Click “Submit” (element 2) on the page open in the agent's browser." When the call runs, the element's words on the page now must be the label given, ignoring case and spaces (or its kind, such as "text field", for an element with no words); otherwise nothing is touched and the model is told what it says now.

Then the guard checks the element itself (`guard.CheckClick`, `guard.CheckType`): no password, payment, one-time-code or sign-in field, no field or button of a form that holds a password, no file chooser, no link or form to a blocked host, and no text that looks like a key or a password. Typing checks the element again once it has the focus, so a page that turns a field into a password field as it is focused is caught.

## Why

- **The person approves what they can read.** "Submit" is a decision a person can make; "element 2" is not.
- **The page cannot swap the target.** A planted page that renumbers its elements, or a model that names the wrong one, gets a refusal and a fresh read, never a click on something the person did not see named.

## What it costs

- A model must copy the label exactly; a page whose button's words change as it is hovered needs a fresh read.
- The label shown on the card is the model's; it is checked against the page as the call runs, not as the card is drawn.
