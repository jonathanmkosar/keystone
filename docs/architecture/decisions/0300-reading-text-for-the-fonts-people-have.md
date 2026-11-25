# 0300. A reply is set for the fonts people have: 17px, crisp serif text, sans semibold headings

November 25, 2026 · decided at the owner's report that chat text in the dark theme was hard to read

## The question

The chat mockups set a reply in Source Serif 4 at 18px on 30px lines, with headings in the same serif at weight 600. Decision 0072 names the design's fonts and does not bundle them, so on a machine without Source Serif 4, which is nearly every machine, a reply fell back to Georgia. The owner found the chat hard to read in the dark theme: the size and the weight looked off. What should a reply be set in until the fonts are bundled?

## The decision

- **A reply is 17px on 28px lines**, down from the mockup's 18px on 30px.
- **The reading stack is `"Source Serif 4", Charter, "Iowan Old Style", Georgia, serif`.** Apple devices draw Charter or Iowan Old Style, and other systems Georgia. All three have thin, crisp regular weights on a dark ground.
- **Headings are set in the interface's sans at semibold** (22, 20 and 18px for the first three levels), not in the serif's bold. Bold inside a reply asks for weight 600, which a font with a semibold uses.

## Why

- Georgia has a large x-height, so at 18px it read bigger than the rest of the screen. At 17px it sits with the 15px interface around it.
- Georgia has no semibold, so weight 600 drew its bold, which is heavy, and light text on a dark ground looks heavier still. Every heading in a model's reply shouted. The sans semibold (IBM Plex Sans when present, the system's own otherwise) gives the same order of headings at a far lighter weight.
- Sitka Text, Windows' screen serif, was tried and set aside: its regular weight is wider and heavier than Georgia's on the dark ground, which is the opposite of what was asked.

## What it costs

- The screen no longer matches the mockup's sizes and heading face exactly. The mockups are drafts awaiting the owner's approval (P1-04), and this records why the build differs.
- Bold phrases inside a paragraph still use Georgia's bold on systems without a semibold serif.
- The real fix was to bundle Source Serif 4 and IBM Plex Sans, and the owner agreed the same day: decision [0301](0301-the-reading-and-interface-fonts-are-bundled.md). The sizes here stay.
