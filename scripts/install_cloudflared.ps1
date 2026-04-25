#Requires -RunAsAdministrator
<#
  Installs cloudflared as a Windows service for the Keystone runtime.

  Prerequisites (do these in the Cloudflare dashboard first):
    1. Add your domain (<your-domain>) to your Cloudflare account.
    2. Zero Trust > Access > Tunnels > Create a tunnel. Copy the install token.
    3. Zero Trust > Access > Applications > Add an application:
         - Self-hosted, hostname: finance.<your-domain>
         - Identity provider: One-time PIN (or Google/GitHub)
         - Policy: include emails: <your email>
         - Note the Application Audience (AUD) tag.
       Repeat for fantasy.<your-domain>.
    4. Tunnels > your tunnel > Public hostnames:
         finance.<your-domain> -> http://localhost:8787
         fantasy.<your-domain> -> http://localhost:8787

  Usage:
    .\scripts\install_cloudflared.ps1 -Token "eyJhIjoi..."
#>

param(
  [Parameter(Mandatory=$true)] [string]$Token,
  [string]$DownloadUrl = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe",
  [string]$InstallDir = "$env:ProgramFiles\cloudflared"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $InstallDir)) {
  New-Item -ItemType Directory -Path $InstallDir | Out-Null
}

$exePath = Join-Path $InstallDir "cloudflared.exe"
if (-not (Test-Path $exePath)) {
  Write-Host "Downloading cloudflared..."
  Invoke-WebRequest -Uri $DownloadUrl -OutFile $exePath
}

# Stop and remove any existing service so we can re-install with this token.
$existing = Get-Service -Name cloudflared -ErrorAction SilentlyContinue
if ($existing) {
  Write-Host "Stopping existing cloudflared service..."
  Stop-Service cloudflared -ErrorAction SilentlyContinue
  & $exePath service uninstall | Out-Null
}

Write-Host "Installing cloudflared service with provided tunnel token..."
& $exePath service install $Token

Write-Host "Starting cloudflared service..."
Start-Service cloudflared

Write-Host "Done. Verify with: Get-Service cloudflared"
Write-Host "Tunnel routes are managed in Cloudflare Zero Trust > Tunnels."
