<#
  Registers Keystone scheduled jobs in Windows Task Scheduler.

  By default the core ships with no scheduled tasks. Individual stones that
  need a scheduler (e.g. a nightly sync) add themselves to the TASKS list in
  `src/keystone/schedulers/windows_tasks.py`, and this script installs every
  task declared there.

  Run as the user who owns the keychain entries (so the jobs can read
  any credentials the stones need).
#>

param(
  [string]$RepoRoot = (Resolve-Path "$PSScriptRoot\..").Path
)

$ErrorActionPreference = "Stop"

Write-Host "Installing Keystone tasks (repo root: $RepoRoot)"
& uv --directory $RepoRoot run python -m keystone.schedulers.windows_tasks install --repo-root $RepoRoot

Write-Host ""
Write-Host "Installed. Inspect with: schtasks /Query /TN 'Keystone\<task_name>' /V /FO LIST"
Write-Host "Run on demand with:      schtasks /Run /TN 'Keystone\<task_name>'"
