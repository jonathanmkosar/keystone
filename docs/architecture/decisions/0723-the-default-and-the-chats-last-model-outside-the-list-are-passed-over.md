# 0723. The default and the chat's last model outside the list are passed over

November 28, 2026 · decided in P14-05

## The question

The spec says a `chats.default_model` outside the allowed list is ignored. A turn that names no model, off Auto, uses the chat's last model first, then the default, then the first model in the registry (decision 0240). What if the chat's last model is outside the list, and what if nothing is allowed?

## The decision

- The order stays, among the allowed models only: the chat's last model if it is allowed, else `chats.default_model` if it is allowed, else the first allowed model by name.
- A turn that names no model is not refused for the chat's last model being outside the list: it is answered by the model chosen that way. Only a model the person names is refused (decision 0721).
- With no allowed model in the registry the choice has none. Off Auto its reason says the admin allows none, and "Pick Auto" when Auto is offered, and a turn then fails with `no_model`, as with an empty registry. On Auto it gives no reason: Auto answers, and the composer shows no warning.

## Why

- The person did not pick the chat's old model just now; refusing their message for it would ask them to undo the admin's change by hand in every chat.
- Treating the default and the last model alike keeps one rule, and the model choice shows the model that will answer before the message is sent.

## What it costs

- A chat's model can change under the person when the admin narrows the list. The picker shows the new one, and the reply names the model that wrote it.
