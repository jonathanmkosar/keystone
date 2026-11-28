# 0724. A stronger model and the ladder are outside the allowed list

November 28, 2026 · decided in P14-05

## The question

The spec says Auto uses the ladder whatever `router.allowed_models` says. "Try a stronger model" writes a turn of Auto again one step up. Is it a pick by hand, held to the list, and is it refused while Auto is required?

## The decision

- Try a stronger model is a turn on Auto: the router picks the step above the one that answered, on the admin's ladder. The policy does not refuse it, whatever the allowed list says and whether or not the person may leave Auto.
- The ladder's steps are not added to `ModelChoice.allowed`: a person may reach them through Auto and still not pick them by hand.

## Why

- The admin sets both the ladder and the list. A step on the ladder is a model the admin chose for Auto; the list is about what the person may choose themselves.
- A person who may not leave Auto still needs a way to ask for more when a small step's reply falls short, and Try a stronger model is that way.

## What it costs

- A person kept on Auto can reach the top step of their ladder with a click on each reply. The admin narrows the ladder to stop that; usage shows how often it is asked for (P14-07).
