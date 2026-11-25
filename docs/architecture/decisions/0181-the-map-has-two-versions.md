# 0181. The map has two versions, and the open one names nothing from a sensitive space

November 25, 2026 · decided in P6-04

## The question

The map starts every call, and it is made from the catalog, which holds the names, kinds and titles of pages in sensitive spaces. Fixed rule 3 says content from a sensitive space reaches only models the registry marks local or trusted. What does a model that may not read sensitive content get?

## The decision

**Two versions are made from one catalog, and one function gives out either.** `Map.Text(mayReadSensitive bool)` takes the model's own answer, `Model.MayReadSensitive()`. The two texts are not exported.

**The open version leaves a sensitive space out whole**: its name, its kinds, its titles and its count of pages. It carries one line that says how many spaces were held back.

**A space is sensitive when any entry in it is.** A blank or unknown label reads as sensitive (decision 0031).

**The package is a guard of rule 3** in `tools/verify/fixedrules.go`. It cannot import the settings registry or the tenant overrides, so its sizes come in as arguments.

## Why

- A title is content. "Appointment with the heart clinic" says as much as the page does. A kind can say as much as a title, and so can the name of a space.
- The count of pages in a space says little, but it says something about one named thing, and the name is already held back. Leaving the whole line out is the rule that needs no judgement.
- The number of spaces held back is kept because without it a hosted model would tell the person their store holds nothing on a subject, when the truth is that this model may not look. With it, the model can say that some of the store is closed to it. A number names nothing.
- A plain yes or no keeps the package free of the model registry. The registry already has the one place a class becomes yes or no.
- With the texts not exported, a caller cannot pick the whole version by mistake. It has to say `true`.

## What it costs

- A hosted model learns that sensitive spaces exist, and how many. If that is too much, the line can go: it is one `case` in `write`, and the count would still reach the turn inspector through `WithheldSpaces`.
- The start of a call differs between a hosted and a local model, so a chat that moves between them has two cached starts.
- A store with one mislabelled entry hides its whole space from hosted models. That is the safe side.
- The profile is not covered. See decision 0180.
