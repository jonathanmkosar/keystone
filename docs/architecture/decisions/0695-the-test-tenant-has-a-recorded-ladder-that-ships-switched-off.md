# 0695. The test tenant has a recorded ladder, which ships switched off

November 27, 2026 · decided in P14-04

## The question

The proof asks for recorded turns of the test tenant to reach every step of a ladder, and a held chat on Auto to reach no hosted model. The test tenant has one model, `recorded-turns`, local, in no registry, and is given it whatever it names (decision 0144). A ladder needs several models of different classes, and the verdict needs a decision model.

## The decision

- Beside its one model the test tenant has four more, in code and in no registry, all played by the fake provider with the same recorded turns: `recorded-small` (local), `recorded-middle` (trusted), `recorded-large` (hosted) and `recorded-decisions` (local, decision). The hosted one costs nothing; it is hosted so the privacy filter has something to withhold.
- `fixtures/test-tenant/turns/router.json` holds the recorded verdicts: an easy message to the small step, a plan to the middle, a hard message to the large, an unsure verdict that moves up, the task of several steps to the middle, a verdict naming the hosted step for the held chat's message, and anything else to the small step.
- Its ladder ships empty, so nothing the test tenant does changes. `router.ladder` set to `recorded-small, recorded-middle, recorded-large` and `router.decision_model` to `recorded-decisions` for it (`go run ./tools/tenant setting`) shows Auto in a browser with no key; the tests and the `sensitive content` check set them in a throwaway folder.
- A step is opened through `routing.Plan` with the held mark, as a person's step is, so the privacy filter is the same code.

## Why

- Shipping the ladder on would change every recorded turn's model and every stream the other checks read.
- Models in code, beside the one already there, keep the registry the person's and the fixtures data.

## What it costs

- Three model names that exist only for the test tenant; a real registry may not use them, and none does.
