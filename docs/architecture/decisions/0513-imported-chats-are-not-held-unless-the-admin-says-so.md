# 0513. Imported chats are not held unless the admin says so before the import

November 26, 2026 · decided in P12-03

## The question

Fixed rule 3: content from a sensitive space reaches only models the registry marks local or trusted. Keystone keeps the rule for chats by holding a chat that was sent such content: from then on it goes only to models that may read it, its recap is written only by them, and the past-chat search leaves it out for a hosted model. The item says: if New Keystone marked anything sensitive, keep the mark; otherwise say what was assumed. What is an imported chat?

## What New Keystone did

Read from its source. It has no mark for sensitive content: no column, no table, no setting. It read one Waymarks space, the one named in its configuration, and put the pages it recalled into each call. It sent every chat to whichever model it was set to, which could be the home gateway's or a hosted provider's (`claude_chat.py`).

## The decision

- **By default no imported chat is held.** Nothing in the source says any should be, and the source itself treated every chat as fit for any model.
- **The admin can hold them, before the import**, with `chats.import_hold`:
  - `wiki` holds the chats in which New Keystone read the person's wiki: a turn whose call was given recalled Waymarks pages (`context.assembled` with `waymarks_pages` above zero), or in which the model looked a page up (`wiki.lookup`);
  - `all` holds every imported chat.
- A hold is written **in the transaction that writes the chat**, so a chat is never in the record unheld and then held. It is the mark `recall.Hold` keeps, a row in `turn_recall`, on a turn of the chat that calls no model, with the actor `import` and the import's request id.
- The dry run counts the chats the setting would hold.

## Why

- Holding everything by default would make the import nearly useless for its purpose: the owner's everyday model is hosted, and a held chat is left out of the past-chat search for a hosted model and cannot be continued on one, for good. He would have his old chats in the list and Keystone would act as if they were not there.
- Holding nothing with no way to say otherwise would be an assumption the owner cannot overrule. Whether the Waymarks space New Keystone read is one he now calls sensitive is his to say, and the source cannot tell.
- `wiki` is the precise answer when it is: the only content of a sensitive space New Keystone could have put in a chat came from the wiki, and its events say in which turns the wiki was read.
- A hold cannot be taken back, so it must be chosen before the import and not after: the setting, not a button.
- The rule's guard is unchanged and reads no setting. The setting only decides whether the import asks for the hold that every part of Keystone already obeys.

## What it costs

- The default trusts that the owner's old chats may go where they already went. The choice cannot be made afterwards: a second run brings nothing that is here already, and a chat that was deleted does not come back, so changing the setting after an import holds nothing. The way back is the backup from before the import. The runbook puts the choice before the import for this reason.
- The importer writes one row in `turns` and one in `turn_recall` itself, in SQL, for each held chat. Calling `recall.Hold` would need a second transaction on a store with one connection. A test proves `recall.Held` and `recap.Sensitive` both see the mark.
- `wiki` holds a whole chat for one turn that read the wiki, as a hold always covers the whole chat.
