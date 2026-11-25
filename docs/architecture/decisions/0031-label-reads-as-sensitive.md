# 0031. A label that is not `normal` reads as sensitive

November 24, 2026 · decided in P6-01

## The question

Fixed rule 3 says content from a sensitive space reaches only models the registry marks local or trusted. Recall and the embedding job decide that from the label on each piece of content a knowledge store returns. What does a missing or unknown label mean?

## The decision

Every entry, page, source and search hit carries its space and that space's label. `Label.Sensitive()` is true for every value except `normal`. A blank label, the zero value of each type, and a label the code has never seen all read as sensitive. An adapter that cannot tell says sensitive.

An adapter reads the label from the store each time. If a space is made sensitive after the handle was opened, the next read says so.

## Why

- The mistake most likely to be made in an adapter is to forget to set a field. With this rule, forgetting sends content to fewer models, not more.
- A fixed rule should not rest on every adapter being written carefully. It rests on one method.
- A label held from when the handle was opened would let content out for as long as the handle lived after the person marked the space.

## What it costs

- An adapter with a fault looks like a store where everything is sensitive: content stays off the cloud models and nothing says why. The contract suite checks labels on every read to catch that early.
- A third label added later reads as sensitive until the code is taught it.
