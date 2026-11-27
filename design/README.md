# Keystone: the mark and the theme

Decided by Jonathan on November 24, 2026. Drafts and every earlier round are on the "Keystone UI" design canvas.

## The mark

Three nested arches and one tall, sharp keystone that locks all three. It comes from the Art Deco doorway: arches set one inside the next, as on the stage of Radio City Music Hall and the crown of the Chrysler Building.

- The arches are three coppers, darkest inside and brightest outside on a dark ground.
- The keystone is ivory. It is the only piece that is not copper.
- No rays, no letter, no outline. A thin gap separates the keystone from the arches.

Files, in `mark/`:

| File | Use |
| --- | --- |
| `keystone-mark.svg` | The mark on dark grounds. Transparent background. |
| `keystone-mark-tile.svg` | The mark on a dark rounded tile. Kept for an app icon that needs a ground of its own; not used on light pages. |
| `keystone-mark-light.svg` | Free-standing on light grounds: deeper coppers and a dark stone keystone. The light-page mark, chosen November 27, 2026. |

## Colours

`tokens-draft.json` holds both themes. Dark is the lead theme, because the mark was chosen on dark.

| Role | Dark | Light |
| --- | --- | --- |
| Page | #15191a | #f4f2ec |
| Raised (cards, composer) | #1d2324 | #fcfbf8 |
| Sunk (sidebar) | #0f1213 | #e9e6dd |
| Line | #2e3637 | #d5d0c3 |
| Text | #f1ece0 (ivory) | #1e2422 |
| Quiet text | #a8aba3 | #565d58 |
| Copper, inner arch | #6b3a1c | #e6b998 |
| Copper, middle arch | #b0683a | #c98253 |
| Copper, outer arch | #e8a878 | #9a4f22 |
| Keystone | #f1ece0 | #2f3a3d |
| Buttons | #e0915a, text #2a1305 | #a85a28, text #ffffff |
| Soft accent | #3b2412, text #e8a878 | #f6e3d3, text #8a4318 |
| Done | #8fc7a1 | #39664b |
| Links | #86bfe6 | #22587f |
| Errors | #f0907f | #9c3527 |

Text and button pairs were checked at 4.5 to 1 or better. Neutral grounds, type and spacing are shared with Waymarks. Copper replaces Waymarks' moss as the accent, so the two apps can be told apart.

## Decided on November 27, 2026

- The wordmark is Fraunces, as in Waymarks.
- The four draft screens (`Home`, `Scheduled`, `Operations`, `Admin`) are approved, and were recoloured in copper the same day.
- On light pages the mark is free-standing, with no tile: deeper coppers and a dark stone keystone, `keystone-mark-light.svg`. The owner turned down the dark tile. The app draws it from the light theme's tokens, and the browser-tab icon (`web/app/public/mark.svg`) carries both versions, chosen by the browser's own light or dark setting.

## Still open


- Motion: the idea of copper lighting up as an agent works has not been redrawn for this mark.
