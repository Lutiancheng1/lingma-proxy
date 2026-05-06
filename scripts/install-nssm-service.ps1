param(
  [string]$ServiceName = "LingmaProxy",
  [string]$BinaryPath = "",
  [string]$Arguments = "--host 127.0.0.1 --port 8095 --session-mode auto",
  [string]$WorkingDirectory = "",
  [string]$NssmPath = "nssm.exe",
  [string]$Description = "Lingma Proxy service"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot

if ([string]::IsNullOrWhiteSpace($BinaryPath)) {
  $BinaryPath = Join-Path $repoRoot "dist\lingma-proxy.exe"
}
if ([string]::IsNullOrWhiteSpace($WorkingDirectory)) {
  $WorkingDirectory = $repoRoot
}

if (!(Test-Path $BinaryPath)) {
  throw "Binary not found: $BinaryPath"
}

Write-Host "Installing NSSM service: $ServiceName"
& $NssmPath install $ServiceName $BinaryPath $Arguments
& $NssmPath set $ServiceName AppDirectory $WorkingDirectory
& $NssmPath set $ServiceName Description $Description
& $NssmPath set $ServiceName Start SERVICE_AUTO_START

Write-Host "Service installed. Start with:"
Write-Host "  $NssmPath start $ServiceName"
