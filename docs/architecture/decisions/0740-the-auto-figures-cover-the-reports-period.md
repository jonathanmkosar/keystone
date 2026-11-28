# 0740. The Auto figures cover the report's period

November 28, 2026 · decided in P14-07

## The question

The spec's Auto card shows "this month on Auto against the same tokens priced at the ladder's top step". The usage report it sits in covers a period the person picks: this month by default, last month, the last 7 or 30 days, a month, or a range. Do the Auto figures always cover this calendar month, or the report's period?

## The decision

- The Auto figures cover the report's period, summed from the same records as the report's total and tables. With no period asked for, that is the person's `usage.report_period`, `this_month` as shipped: the spec's "this month".
- A call is in the period of its own time, as everywhere in the report. A turn, for the counts of turns and stronger asks, is in the period of its first verdict.
- The card says "On Auto this month" for this month and "On Auto in this period" otherwise; the period in words is shown just above it.

## Why

- A card about this month under a table about October would show two periods on one screen, and the Auto figures would not be a part of the total above them.
- One set of records for the whole report keeps "the figures equal sums over the usage records" true for every field at once.

## What it costs

- A turn whose verdict is written just before midnight UTC on the last day of a month, and whose calls end after it, is counted as a turn in one month and its calls in the next. Over two months together both add up; a test holds them to it.
