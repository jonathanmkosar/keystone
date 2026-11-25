# 0180. The profile is one text, and it goes to every model

November 25, 2026 · decided in P6-04

## The question

The plan calls the profile "a few always-loaded facts and preferences". Is it a list of facts, each a row with its own source and date, or one text? And since it starts every call, does fixed rule 3 touch it?

## The decision

**The profile is one text for each tenant, written by the person, kept in one row of `person_profile`.** `SetProfile` replaces the whole of it and refuses a text over the limit it is given.

**It has no label and goes to every model.** It is not content from a space, so fixed rule 3 does not apply to it, and the package makes one version of it.

## Why

- A person edits a short text more easily than a list of rows, and a model reads it the same either way.
- One text is the same from call to call until the person changes it, which is what a cached start needs.
- A list of facts with sources is what the knowledge store already is. Facts that need a source belong there, and the map points to them.
- A profile with a label would be two profiles, or a profile a hosted model never sees. A person who uses only hosted models would then have no profile at all.

## What it costs

- The profile is the one place a person can put something sensitive in front of a hosted model without a label stopping it. The screen that edits it must say that it goes to every model.
- The memory pass (P6-06) cannot add one fact to it without rewriting the text. If it is to offer facts for the profile, it should offer them to the person, who edits the text.
- No history. The old text is gone when a new one is set.
