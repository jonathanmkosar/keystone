# 0201. Until approvals exist, a tool that changes something is refused, and an approval is a value only its own package can make

November 25, 2026 · decided in P7-01

## The question

Fixed rule 2: a side effect outside Keystone happens only under the approval policy, and content the agent read never grants approval. The tool loop (P7-01) comes before the approval policy (P7-02). What does the loop do with a tool that changes something, and how is the rule made to hold now in a way P7-02 extends rather than replaces?

## The decision

**Every tool says whether it only reads.** `ReadOnly` is false unless the tool's author sets it, so a tool that forgets to say is treated as one that changes something.

**One small package holds the rule's test**, `internal/tools/approval`: `Check(readOnly, approval)`. A tool that only reads may run. Any other tool may run only with an `Approval` that says a person agreed. The package imports nothing of Keystone's.

**An `Approval` can be made only inside that package.** Its one field is not exported, and no function makes one yet. Code anywhere else can hold only the empty value, which says nobody agreed. P7-02 adds the one function that makes an approval, there, from a person's answer to a card.

**Until then a changing tool is refused.** The call is answered with an error result the model reads ("approvals are not built yet"), recorded in the audit log with the outcome `refused`, and the turn goes on. The tool's function is never called.

**The rule is checked three ways.** The package's own tests; `verify`'s `fixed rules` check, where `internal/tools/approval` and `internal/tools` are the rule's guards and may not import a setting; and a new standing check, `approval`, which registers a changing tool on the real server, has the model call it, and fails if it ran.

## Why

- A rule that cannot be switched off must not depend on a setting, and the simplest way to prove that is a package that cannot import one.
- A flag that defaults to "changes something" fails safe: the worst a forgotten flag does is refuse a harmless tool.
- If anything outside the package could build an approval, a tool's result or a model's arguments could one day be turned into one by mistake. A type nobody else can fill makes the second half of the rule hold by construction.
- Refusing with a result, instead of failing the turn or hiding the tool, keeps the record valid and lets the model tell the person what it could not do. It is what an approval the person declines will look like after P7-02.
- The plan names "approval" among the standing checks. Starting it now, with the part that can be proven today, gives P7-02 a check to extend with the outside-content rule.

## What it costs

- No tool that changes anything can be built usefully before P7-02. None is in this item.
- The `approval` check depends on the test tenant's recorded turn calling `send_postcard`. If that fixture changes, the check says the rule was not tried, rather than passing.
- "Read-only" is the tool author's word. Nothing proves a tool flagged read-only really changes nothing; review of each tool is the check.
