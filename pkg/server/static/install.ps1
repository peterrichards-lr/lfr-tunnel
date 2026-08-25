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
    $DefaultInstallDir = "$Home\runningpoc\bin"
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

# Configure LDM (liferay-docker-manager) auto-discovery to prevent un-whitelisted auto-downloads
[Environment]::SetEnvironmentVariable("LDM_LFR_TUNNEL_BIN", $DestPath, "User")
$env:LDM_LFR_TUNNEL_BIN = $DestPath
if (Test-Path "$Home\.ldm") {
    $LdmBinDir = "$Home\.ldm\bin"
    if (!(Test-Path $LdmBinDir)) { New-Item -ItemType Directory -Force -Path $LdmBinDir | Out-Null }
    $LdmTarget = "$LdmBinDir\lfr-tunnel.exe"
    if (Test-Path $LdmTarget) { Remove-Item -Force $LdmTarget -ErrorAction SilentlyContinue }
    New-Item -ItemType HardLink -Path $LdmTarget -Target $DestPath -ErrorAction SilentlyContinue | Out-Null
}

Write-Host "lfr-tunnel installed successfully to $DestPath"
Write-Host "Please restart your terminal to reload your PATH environment variable."

