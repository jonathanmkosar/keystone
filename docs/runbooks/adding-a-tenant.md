# Adding a tenant

A tenant is one person. Adding one creates their folder, their database and their entry in `keystone.json`.

1. Stop Keystone if it is running. The registry is read when the server starts.
2. From the repo folder, run:

       go run ./tools/tenant add "Their Name"

   It prints the new tenant's id, sixteen hex characters.
3. Check it:

       go run ./tools/tenant list

4. Start Keystone again.

To use a data folder other than the `data.dir` setting, add `--data <folder>` before `add` or `list`.

The name is a label and can be 1 to 100 characters. The id never changes and is the name of the tenant's folder under `tenants/`. Sign-in for the new tenant is set up in P2-02, and there is no command to remove a tenant yet.
