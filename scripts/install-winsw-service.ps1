param(
  [string]$ServiceName = "LingmaProxy",
  [string]$BinaryPath = "",
  [string]$Arguments = "--host 127.0.0.1 --port 8095 --session-mode auto",
  [string]$AuthKeysFile = "",
  [string]$WorkingDirectory = "",
  [string]$WinSWExePath = "",
  [string]$TemplatePath = ""
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot

if ([string]::IsNullOrWhiteSpace($BinaryPath)) {
  $BinaryPath = Join-Path $repoRoot "dist\lingma-proxy.exe"
}
if ([string]::IsNullOrWhiteSpace($WorkingDirectory)) {
  $WorkingDirectory = $repoRoot
}
if ([string]::IsNullOrWhiteSpace($WinSWExePath)) {
  $WinSWExePath = Join-Path $repoRoot "dist\WinSW-x64.exe"
}
if ([string]::IsNullOrWhiteSpace($TemplatePath)) {
  $TemplatePath = Join-Path $PSScriptRoot "lingma-proxy.xml.template"
}

if (!(Test-Path $BinaryPath)) {
  throw "Binary not found: $BinaryPath"
}
if (!(Test-Path $WinSWExePath)) {
  throw "WinSW executable not found: $WinSWExePath"
}
if (!(Test-Path $TemplatePath)) {
  throw "WinSW template not found: $TemplatePath"
}

# Optional inbound API-key auth. Empty = disabled (open, relies on the 127.0.0.1
# bind). When set, the proxy fails closed at startup if the file has no keys.
if (-not [string]::IsNullOrWhiteSpace($AuthKeysFile)) {
  if (!(Test-Path $AuthKeysFile)) {
    Write-Warning "Auth keys file not found: $AuthKeysFile (the service will refuse to start until it exists with >=1 key)"
  }
  $Arguments = "$Arguments --auth-keys-file `"$AuthKeysFile`""
}

$serviceExePath = Join-Path $repoRoot "$ServiceName.exe"
$serviceXmlPath = Join-Path $repoRoot "$ServiceName.xml"

$xml = Get-Content -Raw $TemplatePath
$xml = $xml.Replace("__SERVICE_ID__", $ServiceName)
$xml = $xml.Replace("__SERVICE_NAME__", $ServiceName)
$xml = $xml.Replace("__SERVICE_DESCRIPTION__", "Lingma Proxy service")
$xml = $xml.Replace("__EXECUTABLE__", $BinaryPath)
$xml = $xml.Replace("__ARGUMENTS__", $Arguments)
$xml = $xml.Replace("__WORKDIR__", $WorkingDirectory)
$xml = $xml.Replace("__LOGDIR__", (Join-Path $repoRoot "logs"))

Copy-Item -Force $WinSWExePath $serviceExePath
Set-Content -Path $serviceXmlPath -Value $xml

Write-Host "Prepared WinSW service wrapper:"
Write-Host "  $serviceExePath"
Write-Host "  $serviceXmlPath"
Write-Host ""
Write-Host "Install with:"
Write-Host "  & `"$serviceExePath`" install"
Write-Host "Start with:"
Write-Host "  & `"$serviceExePath`" start"
Write-Host ""
Write-Host "To require an inbound API key (e.g. when exposing via a tunnel), re-run with:"
Write-Host "  .\install-winsw-service.ps1 -AuthKeysFile C:\path\to\auth-keys.txt"
Write-Host "  (one key per line, '#' comments; edit the file then restart the service to rotate)"
