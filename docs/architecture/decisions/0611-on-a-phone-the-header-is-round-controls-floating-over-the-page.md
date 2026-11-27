# 0611. On a phone the header is round controls floating over the page

November 27, 2026 · decided in P10-07, the phone layout the owner asked for

## The question

The owner asked for the interface to be "more like" another app's phone screens, with five pictures of it. Its header is no bar: a round menu button, a pill with the chat's title and its project under it, and a pill with a working indicator and `...`, floating over the conversation, which scrolls beneath them and fades out at the top. Keystone's phone header was a full-width bar with a bottom border. How should Keystone take the layout and feel without taking the other app's brand?

## The decision

- **At 760 pixels and under, `Shell.tsx` draws a `float-header` in place of the header bar**: a round 44-pixel button with a two-line menu icon (`menu` in `Icon.tsx`) that opens the sidebar drawer; in the middle, the chat's title as a pill (`ChatTitle` with `phone`), with the chat's project in small quiet text under it when it is in one; on the right, a pill with a spinner while a turn of the chat runs and the `...` button (`ChatMenu`, decision 0590). Tapping the title pill opens the menu the title opened before (rename, star, delete). On the new chat screen the right holds the Incognito switch as a round eye-off button; on other pages the middle is the page's name in a pill.
- **It floats.** The header is absolutely placed over the main area, under the drawer's scrim, with a fade from the page colour to transparent behind it; taps on its empty parts reach the page. A chat's scroller and the new chat screen run beneath it; every other page starts under it (`is-under` on the main area).
- **Translucent surfaces with a solid fallback.** The tokens hold no translucent colour, so a pill's ground is `--raised` mixed with transparency, blurred behind with `backdrop-filter`, inside `@supports`; where the browser cannot blur it is `--raised`, solid.
- **Keystone's own brand throughout**: the mark, the name, the tokens and the fonts of `design/README.md`. Nothing of the other app's mark, name or typefaces. The spinner is copper.
- **Incognito's choice moved out of the new chat screen's state** into a small store in `Incognito.tsx` (`useNewChatIncognito`, `setNewChatIncognito`), since the header and the screen both use it; the screen turns it off when it goes, as before. On, the button wears the soft accent and the screen says so under the greeting.

## Why

- A floating header gives the conversation the whole height, which on a 390 by 844 screen is most of what the owner was asking for.
- A separate header element on a phone, rather than restyling `.header`, keeps desktop exactly as it was and keeps the phone rules out of the way of the desktop ones.
- A store rather than lifting state through the router is the smallest change that lets two places share one switch.

## What it costs

- The title's caret is gone on a phone: the pill is the button.
- The header covers the first 64 pixels of every page on a phone; pages are padded for it, but a page with its own sticky top would need its own look.
- The fade and the blur have not been seen on a real phone, and a browser without `color-mix()` draws a plainer fade.

`verify` fails if a record in `decisions/` is not linked from `overview.md`.
