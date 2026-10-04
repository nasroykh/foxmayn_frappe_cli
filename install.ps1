#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

# Force TLS 1.2 — Windows PowerShell 5.1 may otherwise default to TLS 1.0 and
# fail the HTTPS downloads (L31).
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo   = "nasroykh/foxmayn_frappe_cli"
$Binary = "ffc.exe"

# --- detect arch ---
# PROCESSOR_ARCHITECTURE reports the *process* arch (x86 in 32-bit PowerShell,
# AMD64 under ARM64 emulation), so ask for the OS arch first (D28).
$OsArch = $null
try { $OsArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
if (-not $OsArch) {
    $OsArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
}
$Arch = switch -Regex ($OsArch) {
    "^(X64|AMD64)$"   { "amd64" }
    "^(Arm64|ARM64)$" { "arm64" }
    default { throw "Unsupported architecture: $OsArch" }
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

    # --- verify checksum (fail closed) ---
    # SHA-256 only: checksums.txt comes from the same release as the archive, so
    # this catches a corrupt download, not a tampered release. Windows has no
    # built-in Ed25519 verifier; install.sh checks checksums.txt.sig, and every
    # later 'ffc update' verifies it. For provenance, use gh attestation verify.
    # [regex]::Escape: the archive name contains '.', a regex metacharacter (M2).
    $Line = Get-Content $ChecksumPath |
        Where-Object { $_ -match [regex]::Escape($Archive) } |
        Select-Object -First 1
    if (-not $Line) {
        throw "Checksum for $Archive not found in checksums.txt; aborting."
    }
    $Expected = (($Line -split '\s+') | Select-Object -First 1).ToLower()
    $Actual   = (Get-FileHash -Algorithm SHA256 -Path $ArchivePath).Hash.ToLower()
    if ($Actual -ne $Expected) {
        throw "Checksum mismatch!`n  Expected: $Expected`n  Got:      $Actual"
    }

    # --- extract ---
    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    # --- choose install dir ---
    $InstallDir = "$env:LOCALAPPDATA\Programs\ffc"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

    # A running ffc.exe (e.g. a detached MCP server) cannot be overwritten but
    # can be renamed, so move it aside first, as `ffc update` does (D29).
    $Target = Join-Path $InstallDir $Binary
    if (Test-Path $Target) {
        $Old = "$Target.old"
        try { Remove-Item -Force $Old -ErrorAction Stop } catch { }
        if (Test-Path $Old) { $Old = "$Target.old-$([DateTime]::UtcNow.Ticks)" }
        Move-Item -Path $Target -Destination $Old -Force
    }
    Move-Item -Path (Join-Path $TmpDir $Binary) -Destination $Target -Force
    Get-ChildItem -Path "$Target.old*" -ErrorAction SilentlyContinue |
        ForEach-Object { Remove-Item -Force $_.FullName -ErrorAction SilentlyContinue }

    # --- add to user PATH if missing ---
    # Work on the raw registry value so %VAR% entries stay unexpanded, and match
    # whole entries rather than substrings (D27).
    $EnvKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey("Environment")
    try {
        $RawPath = [string]$EnvKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $Entries = @($RawPath -split ';' | Where-Object { $_ -ne "" })
        $Present = $Entries | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') }
        if (-not $Present) {
            $NewPath = (@($Entries) + $InstallDir) -join ';'
            $EnvKey.SetValue("Path", $NewPath, [Microsoft.Win32.RegistryValueKind]::ExpandString)
            # Writing the registry directly does not notify running programs;
            # broadcast WM_SETTINGCHANGE so new terminals see the new PATH.
            try {
                Add-Type -Namespace FfcInstall -Name NativeMethods -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
                $Ignored = [UIntPtr]::Zero
                [void][FfcInstall.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, "Environment", 2, 5000, [ref]$Ignored)
            } catch {
                # Best effort: a new sign-in picks up the change anyway.
            }
            Write-Host ""
            Write-Host "Added $InstallDir to your PATH."
            Write-Host "Restart your terminal for the change to take effect."
        }
    } finally {
        $EnvKey.Close()
    }

    Write-Host ""
    Write-Host "Installed to $InstallDir\$Binary"
    Write-Host "Run 'ffc --help' to get started. Use 'ffc init' to configure your first site."
    Write-Host "Optional provenance check (needs gh): gh attestation verify <downloaded $Archive> --repo $Repo"

} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
