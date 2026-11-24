$ErrorActionPreference = 'Stop'

# Detect Architecture
$Arch = "amd64" # Windows only has amd64 release configured in release.yml

$Binary = "lfr-tunnel-windows-amd64.exe"

# Matched on the BRACES, not against the placeholder's own text (#1684). The gateway substitutes
# with strings.ReplaceAll, so a comparison written as `-eq "{{SERVER_URL}}"` has its right-hand
# side replaced too -- both sides then hold the gateway URL, the guard is always true, and the
# fallback fired on every run, pointing users at a host that does not serve the binaries.
#
# No hardcoded fallback either: a script that does not know its gateway cannot guess one.
$ServerUrl = "{{SERVER_URL}}"
If ([string]::IsNullOrEmpty($ServerUrl) -or $ServerUrl -like "*{{*") {
    Write-Error "This installer does not know which gateway to download from. It was not served by one -- fetch it from your gateway: irm https://<your-gateway>/install.ps1 | iex"
    Exit 1
}
$Url = "$ServerUrl/static/downloads/$Binary"

$DefaultInstallDir = "{{LFR_TUNNEL_WINDOWS_AMD64_INSTALL_DIR}}"
If ([string]::IsNullOrEmpty($DefaultInstallDir) -or $DefaultInstallDir -like "*{{*") {
    # The path agreed with the S1 team (#1591). The EDR exclusion is
    # C:\Users\*\liferay\lfr-tunnel\lfr-tunnel.exe (docs/infosec.md section 3), so the last two
    # directory segments and the binary name are all load-bearing -- installing anywhere else
    # leaves the client unexcluded.
    $DefaultInstallDir = "$Home\liferay\lfr-tunnel"
}

$InstallDir = $env:LFR_TUNNEL_WINDOWS_AMD64_INSTALL_DIR
If (-not $InstallDir) {
    $InstallDir = $env:LFR_TUNNEL_INSTALL_DIR
}
If (-not $InstallDir) {
    $InstallDir = $env:LFT_INSTALL_DIR
}
If (-not $InstallDir) {
    $InstallDir = $DefaultInstallDir
}

# Expand a leading tilde. PowerShell only resolves "~" through its path provider, so a value
# carrying one -- as the gateway's default does -- can otherwise land somewhere other than the
# user's profile depending on the current location.
#
# Separators are deliberately NOT normalised here. The gateway sends Windows a backslash path,
# so there is nothing to convert, and normalisation logic could not be exercised anywhere but
# Windows -- untestable code guarding a path the EDR exclusion matches literally (#1591).
If ($InstallDir -match '^~([\\/]|$)') {
    $InstallDir = Join-Path $Home ($InstallDir -replace '^~[\\/]?', '')
}

If (!(Test-Path $InstallDir)) {
    Try {
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    } Catch {
        Write-Error "Failed to create directory $InstallDir. If this is a protected or system path, please run PowerShell as Administrator."
        Exit 1
    }
}

$DestPath = Join-Path $InstallDir "lfr-tunnel.exe"

# The same mechanism as install.sh and --upgrade (#2505): the current binary is renamed aside --
# Windows allows renaming a running .exe, not overwriting it -- the download goes straight to the
# install path, and on failure the previous binary is put back. A .old still locked by a running
# client is removed by lfr-tunnel itself at its next start.
$OldPath = "$DestPath.old"
if (Test-Path $OldPath) { Remove-Item -Force $OldPath -ErrorAction SilentlyContinue }
$HadOld = $false
if (Test-Path $DestPath) {
    Try {
        Move-Item -Force -Path $DestPath -Destination $OldPath -ErrorAction Stop
        $HadOld = $true
    } Catch {
        Write-Error "Could not move the current $DestPath aside. Close lfr-tunnel, or run PowerShell as Administrator, and try again."
        Exit 1
    }
}

Write-Host "Downloading lfr-tunnel from $Url..."
Try {
    Invoke-WebRequest -Uri $Url -OutFile $DestPath -UseBasicParsing
    if (!(Test-Path $DestPath) -or (Get-Item $DestPath).Length -eq 0) { Throw "empty download" }
} Catch {
    Remove-Item -Force $DestPath -ErrorAction SilentlyContinue
    if ($HadOld) {
        Move-Item -Force -Path $OldPath -Destination $DestPath -ErrorAction SilentlyContinue
        Write-Error "Failed to download $Url; the previous lfr-tunnel at $DestPath is unchanged."
    } else {
        Write-Error "Failed to download or write to $DestPath. If this is a protected or system path, please run PowerShell as Administrator."
    }
    Exit 1
}
if ($HadOld) { Remove-Item -Force $OldPath -ErrorAction SilentlyContinue }

# Add target installation directory to user PATH environment variable if not already present
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    Write-Host "Adding $InstallDir to user PATH..."
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path += ";$InstallDir"
}

# Configure LDM (liferay-docker-manager) auto-discovery to prevent un-whitelisted auto-downloads (#1311)
[Environment]::SetEnvironmentVariable("LDM_LFR_TUNNEL_BIN", $DestPath, "User")
$env:LDM_LFR_TUNNEL_BIN = $DestPath
# The .ldm\bin link is retired (#2579): LDM 2.25.0 and later find the client through
# LDM_LFR_TUNNEL_BIN above or the install path itself. An earlier install put a hardlink (or a
# copy) of the client there: an executable outside the EDR-excluded path, so it is removed. That
# name was always the installer's own; this installer used to replace it unconditionally.
$LdmTarget = "$Home\.ldm\bin\lfr-tunnel.exe"
foreach ($Retired in @($LdmTarget, "$LdmTarget.old")) {
    if (Test-Path $Retired) {
        Remove-Item -Force $Retired -ErrorAction SilentlyContinue
        if (Test-Path $Retired) {
            Write-Host "Could not remove the retired LDM link $Retired (in use); lfr-tunnel removes it at its next upgrade."
        } else {
            Write-Host "Removed the retired LDM link $Retired."
        }
    }
}

Write-Host "lfr-tunnel installed successfully to $DestPath"
Write-Host "Please restart your terminal to reload your PATH environment variable."

