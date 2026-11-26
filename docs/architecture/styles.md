# Styles

Folders: `internal/styles` (the record and the rules), `internal/turn/styles.go` (the routes, and what each call carries), `web/app/src` (the screens). Built in P9-05.

A style is a saved preset of tone and format: a name and instructions on how replies should be written. Every person has the built-in styles and may make their own. Each chat uses one style, and every call of the chat carries the style's instructions at the start, in the standing start's style slot, where a provider caches them. The style changes the words a model is given and nothing else.

The rest of this page is written with the item's write-up.
