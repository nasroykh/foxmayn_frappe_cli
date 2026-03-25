#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$Repo   = "nasroykh/foxmayn_frappe_cli"
$Binary = "ffc.exe"

# --- detect arch ---
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default {
        Write-Error "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE"
        exit 1
    }
}

# --- resolve latest release tag ---
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Version = $Release.tag_name          # e.g. "v1.2.1"
$VersionNum = $Version.TrimStart("v") # e.g. "1.2.1"

Write-Host "Installing ffc $Version (windows/$Arch)..."

$Archive      = "ffc_${VersionNum}_windows_${Arch}.zip"
$DownloadUrl  = "https://github.com/$Repo/releases/download/$Version/$Archive"
$ChecksumUrl  = "https://github.com/$Repo/releases/download/$Version/checksums.txt"

$TmpDir = Join-Path $env:TEMP "ffc_install_$([System.IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Path $TmpDir | Out-Null

try {
    $ArchivePath  = Join-Path $TmpDir $Archive
    $ChecksumPath = Join-Path $TmpDir "checksums.txt"

    # --- download ---
    Invoke-WebRequest -Uri $DownloadUrl  -OutFile $ArchivePath  -UseBasicParsing
    Invoke-WebRequest -Uri $ChecksumUrl  -OutFile $ChecksumPath -UseBasicParsing

    # --- verify checksum ---
    $Expected = (Get-Content $ChecksumPath | Where-Object { $_ -match $Archive }) -split '\s+' | Select-Object -First 1
    $Actual   = (Get-FileHash -Algorithm SHA256 -Path $ArchivePath).Hash.ToLower()

    if ($Expected -and ($Actual -ne $Expected.ToLower())) {
        Write-Error "Checksum mismatch!`n  Expected: $Expected`n  Got:      $Actual"
        exit 1
    }

    # --- extract ---
    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    # --- choose install dir ---
    $InstallDir = "$env:LOCALAPPDATA\Programs\ffc"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

    Move-Item -Path (Join-Path $TmpDir $Binary) -Destination (Join-Path $InstallDir $Binary) -Force

    # --- add to user PATH if missing ---
    $UserPath = [System.Environment]::GetEnvironmentVariable("PATH", "User")
    if ($UserPath -notlike "*$InstallDir*") {
        [System.Environment]::SetEnvironmentVariable("PATH", "$UserPath;$InstallDir", "User")
        Write-Host ""
        Write-Host "Added $InstallDir to your PATH."
        Write-Host "Restart your terminal for the change to take effect."
    }

    Write-Host ""
    Write-Host "Installed to $InstallDir\$Binary"
    Write-Host "Run 'ffc --help' to get started. Use 'ffc init' to configure your first site."

} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
