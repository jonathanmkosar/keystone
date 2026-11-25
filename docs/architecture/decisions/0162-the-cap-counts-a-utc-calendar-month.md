# 0162. The cap counts a calendar month in UTC, and every model that is not local

November 25, 2026 · decided in P3-08

## The question

"Hosted spend defaults to a $100 monthly cap per tenant." Which month, and which models?

## The decision

**A month is a calendar month in UTC.** A usage record keeps its month, the first seven characters of its time, in a column of its own, and the cap sums one month's rows.

**A call counts when its model's class is not `local`**: `hosted` and `trusted` both. The class is copied onto the record when the call is made.

**Records are append-only**, with the audit log's triggers, and outlive the chat they were made for.

## Why

- A calendar month is what a provider's bill and a person's sense of "this month" share. A rolling thirty days never starts again on a day anyone can name.
- UTC needs no setting and gives one answer on every machine. The cost is that the month turns over in the evening of the last day for someone west of Greenwich, which matters once a month and only at the cap.
- `trusted` is a statement about privacy, not about price. A trusted model is run by someone else and sends a bill.
- The class is kept on the record because an entry can be changed or removed later, and last month's spend must not change with it.
- A spend that could be deleted is a cap that could be reset. The table refuses it, whichever code asks.

## What it costs

- A model on the home network that does cost money, such as a gateway that passes calls on to a paid provider, is `local` to the registry and so outside the cap. Its entry can carry a price, and its cost shows in the month's total, but it is not stopped. Marking such a model `trusted` puts it under the cap.
- The table only grows. One row is small, and a tenant makes thousands of calls a month, not millions.
