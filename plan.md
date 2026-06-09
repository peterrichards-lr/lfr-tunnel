# Implementation Plan: lfr-tunnel

This document outlines the step-by-step implementation plan for the `lfr-tunnel` project.

## Step 1: Shared Configuration System
Create `pkg/config/config.go` with structure definitions for both server and client configurations. It will include helpers to load config files (YAML) and fallback to environment variables.

## Step 2: Liferay-Themed Offline Page
Create `static/offline.html` containing a responsive, modern, and high-quality "Offline" page. This page will be served if a requested tunnel is inactive or the client machine is powered off.

## Step 3: Server Registry and Auth Manager
Create `pkg/server/auth.go` to handle:
- In-memory registry of active subdomains, mapped to target ports and active Chisel user sessions.
- Generating random session credentials (usernames/passwords) for each tunnel connection.
- A custom Chisel `AuthHook` that validates incoming tunnel requests against the dynamically created leases.

## Step 4: Reverse Proxy with Liferay Headers
Create `pkg/server/proxy.go` to implement:
- A dynamic reverse HTTP proxy that inspects the Host header.
- Routing of requests to the corresponding localhost port mapped by Chisel.
- Injection of Liferay headers (`X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Forwarded-For`, `X-Real-IP`).
- Handling proxy errors gracefully and rendering the Liferay offline page instead of a generic 502.

## Step 5: Server Entrypoint
Create `pkg/server/server.go` and `cmd/lfr-tunneld/main.go` to coordinate:
- Setting up the registration API endpoint (`/api/register`).
- Initializing the embedded Chisel server.
- Starting the HTTP and HTTPS gateway servers.
- Automatic SSL termination (with cert/key files if provided, or fallback to auto HTTP-01 Let's Encrypt).

## Step 6: Client CLI Implementation
Create `pkg/client/client.go` and `cmd/lfr-tunnel/main.go` to implement:
- Registration handshake with `/api/register`.
- Spawning the embedded Chisel client with the leased credentials and remotes.
- Automatically parsing `client-extension.yaml` if it exists in the workspace to auto-detect target ports.

## Step 7: Testing & Verification
- Write unit tests for the registry and proxy.
- Provide a step-by-step validation guide.
