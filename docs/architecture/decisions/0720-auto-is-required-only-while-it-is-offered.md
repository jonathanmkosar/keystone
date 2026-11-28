# 0720. Auto is required only while it is offered

November 28, 2026 · decided in P14-05

## The question

`router.may_switch_off` false means "the person may not take a chat off Auto". The spec also says an empty ladder means Auto is not offered and every chat uses the model picked. What does a person who may not switch Auto off do when their ladder is empty, or names no model in their registry?

## The decision

- Auto is **required** only while it is **offered**: `router.may_switch_off` is false and the ladder has at least one step that is a usable chat model in the person's registry, the same steps `ModelChoice.ladder` lists.
- With no such step, nothing is required. A named model is checked against `router.allowed_models` only, and `ModelChoice.may_switch_off` is true.
- `auto_required` is checked before `model_not_allowed`: while Auto is required every named model is refused with it, allowed or not, since naming any model would take the chat off Auto.

## Why

- Refusing every message of a person with no ladder would leave them with no way to chat at all, which no admin asks for by leaving a setting at false.
- The choice the picker reads and the refusal the server gives are worked out from the same ladder, so the screen never offers what the server refuses.

## What it costs

- An admin who clears a person's ladder lets them pick models again, within the allowed list. The admin's settings screen (P15-03) shows both settings side by side.
