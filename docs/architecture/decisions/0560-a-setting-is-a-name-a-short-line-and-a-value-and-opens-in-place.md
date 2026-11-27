# 0560. A setting is a name, one short line and its value, and its row opens in place

November 27, 2026 · decided in P11-04, at the owner's report that the settings had very long explanations

## The question

General drew each setting with a name made from its key, the key in monospace, the registry's whole description, the control, and the word "Default". The owner, on his phone: "For settings seems like we have very long explanations, maybe we can shorten and [have] hints with more detailed explanations. Explore different approaches and pick the best user experience and aesthetically pleasing approach." The registry holds 346 settings, 146 of them a person's own, so the screen was a page of paragraphs nobody reads. How should a setting be shown so that it is read at a glance, with the long text one step away?

## What was tried

Three sketches were built on the real registry, behind `?sketch=a`, `b` and `c` (commit a12b53d has them), and looked at in a browser at 375 and 1280 pixels wide, in the light and the dark theme. Each had the same search box, the same plain headings and the same short lines, so only the row differed.

**a. The short line, and "More" opens the long text in place.** A name, the short line, the control at the right (a switch, a select, a number box), and a "More" link under them.

- It read well in both themes, and the long text opened where the eye already was, with the settings above and below still in view.
- Every row was three lines and a boxed control. A phone showed four settings at a time.
- "More" was the most repeated word on the page: 146 times.
- A number box at the right took a third of a phone's width, so the short line wrapped to two and three lines beside it.

**b. The short line, and an info button that opens a sheet on a phone and a popover on a desktop.** The same row with a small circled *i* after the name in place of "More".

- Rows were a line shorter than in a.
- 146 circles. Under a long name the circle wrapped to a line of its own, and at 22 pixels it was too small for a thumb; made large enough, it made the rows as tall as a's.
- On a phone the sheet dimmed the page and covered the control it explained. On a desktop the popover covered the next three rows. Either way two explanations could not be read side by side.
- It is two behaviours to build and keep right: the focus, Escape, a tap outside, and a sheet that must follow the visible part of the page (`visualViewport`), which is where a picker was once squashed by the keyboard.

**c. A compact list of name, value and a chevron, and a view of its own for each setting.** One line a row; a tap opens a view with the short line, the control, the long text, the default and the key.

- By far the calmest and the densest: twelve settings on a phone's screen against four, each value readable down the right edge, and a changed value in copper.
- The short line was not in the list. A name alone does not say what "Context fill" does, so reading the list meant opening rows.
- Flipping a switch took three taps and a change of screen. Each look at an explanation was a round trip, and coming back had to find the place again in a list of 146.

## The decision

**The rows of c, opening in place as in a, with the short line always shown.**

- **Closed, a row is the setting's name and its value on one line, and its short line under them.** The value is plain text ("24h", "Off", "This month", "Empty"), not a box. A long text shows no value: its first words say little.
- **A switch is the one control in the row.** It is obvious without reading, and it flips where it is.
- **The whole row opens it**, with a chevron at its right end and no word. The name is the button (`aria-expanded`), the short line is its description, and its target is stretched over the row, so a thumb does not have to find the words. The switch sits over it as a button of its own.
- **Open, the row adds, in place:** the registry's whole description; the box to change the value in, with what it takes ("A whole number.") and "Saved." once it is kept; the default, with "Reset to default" when the value differs; and the key in monospace, with a Copy button. A setting that is not the person's says who sets it, and shows a value the row cut short in full.
- **The key is in the opened row only**, and the search still finds a setting by it. **"Default" under every control is gone.** A row whose value differs from the default carries a small "Changed" mark, and a Changed filter beside the search lists them.
- **Above the list:** a search box over names, short lines and keys; a select of the sections; and the Changed filter. Nothing takes the focus by itself, and at phone width every box of the settings screens is 16px, so the phone's browser does not zoom the page.
- **There is no overlay**, so there is none to anchor. The `settings at a glance` check fails if a fixed overlay is added to the screen without following `visualViewport`.

## Why

- Reading comes first, and the short line is what is read. Only a and b kept it in view; c did not.
- Looks second: c's rows were the best of the three. A value as plain text at the right edge is what made them calm, and it costs one tap before typing. That tap is no loss: a number should be read about before it is changed, and the box that opens is 16px and as wide as it needs to be.
- A switch is the exception because flipping it is the whole change, and three taps for it was c's worst fault.
- Opening in place keeps a person's place in a long list, lets two rows be open at once, and is one behaviour at every width, built from a button and a region with no focus to manage.
- Dropping "More" and the circle removes 146 copies of the same mark. The chevron says the same and reads as part of the row.

## What it costs

- Changing a number takes a tap more than a box in the row did, on a desktop too.
- A phone shows about eight settings at a time, not c's twelve. The section select and the search are what make 146 rows passable.
- A long text is not previewed in its row.
- The screen has no address for one setting. A link to a setting would need the row to open from the address.
- The look was chosen without a mockup, from the tokens and the cards the settings screens already had. It is provisional until the owner has seen it on his phone.
