param(
  [string]$ServiceName = "LingmaProxy",
  [string]$BinaryPath = "",
  [string]$Arguments = "--host 127.0.0.1 --port 8095 --session-mode auto",
  [string]$AuthKeysFile = "",
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

# Optional inbound API-key auth. Empty = disabled (open, relies on the 127.0.0.1
# bind). When set, the proxy fails closed at startup if the file has no keys.
if (-not [string]::IsNullOrWhiteSpace($AuthKeysFile)) {
  if (!(Test-Path $AuthKeysFile)) {
    Write-Warning "Auth keys file not found: $AuthKeysFile (the service will refuse to start until it exists with >=1 key)"
  }
  $Arguments = "$Arguments --auth-keys-file `"$AuthKeysFile`""
}

Write-Host "Installing NSSM service: $ServiceName"
& $NssmPath install $ServiceName $BinaryPath $Arguments
& $NssmPath set $ServiceName AppDirectory $WorkingDirectory
& $NssmPath set $ServiceName Description $Description
& $NssmPath set $ServiceName Start SERVICE_AUTO_START

Write-Host "Service installed. Start with:"
Write-Host "  $NssmPath start $ServiceName"
Write-Host ""
Write-Host "To require an inbound API key (e.g. when exposing via a tunnel), reinstall with:"
Write-Host "  .\install-nssm-service.ps1 -AuthKeysFile C:\path\to\auth-keys.txt"
Write-Host "  (one key per line, '#' comments; edit the file then restart the service to rotate)"
