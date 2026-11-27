# Brief: admin and views

Type: new
Requester: the owner
Owner: the owner
Status: fuzzy
Last updated: November 26, 2026

## The ask

"I want to add another feature we make admin login and view of operations and admin view. I want to make users ui configuration and views configure. For example showndiffenr submenus for different users"

## The problem

Keystone has no admin. Everything an admin would do is done from the command line, a setting that belongs to the admin cannot be changed from the browser, and nobody can see how the whole install is doing. Every person also gets the same menus, whatever they use Keystone for.

## Why now

The model router's brief gives the admin three settings for each person, and there is no screen to set them on. Operations and Usage are built for one person and both list "an admin's view of every person" as not there yet.

## Done looks like

Not settled. Draft:

- A person marked as an admin signs in as themselves and reaches an admin area that others cannot.
- The admin sees Operations for the whole install: each person's spend, room, failures and jobs as numbers, and the install's own limits.
- The admin changes the settings that are the admin's, for each person and for the install, from the browser.
- The admin chooses which views and menu items each person has. A view a person does not have is refused by the server, not only hidden.
- A person arranges or hides the views they do have.

## Out of scope

Not settled. Draft:

- An admin reading another person's chats, files, memory or traces. Fixed rule 1 stands.
- Teams or shared spaces. A tenant stays one person.

## Constraints

- Fixed rule 1: no person sees another person's data. An admin is a person.
- Every admin read and change of a person's numbers or settings is recorded in that person's audit log.
- The fixed rules stay out of reach of every role.
- The first admin cannot be made from the browser.

## What already exists

- A tenant is one person, with their own database and sign-in. There are no user names and no shared table of people (`docs/architecture/tenants.md`, `signin.md`).
- A setting has a scope: `install`, `tenant-admin` or `tenant` (decision 0001). `tenantsettings.MayChange` already knows the role `admin`, and the audit log already has the actor `admin`.
- There is no admin role in the server. A `tenant-admin` or `install` setting is listed on the settings screens and cannot be changed there, and there is nowhere to store an install setting (`settings-screens.md`, `api.md`).
- Operations and Usage show each person their own, and the disk as an install fact (decision 0460). Both name the admin's view as not built.
- People are added with `go run ./tools/tenant add`.
- `design/screens/Admin.html` and `Operations.html` are drafts in the first round's colours.
- An open question from the models lane: who may change a person's model registry. Recommended there: the admin only.

## Terms

- **Admin**: a person the install marks as one. Not a second kind of account.
- **View**: one screen or menu item a person can open, such as Scheduled, Operations or Connectors.
- **View set**: a named list of views that the admin gives to a person.

## Decisions

None settled. The spec, `docs/plan/SPEC-router-and-admin.md` Part 2, takes the recommended answers below as assumptions until the owner answers.

| Decision | Why | Hard to reverse? | Owner |
|---|---|---|---|
| Assumed (A1). An admin is a person with a mark, set only from the command line, with a step-up before the admin area | One sign-in per person; no browser path to becoming an admin | No | the owner |
| Assumed (A2). An admin sees numbers and settings only | Fixed rule 1 | Yes: widening it later would break the rule | the owner |
| Assumed (A3). A view that is off is refused by the server | Hiding alone is not a limit | No | the owner |
| Assumed (A4). Named view sets, with per-person additions and removals | Set up once, adjusted per person | No | the owner |
| Assumed (A5). A few people; the simple set is chat, projects, artifacts and general settings | The owner has not said who | No | the owner |

## Open questions

| Question | Owner | Blocks start? |
|---|---|---|
| Is an admin a person with a mark, or a separate account with its own sign-in? | the owner | Yes |
| What may an admin see of another person? | the owner | Yes |
| Does switching a view off only hide it, or turn the feature off for that person? | the owner | Yes |
| Are views set person by person, or by named view sets? | the owner | Yes |
| Who will use Keystone besides the owner? | the owner | Yes |

## Log

- November 26, 2026: started. Read the tenants, sign-in, tenant settings, settings screens, operations and usage report docs before asking.
- November 27, 2026: the owner asked for a build spec before answering round 1. The spec takes the recommended answers, marked Assumed, and the work resting on them is provisional.
