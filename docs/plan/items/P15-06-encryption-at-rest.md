# P15-06. Encryption at rest

Status: todo
Lane: core
Depends on: P2-01, P2-03
Owner gate: None.
Built without the gate: Everything.
Scope: Each person's database (with its write-ahead log and temporary files), uploads, files, artifacts and project files encrypted with the person's data key under the install master key. Choose how the database is encrypted and record it in a decision record. Files sealed in chunks with AES-256-GCM, bound to the tenant id, named by a keyed hash. Encrypt an existing install in place at first start, one person at a time after a backup. Touches the chats lane's uploads and the agents lane's artifacts: say so in the journal.
Proof: Every file in a person's folder, read from disk, holds no marker word and no plain database header. A folder copied to another tenant, or opened without the master key, does not open. An interrupted first start resumes and never opens a half-done person. The load test and `verify` pass with encryption on.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "Encryption at rest", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
