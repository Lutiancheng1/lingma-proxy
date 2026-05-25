param(
    [string]$NodeVersion = "22.11.0",
    [string]$FallbackNodeVersion = "20.18.0",
    [string]$Registry = "https://registry.npmmirror.com",
    [switch]$SkipNodeInstall,
    [switch]$PersistPath,
    [string]$LogPath = ""
)

$ErrorActionPreference = "Stop"
$utf8 = New-Object System.Text.UTF8Encoding $false
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
try {
    chcp 65001 | Out-Null
} catch {
}

function New-DefaultLogPath {
    $dir = Join-Path $env:LOCALAPPDATA "LingmaProxy\logs"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    return Join-Path $dir ("feishu-cli-install-{0}.log" -f (Get-Date -Format "yyyyMMdd-HHmmss"))
}

if ([string]::IsNullOrWhiteSpace($LogPath)) {
    $LogPath = New-DefaultLogPath
}

Start-Transcript -Path $LogPath -Append | Out-Null

try {
    function Write-Step {
        param([string]$Message)
        Write-Host ("[{0}] {1}" -f (Get-Date -Format "HH:mm:ss"), $Message)
    }

    function Refresh-ProcessPath {
        $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $paths = @($machinePath, $userPath, $env:Path) -join ";"
        $env:Path = ($paths -split ";" | Where-Object { $_ -and $_.Trim() } | Select-Object -Unique) -join ";"
    }

    function Add-ToUserPath {
        param([string]$PathToAdd)
        if ([string]::IsNullOrWhiteSpace($PathToAdd)) {
            return
        }
        $current = [Environment]::GetEnvironmentVariable("Path", "User")
        $items = @($current -split ";" | Where-Object { $_ -and $_.Trim() })
        if ($items -notcontains $PathToAdd) {
            $next = (@($items) + $PathToAdd) -join ";"
            [Environment]::SetEnvironmentVariable("Path", $next, "User")
        }
        if (($env:Path -split ";") -notcontains $PathToAdd) {
            $env:Path = "$env:Path;$PathToAdd"
        }
    }

    function Get-CommandPath {
        param([string]$Name)
        $cmd = Get-Command $Name -ErrorAction SilentlyContinue
        if ($cmd) {
            return $cmd.Source
        }
        return ""
    }

    function Get-NodeVersionInfo {
        param([string]$NodePath = "")
        if ([string]::IsNullOrWhiteSpace($NodePath)) {
            $NodePath = Get-CommandPath "node.exe"
            if (-not $NodePath) {
                $NodePath = Get-CommandPath "node"
            }
        }
        if (-not $NodePath) {
            return $null
        }
        $raw = (& $NodePath --version 2>$null).Trim()
        if ($raw -notmatch "^v?(\d+)\.(\d+)\.(\d+)") {
            return $null
        }
        return [pscustomobject]@{
            Path = $NodePath
            Raw = $raw
            Major = [int]$Matches[1]
            Minor = [int]$Matches[2]
            Patch = [int]$Matches[3]
        }
    }

    function Test-CompatibleNode {
        param($Info)
        if (-not $Info) {
            return $false
        }
        if ($Info.Major -gt 20) {
            return $true
        }
        return ($Info.Major -eq 20 -and $Info.Minor -ge 12)
    }

    function Add-NodeDirToProcessPath {
        param([string]$NodeDir)
        if ([string]::IsNullOrWhiteSpace($NodeDir)) {
            return
        }
        if (($env:Path -split ";") -notcontains $NodeDir) {
            $env:Path = "$NodeDir;$env:Path"
        }
    }

    function Get-NodeCandidates {
        $paths = New-Object System.Collections.Generic.List[string]
        $seen = @{}
        function Add-CandidatePath {
            param([string]$Path)
            if ([string]::IsNullOrWhiteSpace($Path)) {
                return
            }
            try {
                $resolved = [System.IO.Path]::GetFullPath($Path)
            } catch {
                return
            }
            $key = $resolved.ToLowerInvariant()
            if (-not $seen.ContainsKey($key) -and (Test-Path $resolved)) {
                $seen[$key] = $true
                $paths.Add($resolved) | Out-Null
            }
        }

        $cmd = Get-CommandPath "node.exe"
        if ($cmd) {
            Add-CandidatePath $cmd
        }
        $cmd = Get-CommandPath "node"
        if ($cmd) {
            Add-CandidatePath $cmd
        }
        foreach ($dir in @(
            "$env:ProgramFiles\nodejs",
            "${env:ProgramFiles(x86)}\nodejs",
            "$env:LOCALAPPDATA\Programs\nodejs",
            "$env:NVM_SYMLINK"
        )) {
            if (-not [string]::IsNullOrWhiteSpace($dir)) {
                Add-CandidatePath (Join-Path $dir "node.exe")
            }
        }
        foreach ($root in @($env:NVM_HOME, "$env:APPDATA\nvm", "$env:LOCALAPPDATA\nvm", "C:\nvm4w", "D:\nvm4w")) {
            if ([string]::IsNullOrWhiteSpace($root) -or -not (Test-Path $root)) {
                continue
            }
            Get-ChildItem -Path $root -Directory -ErrorAction SilentlyContinue |
                Where-Object { $_.Name -match "^v?\d+\.\d+\.\d+$" } |
                ForEach-Object { Add-CandidatePath (Join-Path $_.FullName "node.exe") }
        }
        foreach ($root in @("C:\nodejs", "D:\nodejs", "C:\node.js", "D:\node.js")) {
            Add-CandidatePath (Join-Path $root "node.exe")
        }
        foreach ($drive in Get-PSDrive -PSProvider FileSystem -ErrorAction SilentlyContinue) {
            foreach ($dir in @(
                (Join-Path $drive.Root "nodejs"),
                (Join-Path $drive.Root "node.js"),
                (Join-Path $drive.Root "Program Files\nodejs")
            )) {
                Add-CandidatePath (Join-Path $dir "node.exe")
            }
        }
        return @($paths)
    }

    function Select-CompatibleNode {
        $candidates = @()
        foreach ($nodePath in Get-NodeCandidates) {
            $info = Get-NodeVersionInfo $nodePath
            if (-not $info) {
                continue
            }
            $dir = Split-Path -Parent $info.Path
            $npmPath = Join-Path $dir "npm.cmd"
            $npxPath = Join-Path $dir "npx.cmd"
            $candidates += [pscustomobject]@{
                Node = $info
                Dir = $dir
                NpmPath = if (Test-Path $npmPath) { $npmPath } else { "" }
                NpxPath = if (Test-Path $npxPath) { $npxPath } else { "" }
                Compatible = Test-CompatibleNode $info
            }
        }
        if ($candidates.Count -gt 0) {
            Write-Step "Detected Node.js candidates:"
            foreach ($item in ($candidates | Sort-Object @{ Expression = { $_.Node.Major }; Descending = $true }, @{ Expression = { $_.Node.Minor }; Descending = $true })) {
                Write-Step ("  {0} at {1} compatible={2}" -f $item.Node.Raw, $item.Node.Path, $item.Compatible)
            }
        }
        $selected = $candidates |
            Where-Object { $_.Compatible -and $_.NpmPath -and $_.NpxPath } |
            Sort-Object @{ Expression = { Get-NodeStabilityRank $_.Node.Major }; Descending = $false }, @{ Expression = { $_.Node.Major }; Descending = $true }, @{ Expression = { $_.Node.Minor }; Descending = $true }, @{ Expression = { $_.Node.Patch }; Descending = $true } |
            Select-Object -First 1
        if ($selected) {
            Add-NodeDirToProcessPath $selected.Dir
            return $selected.Node
        }
        return $null
    }

    function Get-NodeStabilityRank {
        param([int]$Major)
        if ($Major -eq 22) { return 0 }
        if ($Major -eq 20) { return 1 }
        if ($Major -gt 22) { return 2 }
        return 3
    }

    function Get-NVMPath {
        $paths = New-Object System.Collections.Generic.List[string]
        $roots = @($env:NVM_HOME)
        if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
            $roots += (Join-Path $env:LOCALAPPDATA "nvm")
        }
        if (-not [string]::IsNullOrWhiteSpace($env:APPDATA)) {
            $roots += (Join-Path $env:APPDATA "nvm")
        }
        foreach ($root in $roots) {
            if (-not [string]::IsNullOrWhiteSpace($root)) {
                $paths.Add((Join-Path $root "nvm.exe")) | Out-Null
            }
        }
        $paths.Add("C:\nvm4w\nvm.exe") | Out-Null
        $paths.Add("D:\nvm4w\nvm.exe") | Out-Null
        foreach ($path in $paths) {
            if (-not [string]::IsNullOrWhiteSpace($path) -and (Test-Path $path)) {
                return $path
            }
        }
        $cmd = Get-CommandPath "nvm.exe"
        if ($cmd) {
            return $cmd
        }
        $cmd = Get-CommandPath "nvm"
        if ($cmd) {
            return $cmd
        }
        return ""
    }

    function Install-NodeWithNVM {
        param([string]$NVMPath)
        if ([string]::IsNullOrWhiteSpace($NVMPath) -or -not (Test-Path $NVMPath)) {
            return $false
        }
        Write-Step "Detected nvm-windows at $NVMPath"
        Write-Step "Trying nvm install $NodeVersion 64"
        & $NVMPath install $NodeVersion 64
        if ($LASTEXITCODE -ne 0) {
            Write-Step "nvm install $NodeVersion failed; trying fixed Node.js $FallbackNodeVersion."
            & $NVMPath install $FallbackNodeVersion 64
            if ($LASTEXITCODE -ne 0) {
                return $false
            }
        }
        $selectedBeforeUse = Select-CompatibleNode
        $useVersion = $NodeVersion
        if ($selectedBeforeUse) {
            $useVersion = $selectedBeforeUse.Raw.TrimStart("v")
        }
        Write-Step "Trying nvm use $useVersion 64"
        & $NVMPath use $useVersion 64
        if ($LASTEXITCODE -ne 0) {
            Write-Step "nvm use $useVersion failed; trying nvm use $FallbackNodeVersion 64."
            & $NVMPath use $FallbackNodeVersion 64
        }
        Refresh-ProcessPath
        $selected = Select-CompatibleNode
        return [bool]$selected
    }

    function Install-NodeLTS {
        if ($SkipNodeInstall) {
            throw "Node.js is missing or older than 20.12, and -SkipNodeInstall was specified."
        }

        Write-Step "Installing compatible Node.js. Existing non-nvm Node.js settings will not be modified intentionally."
        $nvm = Get-NVMPath
        if ($nvm) {
            if (Install-NodeWithNVM $nvm) {
                return
            }
            Write-Step "nvm-windows did not provide a compatible node; falling back to winget/MSI."
        }

        $winget = Get-CommandPath "winget.exe"
        if ($winget) {
            Write-Step "Trying winget install OpenJS.NodeJS.LTS"
            & $winget install OpenJS.NodeJS.LTS -e --silent --accept-package-agreements --accept-source-agreements
            Refresh-ProcessPath
            $info = Get-NodeVersionInfo
            if (Test-CompatibleNode $info) {
                return
            }
            Write-Step "winget did not provide a compatible node in current process; falling back to MSI."
        }

        $arch = "x64"
        if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -eq "Arm64") {
            $arch = "arm64"
        }
        $url = "https://nodejs.org/dist/v$NodeVersion/node-v$NodeVersion-$arch.msi"
        $msi = Join-Path $env:TEMP ("node-v{0}-{1}.msi" -f $NodeVersion, $arch)
        Write-Step "Downloading $url"
        Invoke-WebRequest -Uri $url -OutFile $msi
        Write-Step "Installing $msi"
        $proc = Start-Process msiexec.exe -ArgumentList "/i `"$msi`" /qn /norestart" -Wait -PassThru
        if ($proc.ExitCode -ne 0) {
            throw "Node.js MSI install failed with exit code $($proc.ExitCode)."
        }
        Refresh-ProcessPath
    }

    function Ensure-NpmPrefix {
        $npm = Get-CommandPath "npm.cmd"
        if (-not $npm) {
            $npm = Get-CommandPath "npm"
        }
        if (-not $npm) {
            throw "npm was not found after Node.js detection."
        }
        $prefix = (& $npm config get prefix 2>$null | Select-Object -First 1).Trim()
        if ([string]::IsNullOrWhiteSpace($prefix) -or $prefix -eq "undefined") {
            $prefix = Join-Path $env:APPDATA "npm"
            & $npm config set prefix $prefix
        }
        New-Item -ItemType Directory -Force -Path $prefix | Out-Null
        $cache = (& $npm config get cache 2>$null | Select-Object -First 1).Trim()
        if (-not [string]::IsNullOrWhiteSpace($cache) -and $cache -ne "undefined") {
            New-Item -ItemType Directory -Force -Path $cache | Out-Null
        }
        $env:Path = "$prefix;$env:Path"
        if ($PersistPath) {
            Add-ToUserPath $prefix
        }
        return $prefix
    }

    function Invoke-Checked {
        param(
            [string]$Name,
            [string]$File,
            [string[]]$Arguments
        )
        Write-Step "$Name`: $File $($Arguments -join ' ')"
        & $File @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Name failed with exit code $LASTEXITCODE."
        }
    }

    function Test-LarkCLI {
        $path = Get-CommandPath "lark-cli.cmd"
        if (-not $path) {
            $path = Get-CommandPath "lark-cli"
        }
        if (-not $path) {
            return $false
        }
        & $path --version | Out-Host
        return ($LASTEXITCODE -eq 0)
    }

    function Install-LarkCLI {
        $npm = Get-CommandPath "npm.cmd"
        if (-not $npm) {
            $npm = Get-CommandPath "npm"
        }
        if (-not [string]::IsNullOrWhiteSpace($Registry)) {
            Invoke-Checked "Configure npm registry" $npm @("config", "set", "registry", $Registry)
        }
        try {
            Invoke-Checked "Install lark-cli" $npm @("install", "-g", "@larksuite/cli")
        } catch {
            Write-Step "npm install reported an error; checking whether lark-cli is actually usable."
            if (-not (Test-LarkCLI)) {
                throw
            }
        }
    }

    function Install-LarkSkills {
        $npx = Get-CommandPath "npx.cmd"
        if (-not $npx) {
            $npx = Get-CommandPath "npx"
        }
        if (-not $npx) {
            throw "npx was not found."
        }
        $commands = @(
            @("-y", "skills@1.5.6", "add", "https://open.feishu.cn", "--skill", "-y", "-g"),
            @("-y", "skills@1.5.5", "add", "https://open.feishu.cn", "--skill", "-y", "-g"),
            @("-y", "skills", "add", "https://open.feishu.cn", "--skill", "-y", "-g"),
            @("-y", "skills", "add", "larksuite/cli", "-y", "-g")
        )
        $lastError = $null
        foreach ($args in $commands) {
            try {
                Invoke-Checked "Install lark-cli skills" $npx $args
                return
            } catch {
                $lastError = $_
                Write-Step "skills install attempt failed: $($_.Exception.Message)"
            }
        }
        throw $lastError
    }

    function Test-RequiredSkills {
        $npx = Get-CommandPath "npx.cmd"
        if (-not $npx) {
            $npx = Get-CommandPath "npx"
        }
        if (-not $npx) {
            throw "npx was not found."
        }
        $required = @(
            "lark-shared",
            "lark-calendar",
            "lark-im",
            "lark-doc",
            "lark-base",
            "lark-sheets",
            "lark-task",
            "lark-wiki"
        )
        $commands = @(
            @("-y", "skills@1.5.6", "ls", "-g", "--json"),
            @("-y", "skills@1.5.5", "ls", "-g", "--json"),
            @("-y", "skills", "ls", "-g", "--json")
        )
        $raw = ""
        $lastError = $null
        foreach ($args in $commands) {
            try {
                Write-Step "Check lark-cli skills: $npx $($args -join ' ')"
                $raw = (& $npx @args 2>$null | Out-String).Trim()
                if (-not [string]::IsNullOrWhiteSpace($raw)) {
                    break
                }
            } catch {
                $lastError = $_
            }
        }
        if ([string]::IsNullOrWhiteSpace($raw)) {
            if ($lastError) {
                throw $lastError
            }
            throw "skills ls returned empty output."
        }
        $items = $raw | ConvertFrom-Json
        $names = @($items | ForEach-Object { $_.name })
        $missing = @($required | Where-Object { $names -notcontains $_ })
        if ($missing.Count -gt 0) {
            throw "Required skills are missing: $($missing -join ', ')"
        }
    }

    Refresh-ProcessPath
    $node = Select-CompatibleNode
    if (-not $node) {
        $node = Get-NodeVersionInfo
    }
    if (-not (Test-CompatibleNode $node)) {
        if ($node) {
            Write-Step "Current Node.js $($node.Raw) is older than required 20.12."
        } else {
            Write-Step "Node.js was not found."
        }
        Install-NodeLTS
        $node = Select-CompatibleNode
        if (-not $node) {
            $node = Get-NodeVersionInfo
        }
    }
    if (-not (Test-CompatibleNode $node)) {
        throw "Node.js 20.12+ is required, but compatible Node.js was not found."
    }

    Write-Step "Using Node.js $($node.Raw) at $($node.Path)"
    $prefix = Ensure-NpmPrefix
    Write-Step "Using npm global prefix $prefix"
    Install-LarkCLI
    if (-not (Test-LarkCLI)) {
        throw "lark-cli is still not executable after installation."
    }
    Install-LarkSkills
    Test-RequiredSkills

    $summary = [pscustomobject]@{
        ok = $true
        node = $node.Raw
        nodePath = $node.Path
        npmPath = Get-CommandPath "npm.cmd"
        npxPath = Get-CommandPath "npx.cmd"
        larkCLIPath = Get-CommandPath "lark-cli.cmd"
        npmPrefix = $prefix
        logPath = $LogPath
    }
    $summary | ConvertTo-Json -Depth 4
    exit 0
} catch {
    Write-Error $_
    exit 1
} finally {
    Stop-Transcript | Out-Null
}
