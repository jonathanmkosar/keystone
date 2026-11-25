# 0260. A backup is one file sealed under a passphrase, with PBKDF2 from the standard library

November 25, 2026 · decided in P13-04

## The question

The plan said a backup of one tenant is its folder. That folder holds the person's chats and files in the clear, and its secrets under a master key the operating system keeps (decision 0006), so a copied folder is readable by anyone who has it and its keys are useless anywhere else. The item asks for an encrypted backup of one tenant that can be restored on another machine. What is the file, how is its key made, and what checks it?

## The decision

**One file per tenant, sealed under a passphrase the person gives.** The passphrase is read from standard input, never from an argument, and is stored nowhere. The command asks for it twice when it is typed.

**The key comes from PBKDF2-SHA-256, in Go's standard library**, with a salt of 32 random bytes that is new for each file. The rounds are the setting `backup.passphrase_rounds`, 2,000,000 as shipped, and the file records its own count, so changing the setting never strands an old backup. A file may name 100,000 to 50,000,000 rounds and no other count. HKDF-SHA-256, also in the standard library, splits the result into three keys: one seals the contents, one wraps the tenant's data key (decision 0261), and one is a check value in the header that tells a wrong passphrase from a damaged file.

**The contents are sealed in 64 KiB chunks with AES-256-GCM**, as the age format and Tink's streaming encryption do. Each chunk's nonce is its number and a flag that marks the last chunk, and every chunk is bound to the header. So a changed byte, a dropped, repeated or moved chunk, a file cut short at any point (even exactly at a chunk's edge), anything added after the end, and a changed header are each refused. The file is written and read as a stream, so a large tenant never has to fit in memory.

**Inside the chunks is a gzip-compressed tar**, both from the standard library: a manifest, then the tenant's folder under `tenant/`. Names are checked when they are written and again when they are read: plain relative paths only, regular files and folders only, no name twice even in another case.

**The format is versioned in its first four bytes** (`KSB1`), and the key derivation by a byte of its own, so Argon2id or another format can be added later beside this one.

## Why

- Argon2id and scrypt are stronger against guessing on graphics cards, and both are in `golang.org/x/crypto`, which would be a second outside module. The brief allows it only with a reason. PBKDF2 at two million rounds, with a passphrase of at least twelve characters (`backup.passphrase_min_length`), is the same choice passwords made in decision 0007, and a backup is made rarely enough that a quarter of a second of rounds costs nothing. The byte for the derivation leaves the door open.
- A check value gives the person a plain "wrong passphrase" instead of "damaged". It lets a guesser test a passphrase no faster than opening the first chunk would.
- Sealing chunk by chunk, with the last one marked, is the known way to authenticate a stream that may be cut. One seal over the whole file would mean holding it in memory, or writing what it holds before it is proven.
- Tar and gzip are in the standard library and carry folders, sizes and long names. The database is mostly text and compresses well: an empty tenant's 344 KB database becomes a 10 KB backup.

## What it costs

- A backup holds the tenant's keys and chats under the passphrase alone. Whoever has the file and the passphrase has everything in it, on any machine. That is the point of a backup, and it is weaker than the operating system's key store. The runbook says to keep the passphrase in a password manager and the file somewhere the person trusts.
- A lost passphrase is a lost backup. Nothing can open the file without it.
- Two readings of a passphrase that look the same but are written differently in Unicode (an accent typed as one character or two) are different passphrases. Normalising them would need `golang.org/x/text`. The command asks twice when typed, which catches a slip, not this.
- Compressing before sealing gives away a little about the contents through the file's size. A backup sits at rest and an attacker cannot add text of their own to it, which is what makes that matter elsewhere.
