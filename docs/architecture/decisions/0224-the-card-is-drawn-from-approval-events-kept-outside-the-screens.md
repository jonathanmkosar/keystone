# 0224. The approval card is drawn from approval events kept outside the screens, and forgotten when the turn ends

November 25, 2026 · decided in P7-02

## The question

The chat screen folds a turn's events into a reply in `turns.ts`, and two other lanes were changing the chat screen while this item was built. Where does the screen keep the calls that wait for an answer, and where is the card drawn?

## The decision

**`api.ts` hands every `approval` event to listeners** (`onApproval`), one added line in the reader of a turn's stream, and does not add it to the turn's event union. **`Approval.tsx` keeps the approvals by chat**, outside the screens, as `turns.ts` keeps arriving replies (decision 0151), and forgets a chat's when its turn ends.

**The cards are drawn under the conversation**, one line in `Chat.tsx`, in the conversation's column. The step row of a call that waits asks `Approval.tsx` whether it waits, and says "waiting for you".

**After the turn ends the record speaks.** The card is gone, and the step row says what became of the call: its result, or "declined" or "not run" from the server's sentence (decision 0204).

## Why

- The fewest changes to files others were changing: a new file for the card, a new stylesheet, one line in `Chat.tsx`, one in `api.ts`'s reader, a few in `Conversation.tsx`.
- An answer belongs to the turn that is running. Once the turn has ended the card has nothing to ask, and the record already says what happened.
- A reload in the middle of a wait joins the turn's stream from its first event (P4-04), so the card comes back without a route of its own.

## What it costs

- A card is below the whole reply, not inside it beside its step. A reply's last step is the one that waits, so it reads in order.
- `Approval.tsx` keys the waiting row by the call's id within the chat on the screen. Call ids are the model's own and repeat across chats, which the chat scoping covers.
- What the card said is not kept after the turn ends. `GET …/approvals` still lists it.
