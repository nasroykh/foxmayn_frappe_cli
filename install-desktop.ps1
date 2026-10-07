#Requires -Version 5.1
# Installs Foxmayn Frappe Desktop on Windows (PowerShell or cmd.exe):
#
#   powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install-desktop.ps1 | iex"
#
# It downloads the newest desktop release ($env:FFD_VERSION = "0.1.2" picks
# one), checks the installer's SHA-256 against checksums.txt, runs the
# per-user installer silently (no administrator rights) and starts the app.
# $env:FFD_NO_OPEN = "1" skips starting it.
#
# The installer is not code-signed yet. A browser download is marked as coming
# from the internet, and SmartScreen then warns; a download by PowerShell is
# not, so no warning shows. That is why this script exists: it checks the
# download itself instead.
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

# Force TLS 1.2: Windows PowerShell 5.1 may otherwise default to TLS 1.0.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo    = "nasroykh/foxmayn_frappe_cli"
$Product = "Foxmayn Frappe Desktop"
$Process = "foxmayn-frappe-desktop"

# --- resolve the version ---
# Desktop releases are never GitHub's "latest" (that is the CLI), so read the
# release list and take the newest desktop-v<X.Y.Z> (no -rc suffix).
$Version = "$env:FFD_VERSION" -replace '^desktop-v', '' -replace '^v', ''
if (-not $Version) {
    try {
        $Releases = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases?per_page=100"
    } catch {
        $Releases = @()
    }
    $Tag = $Releases |
        Where-Object { -not $_.draft -and $_.tag_name -match '^desktop-v\d+\.\d+\.\d+$' } |
        Select-Object -First 1 -ExpandProperty tag_name
    if ($Tag) { $Version = $Tag -replace '^desktop-v', '' }
}
if ($Version -notmatch '^\d') {
    throw "Could not find the latest desktop release (GitHub may be limiting requests). Pick one from https://github.com/$Repo/releases and set `$env:FFD_VERSION before running this again."
}

# Only an amd64 build exists; Windows on Arm runs it under emulation.
$Setup = "foxmayn-frappe-desktop-$Version-windows-amd64-setup.exe"
$Base  = "https://github.com/$Repo/releases/download/desktop-v$Version"
Write-Host "Installing $Product $Version..."

$TmpDir = Join-Path $env:TEMP "ffd_install_$([System.IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Path $TmpDir | Out-Null

try {
    $SetupPath    = Join-Path $TmpDir $Setup
    $ChecksumPath = Join-Path $TmpDir "checksums.txt"

    # --- download ---
    Invoke-WebRequest -Uri "$Base/$Setup"        -OutFile $SetupPath    -UseBasicParsing
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile $ChecksumPath -UseBasicParsing

    # --- verify the SHA-256 (fail closed) ---
    # Windows has no built-in Ed25519 verifier, so checksums.txt.sig is not
    # checked here (install-desktop.sh checks it). For provenance, use
    # gh attestation verify.
    $Line = Get-Content $ChecksumPath |
        Where-Object { ($_ -split '\s+', 2)[1] -eq $Setup } |
        Select-Object -First 1
    if (-not $Line) {
        throw "$Setup is not listed in checksums.txt; aborting."
    }
    $Expected = (($Line -split '\s+') | Select-Object -First 1).ToLower()
    $Actual   = (Get-FileHash -Algorithm SHA256 -Path $SetupPath).Hash.ToLower()
    if ($Actual -ne $Expected) {
        throw "Checksum mismatch!`n  Expected: $Expected`n  Got:      $Actual"
    }

    # --- close the installed copy if it runs: the installer cannot replace a
    # running exe. Other copies (a development build) are left alone. ---
    $Exe = Join-Path $env:LOCALAPPDATA "Programs\$Product\$Process.exe"
    function Get-Installed {
        @(Get-Process -Name $Process -ErrorAction SilentlyContinue |
            Where-Object { $_.Path -and ($_.Path -ieq $Exe) })
    }
    $Running = Get-Installed
    if ($Running.Count -gt 0) {
        Write-Host "Closing the running app..."
        $Running | ForEach-Object { [void]$_.CloseMainWindow() }
        $Deadline = (Get-Date).AddSeconds(10)
        while ((Get-Installed).Count -gt 0 -and (Get-Date) -lt $Deadline) {
            Start-Sleep -Milliseconds 500
        }
        if ((Get-Installed).Count -gt 0) {
            throw "$Product is still running. Close it and run this again."
        }
    }

    # --- install (per user, silent) ---
    $Proc = Start-Process -FilePath $SetupPath -ArgumentList "/S" -Wait -PassThru
    if ($Proc.ExitCode -ne 0) {
        throw "The installer failed (exit code $($Proc.ExitCode))."
    }

    if (-not (Test-Path $Exe)) {
        throw "The installer finished, but $Exe was not found."
    }

    Write-Host ""
    Write-Host "Installed to $Exe"
    Write-Host "Start menu and desktop shortcuts are named '$Product'."
    Write-Host "Optional provenance check (needs gh): gh release download desktop-v$Version -R $Repo -p '$Setup'; gh attestation verify '$Setup' --repo $Repo"

    if ($env:FFD_NO_OPEN -ne "1") {
        Start-Process -FilePath $Exe
    }
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
