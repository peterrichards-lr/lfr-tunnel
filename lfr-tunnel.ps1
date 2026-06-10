# Load .env file if it exists
if (Test-Path .env) {
    Get-Content .env | Where-Object { $_ -notmatch '^\s*#' -and $_ -match '=' } | ForEach-Object {
        $name, $value = $_.Split('=', 2)
        # Strip outer quotes if present
        $value = $value.Trim("`"'")
        [System.Environment]::SetEnvironmentVariable($name.Trim(), $value, [System.EnvironmentVariableTarget]::Process)
    }
}

# Check if LFT_TOKEN is set
$token = [System.Environment]::GetEnvironmentVariable("LFT_TOKEN")
if ([string]::IsNullOrEmpty($token)) {
    Write-Error "LFT_TOKEN is not set. Please copy '.env.example' to '.env' and configure your token."
    exit 1
}

# Set defaults
$server = [System.Environment]::GetEnvironmentVariable("LFT_SERVER")
if ([string]::IsNullOrEmpty($server)) { $server = "https://lfr-demo.se" }

$subdomain = [System.Environment]::GetEnvironmentVariable("LFT_SUBDOMAIN")

$ports = [System.Environment]::GetEnvironmentVariable("LFT_PORTS")
if ([string]::IsNullOrEmpty($ports)) { $ports = "8080" }

# Check if Docker image is built
docker image inspect lfr-tunnel-client:latest >$null 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "Docker image 'lfr-tunnel-client:latest' not found. Building it now..." -ForegroundColor Yellow
    docker build --load -t lfr-tunnel-client:latest .
}

Write-Host "[Docker Client] Launching tunnel..." -ForegroundColor Green

# Build arguments list
$dockerArgs = @("run", "--rm", "-it", "-e", "LFT_TARGET_HOST=host.docker.internal", "lfr-tunnel-client:latest", "-server", $server, "-token", $token)

if (-not [string]::IsNullOrEmpty($subdomain)) {
    $dockerArgs += @("-subdomain", $subdomain)
}

$dockerArgs += @("-ports", $ports)

# Append any remaining command-line arguments
if ($args) {
    $dockerArgs += $args
}

# Run docker
& docker $dockerArgs
