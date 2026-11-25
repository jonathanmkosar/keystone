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

The name is a label and can be 1 to 100 characters. The id never changes and is the name of the tenant's folder under `tenants/`. There is no command to remove a tenant yet.

## Giving them a password

A new tenant has no password and cannot sign in until it has one. This is also how to replace a password that was forgotten or may have leaked.

1. Run, with the id from `list`:

       go run ./tools/tenant password <tenant-id>

2. Type the password and press Enter. It shows on the screen as you type, so do it where nobody is watching, and clear the screen after.
3. It prints `Password set`. Every session that tenant had is ended, and they sign in again with the new password.

A password has at least 12 characters (the `signin.password_min_length` setting). The password is never given as an argument, because arguments stay in the shell's history. To pipe it in from a password manager, send one line to standard input. In Windows PowerShell 5 as it comes, a piped password must be plain ASCII letters, digits and signs: any other character arrives as a question mark, and the password that is set is not the one that was sent.
