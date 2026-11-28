# 0715. The admin's settings are drawn from the registry by scope, and a person's own settings are neither shown nor set there

November 28, 2026 · decided in P15-03 · provisional on the mockups: the Admin draft has a Settings tab and no drawing of it

## The question

What does the admin see of a person's settings, and how do settings other lanes add, such as the router's policy (P14-05), reach the admin's screens?

## The decision

- `GET /api/v1/admin/people/{id}/settings` returns that person's `tenant-admin` settings alone: those the admin sets for them. A person's own (`tenant`) settings are not returned: some hold words of theirs (the name of their memory space, for one), and A2 gives the admin numbers and settings, not a person's words. The admin sets only the default a person's own setting starts from, for the whole install (decision 0712).
- `GET /api/v1/admin/install/settings` returns every `install` setting but `data.dir`, and every `tenant` setting as a person default, each with its scope.
- The routes and the screens name no setting. Each row is drawn from the registry by its scope, with the same title, short line, grouping and controls as a person's General screen (`settingsView.ts`), so a `tenant-admin` setting another lane adds, the router's policy among them, appears on A person → Settings with no change here.
- Screens: Admin → Settings, in two parts ("The whole install" and "Each person starts with"), with a search, a section and "Changed", and each restart setting marked "After a restart" or "Waits for a restart"; Admin → People, a list with a link to each person's settings; A person → Settings.

## Why

- Scope is what the registry already says about who may change a setting, so the screens cannot drift from the server's rule.
- Leaving a person's own settings out keeps to A2 and costs nothing the proof needs.

## What it costs

- The admin cannot see whether a person kept the install's default or chose their own. If the owner wants that, a count per setting ("3 of 4 people follow the default") says it without showing a value.
- The People list here is a stand-in until P15-02's People and A person screens; the integrator keeps one.
