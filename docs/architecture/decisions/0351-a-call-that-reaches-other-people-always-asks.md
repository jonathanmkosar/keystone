# 0351. A call that reaches other people always asks, whatever the policy, and only a person's answer covers it

November 25, 2026 · decided in P12-02

## The question

P7-02's rule lets a person set a tool that changes something to `always`, so it runs without asking until the chat reads outside content. Sending an email, or inviting or telling the guests of an event, reaches other people and cannot be taken back from them. The brief says a send, and a change that notifies guests, must always ask "whatever the policy". Where does that live, and what decides it for one call?

## The decision

**`approval.Call` gains `Others`**: the call reaches other people. `Decide` checks it right after `never`: such a call asks, with the reason `others`, under `always`, `ask` and any unreadable policy, with outside content or without. `never` still refuses it. No standing approval is ever made for it.

**`tools.Tool` gains `ReachesOthers(input)`**, a function of the call's input, because the same tool can reach people or not: an event with guests does, one with none does not. It answers true for input it cannot read. The turn passes `tool.Reaches(input)` to `Decide` (one line in `internal/turn/approvals.go`), and the card says "Asking because this reaches other people, so it always asks, whatever you have set."

**`tools.Run` holds the rule a second time**: a call that reaches others runs only with an approval that is a person's answer to it (`approval.CheckReach`), never a standing one, and never as a tool that says it only reads. So a mistake in the turn, or a tool that wrongly says it only reads, still cannot send.

`gmail_send` reaches others on every call. The calendar tools reach others when the call names guests to invite, keep, tell or take off (decision 0353 makes sure a change to an event with guests names them).

The approvals screen marks a tool some of whose calls reach others ("Asks every time it sends to or tells other people, whatever is set here").

## Why

- The rule is fixed rule 2's: it belongs in `internal/tools/approval`, which imports nothing of Keystone's and reads no setting, so no setting or policy can switch it off, and the `fixed rules` check keeps it so.
- A flag per tool would make every calendar change ask, which the brief does not want: a change to an event only the person is on follows their policy. A function of the input is plain and keeps one tool for one action.
- Two guards, in the decision and where the tool runs, follow P7-02's pattern (`CheckCall`), so a call that reaches others never runs on a standing approval even if the turn's side were wrong. The `verify` step was seen to catch an invitation through the second guard when the first was taken out.

## What it costs

- A person who wants mail sent without a card cannot have it. That is the point.
- `never` still applies, so a person can keep the model from sending at all.
