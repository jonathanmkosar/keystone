# 0080. The test tenant is marked in its own database, and its password is published

November 24, 2026 · decided in P3-09

## The question

`rebuild` loads a test tenant into the same data folder that holds real people. Three things follow.

1. The code must be able to say which tenant is the test tenant: the loader, to find it on a second run, and the API, to give it the fake provider and never a real model.
2. A fresh checkout must be able to sign in, so the password has to be known, and the repo is public.
3. The chats are fixtures, and so are the recorded turns. The same reply written in both would drift apart.

## The decision

**The mark is a row in the tenant's own database**: `test_tenant` = `true` in `tenant_meta`. The loader writes it into a tenant it has just made, and nothing else writes it. `testtenant.IsTest` reads it and `testtenant.Find` looks for it.

Three other ways were weighed.

- A fixed name. The name is a label a person types. `go run ./tools/tenant add "Test Person One"` is the example in the tenant command's own comment, and a tenant made that way is a person's. A loader that trusted the name would set a published password on it.
- A fixed id. Ids are random, and `internal/tenant` makes them. A fixed one would need a second way to create a tenant, in the package that guards isolation.
- A field in `keystone.json`. The registry belongs to `internal/tenant`, which keeps fields it does not know but does not hand them out. The mark would also stay behind when a tenant's folder is backed up or moved, and the database would arrive somewhere else as a person's.

The mark in the database goes where the data goes and needs no change to the tenant package.

**The password is in the fixtures, in the clear**, in `fixtures/test-tenant/tenant.json`. It is invented and says in its own words that it is a test value. It goes through the same hash and the same sign-in as any password, so the test tenant proves the real path.

**A chat fixture holds the person's messages only.** The loader plays each through the recorded turns and stores the reply. There is one copy of every reply, and a chat in the test tenant is always what the fake provider would say now.

**A second load changes as little as it can.** It tries the fixture's password before setting it, so a load that changes nothing signs nobody out. It keeps a chat whose fixture has not changed, and never touches a chat the loader did not make.

## What it costs

- Anyone who can reach the sign-in page can sign in as the test tenant. What that gives them is the invented chats and a scripted model: the store handle reaches one tenant, the test tenant holds no key, and the API gives it the fake provider only. It is still a signed-in session on the install. So `test_tenant.load` switches the load off, and an install that is reached from outside the home (P10-05) should have it off and should not hold a test tenant.
- "The test tenant gets the fake provider only" is a rule the API and the turn runner must keep, by asking `IsTest`. Nothing enforces it until P3-02 and P4-03 are built. Their items should add a use case or a check for it.
- `Find` opens every tenant's database to read the mark. That is fine once at start-up on a home install, and wrong on every request.
- If the loader stops between making the tenant and marking it, an unmarked tenant with the test tenant's name is left, and the next load makes another. The window is one statement wide. The leftover is harmless and has no password.
- The loader writes rows of its own into `tenant_meta`, a table the tenant package made. The keys start with `test_tenant`, and no other part may use that start.
- A loaded chat the person deleted comes back at the next `rebuild`, and a loaded chat is replaced, with anything the person added to it, when its fixture or any recorded turn changes.
- The rule that every fixture is marked as test data is checked for JSON files. An image or another file that cannot carry the mark needs its own answer when the first one is added.
