# 0674. The router's window check mirrors the budget's, held to it by a test

November 27, 2026 · decided in P14-03

## The question

Rule 3 says every step must fit the call in its window, "the budget's check". The budget (`internal/budget`) has that check, `budget.Measure`, but the package also reads its tunables from the settings registry (`budget.FromSettings`), and a guard of fixed rule 3 may not import the settings, even through another package. How does the router apply the budget's check?

## The decision

- The turn passes the budget's estimate of the call's input as `Facts.Tokens` and the reply's room the call asks for as `Facts.ReplyTokens`.
- The router's `fitsWindow` is `budget.Measure` on those numbers, written again in four lines: the room kept for the reply is what the call asks for, or the model's output limit when it asks for nothing or for more (`budget.Reply`), and a step fits when the input and that room are no more than the model's window.
- A test in `internal/router` compares the two on every model of its registry, over input counts and reply sizes at and around each edge. Tests are not part of the package's imports, so the guard is unchanged.

## Why

- Importing the budget would make the router able to reach the settings, and the `fixed rules` check would fail, rightly.
- Splitting the budget so its check had no settings import would change a package of another part of the plan for four lines.

## What it costs

- Two copies of a small formula. If the budget's check changes, the test fails until the router's follows.
