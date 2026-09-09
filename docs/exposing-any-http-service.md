# Exposing Any Local HTTP Service

> **This is an addendum.** `lfr-tunnel` exists to expose Liferay, and the
> [Getting Started Guide](getting_started.md) is the guide to follow — its Liferay workspace and
> standalone-bundle modes are the paths almost everyone wants. This page covers one thing that
> guide does not say out loud: the tunnel does not inspect what is listening on the port it
> publishes, so anything speaking HTTP on localhost works the same way.

## The short version

```bash
lfr-tunnel -subdomain my-service -ports 3001
```

Whatever answers on `127.0.0.1:3001` is now served at `https://my-service.<gateway-domain>`. A
Liferay client extension in dev mode, a Node or Express app, a Vite dev server, a Python service,
a mock API — the client neither knows nor cares which.

There is nothing to enable and no separate mode. `-ports` takes port numbers and does no other
checking: it splits the value on commas and parses each part as an integer. No probe, no
handshake, no "is this Liferay?" test exists anywhere in that path.

## Why the rest of the documentation reads as Liferay-only

Because the *convenience* features genuinely are Liferay-specific, and they are what the guide
spends its time on:

- scanning a Liferay Workspace for `client-extension.yaml` files and taking each extension's port
- looking for a running Liferay or LDM instance when you are not in a workspace, including reading
  `docker ps` for containers whose name or image mentions `liferay`, `dxp` or `ldm`
- falling back to probing `8080`, `13000` and `3000`

All of that is **auto-discovery** — the client working out ports you did not give it. It is not a
restriction on what may be forwarded to.

**The sentence that makes both modes make sense together:** naming ports yourself switches
auto-discovery off entirely. The Liferay-specific scanning only runs when `-ports`, `ports:` in
your config file and `LFT_CLIENT_PORTS` are all absent. Give a port and none of it executes, so
none of it can get in your way.

## Several services at once

```bash
lfr-tunnel -subdomain my-service -ports 8080,3001
```

The **first** port takes the plain subdomain; each one after it gets the port number appended:

| Port | URL |
|---|---|
| `8080` | `https://my-service.<gateway-domain>` |
| `3001` | `https://my-service-3001.<gateway-domain>` |

One hostname serves one port — the tunnel does not multiplex several services behind a single
name. Order matters, so put the service you want on the bare subdomain first.

> Inside a Liferay Workspace, discovered client extensions are suffixed with the **extension's
> key** rather than its port number — `your-name-se-my-remote-app`. Explicit `-ports` uses the
> number, as above. See
> [Zero-Config Workspace Mode](getting_started.md#zero-config-workspace-mode-ldmworkspaces).

## When the service is not on localhost

If it runs in a container, a VM or on another machine, point the client at it:

```bash
lfr-tunnel -subdomain my-service -ports 3001 -target-host 192.168.1.50
```

`-target-host` always wins over anything auto-discovery found. With nothing set and nothing
discovered you get `127.0.0.1` — and note that a *discovered* `localhost` is deliberately
normalised to `127.0.0.1`, because `localhost` often resolves to `::1` first and a dropped IPv6
connection costs you a hang rather than a fast failure. The full precedence order is in
[Leaving `target_host` unset](client_configuration.md#leaving-target_host-unset).

## What does not change

Everything else behaves exactly as the main guide describes, because none of it depends on what is
being served: passcodes and IP allow-lists, rate limiting, the request Inspector on
`localhost:4040`, regional failover, and the traffic log.

Two Liferay-oriented options simply have nothing to act on and can be ignored:

- **`-preserve-host`** matters when the upstream generates absolute URLs from the `Host` header,
  which is a Liferay concern. Most microservices do not care.
- **Workspace auto-discovery** does not run at all once you have named a port.

## Caveats worth knowing before you rely on this

- **Your service must speak HTTP.** The tunnel proxies HTTP and WebSocket traffic. A raw TCP
  protocol — a database port, SSH — is not what this forwards.
- **It must actually be listening before traffic arrives.** A dial failure to the local port is
  surfaced as a 502 with the tunnel's own error page, which looks like a gateway problem but is
  usually just a service that has not started yet.
- **The mechanics above are read from the client's source**, and are accurate as of v1.48.26. What
  has *not* been done is a full end-to-end run of a non-Liferay service through a production
  tunnel, so treat the worked examples as correct-by-construction rather than as a tested recipe.
  If something here does not behave as described, that is worth reporting as a bug rather than
  assumed to be your mistake.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-09* | *Last Reviewed: 2026-09-09*
