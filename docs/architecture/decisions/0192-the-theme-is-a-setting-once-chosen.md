# 0192. The theme is a setting once a person has chosen one

November 25, 2026 · decided in P11-01

## The question

The web shell keeps the theme in the browser, so the page can wear it before it paints and before anyone signs in. The registry also has `appearance.theme`, a setting each person may change, and the General screen now draws a control for it. Two places hold the same choice. Which one counts?

## The decision

**The browser's copy is what the page wears. The setting is what follows the person.**

- A theme chosen while signed in, from the sidebar, the Appearance screen or General, is worn at once, kept in the browser, and stored as the person's `appearance.theme`.
- At sign-in the page reads the settings. If the person has stored a theme, it replaces the browser's. If they have stored none, the browser's stands, and the setting's default is not applied.
- Signed out, the page wears the browser's copy.

## Why

- The sign-in screen has no person, so it has no setting to read. Something in the browser must hold the theme, or a person who chose light sees a dark flash on every visit.
- A person who picks a theme on their laptop expects it on their phone. Only the server can carry it there.
- The registry's default is `dark`, and the browser's default is to follow the system. Applying an unchosen default at sign-in would turn a light system dark the moment a person signed in. Reading only a stored choice avoids that.
- Storing the choice is a call that may fail. The theme is still worn and still kept in the browser, so a failure costs nothing the person can see.

## What it costs

- Until a person chooses a theme while signed in, General shows the setting's default, `dark`, even when the page follows a light system. The first choice puts them in step.
- Two people who share one browser profile share its copy between sign-ins. The second person's stored theme replaces it when they sign in. If they have stored none, they see the first person's.
- A theme changed on another device shows here at the next sign-in or reload, not at once.
