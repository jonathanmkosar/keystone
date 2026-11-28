# 0673. The verdict chooses among the candidates, and moving up is the next candidate

November 27, 2026 · decided in P14-03

## The question

The spec says the verdict's `step` question is "a choice among the candidates", each described as "<label>: step n of m, smallest first" when it has no note; that an unsure verdict "moves one candidate up"; that the effort is a choice of low, medium or high and "the router never asks for more than `router.max_effort`"; and that "Try a stronger model" writes the turn "one step above the step that answered". Several readings are open: what n and m count, what happens when the cap is below high, what counts as an answer, and what "one step above" is when the step above cannot take the call.

## The decision

- **n of m counts the candidates**, the options the decision model is choosing among, not the ladder. A step the rules left out is not shown, so "step 2 of 3" means the second of the three it may pick.
- **The effort question always offers low, medium and high**, and the effort chosen is lowered to `router.max_effort` afterwards. `router.default_effort` is capped the same way.
- **A verdict is used only when it answers both questions** as `seam.ValidateDecisionResult` asks, its step is a candidate and its effort one of the three. Anything else is a failure, and the rules alone decide.
- **Moving up is the next candidate**: the step above the one chosen among the steps the rules left, skipping any that cannot take the call. On the top candidate an unsure verdict stays. A probability equal to `router.min_confidence` is not under it.
- **`Stronger`** is the first candidate above the step that answered, at the effort that turn used, capped. It is refused with `no_stronger` on the top candidate, or when the step that answered is no longer on the ladder.
- **The reason line** follows the spec's examples: the label, the effort and the probability to two places for a verdict; "Moved up: decision unsure (0.48)"; "Rules only: ..." with the cause; and "; switched from <label>" at the end when the step is not the previous message's.

## Why

- The decision model sees only the candidates, so numbering anything else would describe options it does not have.
- A fixed set of three options keeps every verdict's request the same shape, and a choice question needs at least two options, which a cap of low would leave one.
- Moving to a step the rules ruled out would send a call to a model that cannot take it.

## What it costs

- With a cap below high, the decision model may answer an effort that is then lowered; the record keeps both, `effort_answered` and `effort`.
