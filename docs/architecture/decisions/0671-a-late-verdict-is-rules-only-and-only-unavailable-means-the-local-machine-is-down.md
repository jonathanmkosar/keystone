# 0671. A late verdict is the rules alone; only `unavailable` means the local machine is down

November 27, 2026 · decided in P14-03

## The question

The spec has two ways a verdict can go missing. "With no answer within `router.verdict_timeout`, or any error, the rules alone decide: the lowest candidate." And rule 4: "When the decision call or the last call to a local endpoint failed with `unavailable` within `router.local_down_for`, local steps are skipped for this request and the reply carries the notice." The decision model runs on the same machine as the local chat model. When the verdict does not come, is the local machine down?

## The decision

- **A timeout is not the machine being down.** The rules alone decide, with the lowest candidate, which may be the local chat model, at the default effort, with no notice. `Record.TimedOut` is set. An answer that arrives after the timeout is not used.
- **A local decision model that fails with `unavailable` is rule 4 for that message.** The local steps are skipped, the reply carries `local_unavailable`, the rules alone decide among what is left, and `Record.LocalUnavailable` tells the turn to keep the time as `Facts.LocalFailedAt` for the messages after it. A held chat with only local steps left is refused with `local_unavailable`.
- **A machine already known to be down is not asked.** When `Facts.LocalFailedAt` is within `router.local_down_for` and the decision model is local, no verdict is asked: rules only, "local machine not answering". A hosted or trusted decision model is still asked, and is offered no local step.
- **Any other failure** of the verdict, including `unavailable` from a decision model that is not local, is the rules alone, with no notice.
- **When the local chat model itself fails** with `unavailable` before any block is passed on, the turn (P14-04) may route the message again with `LocalFailedAt` set. The router gives the next step and the notice; this is within decision 0160, since nothing of the reply was shown.

## Why

- The brief names the risk that "a verdict waits behind the chat model on the same machine". A busy machine answers late; treating that as down would send easy messages to a paid model whenever the local model is working.
- `unavailable` is the seam's kind for an endpoint that did not answer at all (refused connection, no route), which is what "the machine is off" looks like.
- Asking a decision model on a machine known to be down only adds the timeout to every message for two minutes.

## What it costs

- A machine that is off without refusing connections (no answer at all) shows first as a timeout, and the lowest step, the local chat model, is tried; its own failure is then what marks the machine down. The turn's second routing covers that message.
