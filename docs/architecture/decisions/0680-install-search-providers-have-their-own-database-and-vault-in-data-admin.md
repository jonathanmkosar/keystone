# 0680. The install's search providers have their own database and vault in data/admin/

November 27, 2026 · decided in P7-06

## The question

The spec keeps the install's search providers "in `data/admin/`, beside the admin accounts", with their keys in an install vault, `data/admin/secrets.bin`. P15-01 is building the admin accounts at the same time, in `data/admin/admin.db`, with a package of its own. Where exactly do the providers, their state and their counts go, and how is the install vault sealed?

## The decision

- **A database of their own, `data/admin/search.db`**, with the same four tables a person's own providers have in their database (`search_providers`, `search_states`, `search_counts`, `search_meta`; the tenant migration `20261128T010000_search_providers.sql`, and `installSchema` in `internal/tools/web/store.go`). The same code reads both. It is opened for each piece of work and closed again, so no file there is held open, and it is made only when the first install provider is added: an install with none has no file.
- **The install vault is a vault like a person's**, `data/admin/secrets.bin`, its data key wrapped under the same install master key (decision 0006), and both the wrap and the sealed secrets bound to the id `install` (`secrets.InstallID`, in the new `internal/secrets/install.go`). A tenant id is 16 hex digits, so no tenant can have that id: a person's `secrets.bin` put in `data/admin/` is refused, and so is the install's put in a person's folder.
- **`tenant.Install` hands out the folder and the vault** (`AdminFolder`, `InstallVault`, in the new `internal/tenant/admin_folder.go`), because only it holds the install's keeper. One vault is shared by every caller, as each tenant's handles share theirs.
- `admin.db` is not touched. The two files sit side by side.

## Why

- **Two items build in `data/admin/` at once.** A file each, and a package each, means neither waits on the other and the merge is two new files, not one schema edited twice.
- **The same tables as a person's** keep one set of functions for listing, ordering, states and counts, and let P7-08 draw both lists from one view.
- **Open and close each time** costs a few milliseconds a search, and keeps Windows able to remove a data folder a check made, and a backup able to copy the folder.
- **A tenant vault bound to another id** is the smallest change to `internal/secrets` that gives the install a vault: one constant and one function in a new file, and no existing line changed.

## What it costs

- An install provider's state and counts are a second database to back up. A person's backup (P13-04) holds only the person; the install's providers and their keys are not in it, and are set again by the admin on a new machine.
- If P15-01 or a later item would rather keep everything in `admin.db`, the four tables move there as they are, and `OpenInstall`'s `use` opens that file instead.
