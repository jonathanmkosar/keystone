# 0191. The General screen is drawn from the settings route, and the proof is a test of the route

November 25, 2026 · decided in P11-01

## The question

The item's proof is "every overridable setting in the registry has a control; a doc check proves it". The registry has 24 settings a person may change today, and every lane adds more. How does each get a control, and what proves that none was missed?

## The decision

**One screen, General, draws a control for every setting the route marks `changeable`, from the route's answer alone.** The control is chosen by the setting's type and options: a switch, a select, a number field, a text field or a text box. The title is made from the key and the words are the registry's description. The screen's code names no setting.

**The proof is a Go test of the route**, `TestEveryOverridableSettingHasAControl`, run by `verify`'s `tests` check. It reads the real registry and shows that every setting a person may change is returned as changeable, has a type the screen has a control for, and can be stored and reset; that no other setting is; and that no fixed rule is a setting.

The Profile, Appearance and Models and keys screens are written by hand, because they are not lists of settings.

## Why

- A screen written setting by setting is out of date the day another lane adds a setting. Lanes work at the same time and do not edit the interface.
- A check that compares a hand-kept list of controls with the registry proves the list was kept, and fails every time a lane adds a setting, until someone in another lane edits the interface.
- With a generated screen, "has a control" comes down to two things that can be tested without a browser: the route lists the setting as changeable, and its type is one the screen can draw. The test does both, and also stores and resets each one, so a control that would be refused is found too.
- The item's wording asks for a doc check. The `docs` check reads files, and a line in a file cannot show that a route returns a setting. A test that runs on every `verify` is the stronger form of the same promise, and the doc says how the screen is generated.

## What it costs

- A generated row is plainer than a designed one. The title is the key with its dots and underscores taken out, and settings are grouped by the lane that owns them, which is how the code is organised and not how a person thinks. A later pass can give a setting a label and a group in the registry, and the screen would read them.
- The theme has two controls: the switch on Appearance and a select on General. Both set the same setting.
- A new setting type would have no control. The test fails when a changeable setting has a type outside the five.
- Nothing on every `verify` opens the screen. That the page draws a row for each changeable setting was checked by hand in a browser. The app has no tests of its own yet.
