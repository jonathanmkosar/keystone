# 0701. The disk leaves a person's Operations and is shown only in the admin account

November 28, 2026 · decided in P15-02 · changes decision [0460](0460-the-disk-is-the-volume-and-each-person-sees-their-own-folder.md)

## The question

Decision 0460 showed every person the used space of the volume that holds the data folder, as an install fact, beside their own database, files and artifacts. The owner answered on November 27, 2026: a person's own jobs, spend and storage stay in that person's Settings, and anything install-wide is in the admin account only. Now that there is an admin account, where does the disk go?

## The decision

- A person's Operations keeps their own: jobs, schedules, spend against the cap and the memory reserve, their database, files and artifacts, their runs' budgets and model health. The `disk` limit is no longer in their report, their watch route or the home screen's notice, and the sampler no longer records it in their database.
- The admin account's Install (`GET /api/v1/admin/install`) shows the disk: the volume of the data folder, used of its size, with its watch level, read live through `figures.ReadDisk`, as 0460 read it.
- The install disk's watch level is `operations.watch_disk_percent` as the registry ships it, until install settings exist (P15-03); its scope stays as it was, so no person's change to it does anything now.
- The data folder's own total is still never shown, to anyone.

## Why

- It is the owner's answer: install-wide facts belong to the admin.
- A full disk is still the admin's to act on: a person could only be told, and could do nothing about anything but their own folder.
- 0460's reasons for reading the volume rather than the folder still hold, so the admin reads it the same way.

## What it costs

- A person no longer learns, on their own page, that the disk is nearly full. The home screen's notice says nothing about it; the admin must look.
- `operations.watch_disk_percent` is a person's setting that no longer changes anything for them. Recommended: P15-03 makes it an install setting.
- The disk has no trend in the admin account yet: its snapshots were in each person's database, and are no longer taken.
