# 0662. Every part of a sealed file's name on disk is a keyed hash

November 27, 2026 · decided in P15-06

## The question

The spec asks that a file kept by its hash be named by a keyed hash, so that equal names do not show equal files across people. Uploads and project files are named by their SHA-256; artifact versions by the chat's id, the artifact's id and the version. Which names change, and how?

## The decision

Every part of every path under a sealed person's `files` and `artifacts` folders is `Keys.Name(part)`: the first 16 bytes of HMAC-SHA-256 under the person's names key, as 32 hex characters. `tenant.Folder` maps each part of a slash-separated path on the way to disk, so `chat/artifact/v2` is three keyed names in three folders, and `RemoveAll(chat)` still removes a chat's artifacts. The database keeps the plain ids, sealed with the rest of it.

## Why

- One rule for every name is simpler than one for hashes and another for ids, and a chat's or an artifact's id on disk would say how many chats a person has with artifacts, and when they were made (ids carry no time, but the folders' times do).
- The names key is the person's, so the same upload has a different name for every person, and nobody without the key can test whether a person holds a known file.
- 128 bits keep names short and are far beyond a collision among one person's files.

## What it costs

- A person's folder can no longer be read by hand. Nothing in Keystone lists these folders by name; the commands and the API go through the database.
- The mapping is one HMAC per part of a name, at each read and write: nothing beside a disk's own cost.
