# 0220. The rule decides each call from values it is given, and an approval covers one call and is used once

November 25, 2026 · decided in P7-02

## The question

The approval policy reads each person's policy for each tool, which is stored, and whether the turn has read outside content, which the turn knows. Fixed rule 2 says no setting can switch the rule off, and decision 0014 says the code that enforces a rule may not read a setting. P7-01 left an `Approval` type that nothing could make. How does a person's policy or answer become an approval without the rule's package reading anything, and what stops one approval from running something else?

## The decision

**`approval.Decide` is the rule, and it takes values.** The tool's name and input, whether it only reads, the person's policy, and whether the turn has read outside content. It answers run, ask or refuse. `never` refuses. A tool that changes something asks once the turn has read outside content, whatever its policy. `ask`, and anything that is not one of the three policies, asks. `always` runs. The default is `always` for a tool that only reads and `ask` for one that changes something.

**An approval is made only in that package**, by `Decide` for a changing tool set to `always` with nothing read from outside, and by `Answered` from a person's recorded answer. The turn reads the answer from the row the approval route wrote, and from nothing else.

**An approval covers one call**: the tool's name and a digest of its input, compacted as JSON. `internal/tools` runs a changing tool only through `approval.CheckCall`, which refuses an approval given for another tool or other input.

**An approval is used once.** The turn marks its row just before the call runs, and a row already marked is never run again, also after a restart.

## Why

- The rule stays in a package that cannot read a setting, as decision 0014 asks, and the part that reads the database stays in `internal/turn`. A policy that says `always` cannot reach past `Decide`'s outside-content line, because that line comes first.
- A value only the rule can make is what P7-01 built for. Binding it to the call means a bug that hands the wrong approval to the wrong call is refused where the tool runs, not trusted.
- A side effect that runs twice after a restart is the worst failure this part could have. Marking the row first means a crash at the wrong moment loses one run, which the model is told about, rather than repeating it.

## What it costs

- A call marked used whose tool never finished is not retried. The model reads that its approval was used and is told to check before asking again.
- Input that is the same but written with its keys in another order is another call. A model's call is stored and read back as written, so this has not been seen.
- "Read-only" and "inside" are still the tool author's words (decision 0201).
