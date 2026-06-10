$ErrorActionPreference = 'Stop'

# Detect Architecture
$Arch = "amd64" # Windows only has amd64 release configured in release.yml

$Binary = "lfr-tunnel-windows-amd64.exe"
$Url = "https://github.com/peterrichards-lr/lfr-tunnel/releases/latest/download/$Binary"

$InstallDir = "$Home\AppData\Local\Programs\lfr-tunnel"
If (!(Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
}

$DestPath = Join-Path $InstallDir "lfr-tunnel.exe"

Write-Host "Downloading lfr-tunnel from $Url..."
Invoke-WebRequest -Uri $Url -OutFile $DestPath -UseBasicParsing

# Add to user PATH environment variable if not already present
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    Write-Host "Adding $InstallDir to user PATH..."
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path += ";$InstallDir"
}

Write-Host "lfr-tunnel installed successfully!"
Write-Host "Please restart your terminal to reload your PATH environment variable."
