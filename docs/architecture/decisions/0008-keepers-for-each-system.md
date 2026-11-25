# 0008. Each operating system's key store is reached without an outside module, and a key file is used only when asked for

November 24, 2026 · decided in P2-03

## The question

Decision 0006 says the install master key is held by the operating system. Three things were left open. How does Keystone reach the key store on macOS and Linux? What happens on a machine that has none? And how does an install remember which one it used?

## The decision

**Windows uses DPAPI, through `golang.org/x/sys/windows`.** The data key is handed to the system to wrap, and Keystone stores no master key of its own.

**macOS and Linux use the system's own command.** On macOS the master key is a generic password in the login keychain, reached through `/usr/bin/security`. On Linux it is an item in the Secret Service, reached through `secret-tool`. Keystone makes 32 random bytes the first time it wraps, stores them there, and wraps with AES-256-GCM.

**A machine with no key store stores no secrets, unless the install is told to use a key file.** The `secrets.keeper` setting is `system` by default. Set to `file`, the master key is a file in the user's profile folder, outside the data folder.

**`keystone.json` records which keeper wrapped the first data key.** It records the name and never a key. From then on that keeper is the only one accepted, and the setting is no longer read.

**The tenant package is told the setting and does not read it.** The command that opens the install reads `secrets.keeper` and passes it in.

## Why

- A keychain module for macOS needs a C compiler, which breaks the one-binary build of decision 0002. A Secret Service module brings a D-Bus client with it. The two commands are already on the machines that have a key store.
- Falling back to a key file without being asked would leave an install weaker than its owner believes. Refusing, with a message that names the runbook, makes it a choice.
- Without the record, changing the setting later would make every stored secret unreadable, with an error that does not say why.
- `internal/tenant` enforces tenant isolation, a fixed rule, and the fixed rules check refuses any guard that can read a setting.

## What it costs

- The macOS and Linux keepers pass a key to another program on its standard input. It is never an argument, but it is one more place the key passes through.
- A Linux server with no desktop has no Secret Service, so it needs the key file.
- The key file protects against a copied data folder and nothing more. Whoever can read the user's profile can read it.
- The macOS and Linux keepers have not been run on those systems yet. They are provisional until they are.
