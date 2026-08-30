$ErrorActionPreference = 'Stop'

# Detect Architecture
$Arch = "amd64" # Windows only has amd64 release configured in release.yml

$Binary = "lfr-tunnel-windows-amd64.exe"

$ServerUrl = "{{SERVER_URL}}"
If ([string]::IsNullOrEmpty($ServerUrl) -or $ServerUrl -eq "{{SERVER_URL}}") {
    $ServerUrl = "https://lfr-demo.se"
}
$Url = "$ServerUrl/static/downloads/$Binary"

$DefaultInstallDir = "{{LFR_TUNNEL_WINDOWS_AMD64_INSTALL_DIR}}"
If ([string]::IsNullOrEmpty($DefaultInstallDir) -or $DefaultInstallDir -like "*{{*") {
    # The path agreed with the S1 team (#1591). The EDR exclusion is the wildcard
    # *\liferay\lfr-tunnel\lfr-tunnel.exe, so the last two directory segments and the binary
    # name are all load-bearing -- installing anywhere else leaves the client unexcluded.
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

Write-Host "Downloading lfr-tunnel from $Url..."
Try {
    Invoke-WebRequest -Uri $Url -OutFile $DestPath -UseBasicParsing
} Catch {
    Write-Error "Failed to download or write to $DestPath. If this is a protected or system path, please run PowerShell as Administrator."
    Exit 1
}

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
$LdmBinDir = "$Home\.ldm\bin"
if (!(Test-Path $LdmBinDir)) { New-Item -ItemType Directory -Force -Path $LdmBinDir | Out-Null }
$LdmTarget = "$LdmBinDir\lfr-tunnel.exe"
if (Test-Path $LdmTarget) { Remove-Item -Force $LdmTarget -ErrorAction SilentlyContinue }
try {
    New-Item -ItemType HardLink -Path $LdmTarget -Target $DestPath -ErrorAction Stop | Out-Null
} catch {
    Copy-Item -Force -Path $DestPath -Destination $LdmTarget -ErrorAction SilentlyContinue | Out-Null
}

Write-Host "lfr-tunnel installed successfully to $DestPath"
Write-Host "Please restart your terminal to reload your PATH environment variable."

