@echo off
setlocal enabledelayedexpansion

:: Load .env file if it exists
if exist .env (
    for /f "usebackq delims=" %%x in (`findstr /v "^#" .env`) do (
        set "line=%%x"
        :: Remove any double quotes
        set "line=!line:"=!"
        for /f "tokens=1,* delims==" %%a in ("!line!") do (
            set "%%a=%%b"
        )
    )
)

:: Validate LFT_TOKEN
if "%LFT_TOKEN%"=="" (
    echo Error: LFT_TOKEN is not set.
    echo Please copy '.env.example' to '.env' and configure your token.
    exit /b 1
)

:: Set defaults
if "%LFT_SERVER%"=="" set "LFT_SERVER=https://lfr-demo.se"
if "%LFT_PORTS%"=="" set "LFT_PORTS=8080"

:: Check if Docker image is built
docker image inspect lfr-tunnel-client:latest >nul 2>&1
if %errorlevel% neq 0 (
    echo Docker image 'lfr-tunnel-client:latest' not found. Building it now...
    docker build --load -t lfr-tunnel-client:latest .
)

echo [Docker Client] Launching tunnel...

:: Determine if subdomain flag should be passed
set "subdomain_flag="
if not "%LFT_SUBDOMAIN%"=="" (
    set "subdomain_flag=-subdomain %LFT_SUBDOMAIN%"
)

:: Set default target host if not configured
if "%LFT_TARGET_HOST%"=="" set "LFT_TARGET_HOST=host.docker.internal"

:: Run container
docker run --rm -it ^
  -e LFT_TARGET_HOST=%LFT_TARGET_HOST% ^
  lfr-tunnel-client:latest ^
  -server "%LFT_SERVER%" ^
  -token "%LFT_TOKEN%" ^
  %subdomain_flag% ^
  -ports "%LFT_PORTS%" ^
  %*

endlocal
