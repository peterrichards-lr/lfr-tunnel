# Client Configuration File

The `lfr-tunnel` client reads an optional YAML file — `~/.lfr-tunnel/config.yaml` — for anything
you would otherwise retype on every run: your subdomain, your ports, your gateway, your access
controls.

Nothing forces you to have one. But one of its settings has no equivalent anywhere else:
**`server_url:` names a gateway without pinning the client to it**, while `-server` and the
`LFT_*` server variables pin, silently disabling region election and failover (#1691). If you
read one section of this page, read [Gateways, regions and
failover](#gateways-regions-and-failover).

A complete, copy-pasteable file lives at
[`resources/client/client-config.example.yaml`](https://github.com/peterrichards-lr/lfr-tunnel/blob/master/resources/client/client-config.example.yaml).
A test asserts it covers every setting the client accepts, so it cannot quietly fall behind this
page or the code.

---

## Where the file lives

| Platform | Path |
| --- | --- |
| macOS / Linux | `~/.lfr-tunnel/config.yaml` |
| Windows | `%USERPROFILE%\.lfr-tunnel\config.yaml` |

That is the whole of the location logic. There is **no environment variable for the config
path** — `LFT_TOKEN_FILE` moves the token file (the one `token_file:` names), not this one — and
if your home directory cannot be resolved at all, the client falls back to `client-config.yaml`
in the current working directory.

`-config <path>` reads a different file instead:

```bash
lfr-tunnel -config ./demo-tunnel.yaml -subdomain your-name-se
```

The two paths fail differently, deliberately:

* **The default path is optional.** If `~/.lfr-tunnel/config.yaml` does not exist, the client
  starts anyway on defaults, environment variables and flags.
* **An explicit `-config` path is not.** If the file you named cannot be opened, or does not
  parse as YAML, the client exits — naming a file and being given the defaults instead is worse
  than being told.

Unknown keys are ignored in silence, so a typo (`sub_domain:`) is not an error and takes no
effect. Check a key against the [reference](#key-reference) rather than against the absence of a
complaint.

### The client writes this file too

The Inspector's **Settings** tab (`http://localhost:4040`) and the system-tray GUI both save
back to `~/.lfr-tunnel/config.yaml`. A save rewrites the whole file from the client's in-memory
config, which means **your comments and any formatting are lost**. Keep a copy of a
hand-maintained file somewhere else if you care about the comments in it.

The file is written `0600` inside a `0700` directory. If you create it yourself, match that:
`auth_token`, `passcode` and `basic_auth` are all credentials.

---

## Precedence

Resolved in this order, each step overriding the one before it:

1. **Built-in defaults** — including the gateway URL compiled into your binary, if the build had
   one.
2. **The config file** — the default path, or `-config <path>`.
3. **`token_file:`**, if the config file sets it — the token is read from the file it names,
   **overriding an inline `auth_token:`**. `$LFT_TOKEN_FILE` moves that path, as it does for the
   fallback below, so the environment still beats the file. A path that cannot be read is a
   fatal error, not a fall-through. See [Keeping the token out of this
   file](#keeping-the-token-out-of-this-file).
4. **Token file fallbacks** — only when `auth_token` is still empty. `$LFT_TOKEN_FILE`, else
   `~/.lfr-tunnel/token` (what `lfr-tunnel login` writes), else `~/.config/lfr/secrets` and
   `~/.config/lfr/secrets.ps1`.
5. **Environment variables** — `LFT_CLIENT_*` is checked first, then the shorter `LFT_*` alias.
6. **Command-line flags.**
7. **`-gateway <url>`**, which replaces the gateway after everything above — see below.
8. **Region election**, which replaces the gateway again with the closest region's, unless the
   client is pinned or `region:` is set.

**Auto-discovery sits underneath all of that, and only for `ports` and `target_host`.** It runs
*after* the eight steps above — it is the last thing to touch the configuration — but it only
fills a value in where every step above left one empty. It never overrides one, so in priority
terms it is the bottom of the chain rather than the top: for the host, `-target-host` beats
`LFT_TARGET_HOST` beats `target_host:` beats discovery. See
[Leaving `ports` unset](#leaving-ports-unset) and
[Leaving `target_host` unset](#leaving-target_host-unset).

Four consequences worth knowing:

* **`token_file:` is the one key that outranks another key in the same file.** Everywhere else
  the config file is a single layer and later steps beat it wholesale. Writing `token_file:` says
  the token is not in this file, so an `auth_token:` left beside it is stale by construction —
  and quietly preferring the stale one is the "it looked like a setting and did nothing" failure
  this key was filed under (#1709). Steps above it still win: `LFT_CLIENT_TOKEN` and `-token`
  both beat it.
* **A built-in default is not always step 1.** `target_host` has no default in the config at
  all; the `127.0.0.1` you get is applied at the moment the client dials, after discovery has
  had its turn. That is what puts it *below* discovery in the chain, where reading step 1 would
  put it above.
* **An empty value never overrides.** Every override is guarded on "not empty", so
  `LFT_SUBDOMAIN=""` does not clear a `subdomain:` from the file, and `-rate-limit 0` cannot put
  `0` back over a `rate_limit: 100` in the file. Edit the file, or point `-config` at a
  different one.
* **A boolean flag can only turn a setting on.** `-preserve-host` and `-insecure-skip-verify`
  set `true`; there is no flag spelling that forces `false` back over a `true` in the file.

### Flag and environment equivalents

Settings not listed here can only be set in the config file.

| Key | Flag | Environment |
| --- | --- | --- |
| `server_url` | `-gateway <url>` (no pinning), `-server <url>` (**pins**) | `LFT_CLIENT_SERVER`, `LFT_SERVER_URL`, `LFT_SERVER` (all **pin**) |
| `auth_token` | `-token` | `LFT_CLIENT_TOKEN`, `LFT_TOKEN` |
| `token_file` | — | `LFT_TOKEN_FILE` (**moves** the path this key names) |
| `subdomain` | `-subdomain` | `LFT_CLIENT_SUBDOMAIN`, `LFT_SUBDOMAIN` |
| `custom_domain` | `-domain` | `LFT_CLIENT_CUSTOM_DOMAIN`, `LFT_CUSTOM_DOMAIN` |
| `ports` | `-ports 8080,3000` | `LFT_CLIENT_PORTS` |
| `target_host` | `-target-host` | `LFT_TARGET_HOST` |
| `region` | `-region` | `LFT_CLIENT_REGION`, `LFT_REGION` |
| `passcode` | `-passcode` | `LFT_CLIENT_PASSCODE`, `LFT_PASSCODE` |
| `whitelist_ips` | `-whitelist-ip` | `LFT_CLIENT_WHITELIST_IPS`, `LFT_WHITELIST_IPS` |
| `latency` | `-latency` | `LFT_CLIENT_LATENCY`, `LFT_LATENCY` |
| `bandwidth` | `-bandwidth` | `LFT_CLIENT_BANDWIDTH`, `LFT_BANDWIDTH` |
| `rate_limit` | `-rate-limit` | — |
| `basic_auth` | `-basic-auth` | — |
| `preserve_host` | `-preserve-host` | — |
| `insecure_skip_verify` | `-insecure-skip-verify` | — |
| `theme` | `-theme` | — |
| `log_dir` | `-log-dir` | `LFT_LOG_DIR` |
| `disable_latency_report` | — | `LFT_DISABLE_LATENCY_REPORT` (`1`, `true`, `yes`) |

---

## Gateways, regions and failover

Three things can name a gateway, and **they do not mean the same thing**. This is the part users
get wrong, and it is invisible when it goes wrong: a pinned client works, it just works from the
wrong continent and stops when that one gateway stops.

| How you supply it | Elects the closest region | Fails over |
| --- | --- | --- |
| `server_url:` in this file | ✅ | ✅ |
| `-gateway <url>` | ✅ | ✅ |
| `-server <url>` | ❌ pinned | ❌ |
| `LFT_SERVER_URL` / `LFT_CLIENT_SERVER` / `LFT_SERVER` | ❌ pinned | ❌ |

`server_url:` is a **starting point**: the client asks that gateway for the region list, probes
each region, moves to whichever answers fastest, and re-registers elsewhere if its gateway goes
away. Pinning is intended behaviour for `-server` — sometimes one specific gateway is exactly
what you want — but it is a different intent, so passing `-gateway` and `-server` together is
refused rather than resolved by a precedence rule.

The client prints a notice when it is pinned and more than one region exists. If you see it, and
you meant "start here", move the URL into this file.

### `region:` skips the election

```yaml
region: "eu"
```

A `region:` value means **the latency probe never runs**. The client resolves the name against
the region list and connects there, however far away it is. This surprises people who set it
once for a demo, forget it, and later wonder why a machine that moved continents is still on the
old gateway. Failover still works — a `region:` client is not pinned — but the initial choice is
yours, not the network's.

Leave `region:` empty to get the election. Its result is cached for 24 hours in
`~/.lfr-tunnel/region_cache.json`; `-refresh-region` re-probes immediately. If the region you
name is offline, the client says so and probes for the next best one instead.

`regions:` is a fallback map of `name: url` for a client that cannot reach any gateway to ask.
The live list is fetched from `<server_url>/api/version` at startup and **replaces** whatever is
in the file, so on a normal deployment there is no reason to set it.

---

## Key reference

Every key below is optional. Types are YAML types; a duration is a Go duration string such as
`200ms`, `1.5s` or `2m`, quoted.

### Gateway and region

| Key | Type | Default | What it does |
| --- | --- | --- | --- |
| `server_url` | string | the gateway compiled into your build, if any | Gateway to start from, **without pinning**. Example: `"https://tunnel.example.com"`. With no compiled-in default and nothing set here, the client exits with instructions rather than guessing. |
| `region` | string | *empty* — elect by latency | Connect to this region and skip the election. Example: `"eu"`. Falls back to probing if the name is not currently online. |
| `regions` | map of string → string | *empty* — fetched from the gateway | Fallback region-name → gateway-URL map. Replaced by the live list whenever the gateway answers. |
| `disable_latency_report` | bool | `false` | Stops the client reporting its region probe round-trips. What is otherwise sent is a region name and a round trip in milliseconds — no IP, and nothing derived from one. |

### Identity

| Key | Type | Default | What it does |
| --- | --- | --- | --- |
| `auth_token` | string | *empty* — read from the token file | Your Personal Access Token. Prefer `~/.lfr-tunnel/token` or `token_file`; see [Secrets](#secrets). |
| `token_file` | string | *empty* | A file to read the Personal Access Token from, so it need not be in this file at all. A leading `~` expands. **Overrides `auth_token`**, and is overridden by `LFT_CLIENT_TOKEN` and `-token`. Unlike the fallbacks, a path that cannot be read **stops the client** rather than leaving it with no token. Example: `"~/.lfr-tunnel/token"`. |
| `subdomain` | string | this machine's hostname | Requested subdomain prefix. Example: `"your-name-se"`. The hostname fallback takes the first label, lowercases it and turns spaces and underscores into dashes; if even that is unavailable it uses `se-dev`. |
| `custom_domain` | string | *empty* | A custom domain already reserved for you in the portal, used instead of a subdomain. Example: `"demo.example.com"`. |

### What is exposed

| Key | Type | Default | What it does |
| --- | --- | --- | --- |
| `ports` | list of int | *unset* — discovered, see below | Local ports to publish. The first is the primary; each subsequent port gets its own subdomain, suffixed with the port number. Example: `[8080, 3000]`. |
| `target_host` | string | *unset* — discovered, then `127.0.0.1`; see below | Hostname or IP the tunnel forwards to — a Docker service name, another machine on your LAN. Example: `"liferay"`. |
| `preserve_host` | bool | `false` | Forward the visitor's `Host` header unchanged instead of rewriting it to `target_host`. Needed when the local application generates absolute URLs from the host it is asked for. |
| `insecure_skip_verify` | bool | `false` | Skip TLS verification when the **local** target serves HTTPS with a self-signed certificate. It has no effect on the connection to the gateway. |

#### Leaving `ports` unset

`ports` is the one key here whose default is not a value but a **decision**. Set it — in this
file, with `-ports`, or via `LFT_CLIENT_PORTS` — and the client publishes exactly that list and
does nothing else. Leave it out and the client works the ports out for itself, in this order:

1. **In a Liferay workspace** (a `client-extensions/` directory, `gradlew`, or
   `gradle.properties` in the working directory) it walks the tree for `client-extension.yaml`
   files and reads each one's `port`. Port `8080` is always published first, on your plain
   subdomain; each extension is published alongside it on a subdomain suffixed with the
   extension's key.
2. **Otherwise** it looks for a running instance: `docker ps` for a container whose name or
   image mentions `liferay`, `dxp` or `ldm`, then a probe of `8080`, `13000` and `3000` on
   `127.0.0.1`. This step reports a **host** as well as ports — see
   [Leaving `target_host` unset](#leaving-target_host-unset).
3. **If neither finds anything**, port `8080`.

Two consequences worth knowing:

- `ports: [8080]` and no `ports` key at all are **not** the same thing. The first pins you to
  8080; the second is what gets you the scan. Until #1710 the client could not tell them
  apart — it defaulted to `[8080]`, which looked identical to an explicit request, so steps 1
  and 2 were unreachable for everyone.
- Step 2 can publish something other than 8080. A Docker container matching the name test
  above that publishes `80` or `443` and not `8080` will make that port your primary. Set
  `ports` explicitly if you need to be certain what gets published.

#### Leaving `target_host` unset

`target_host` is the second key here whose default is a decision rather than a value, and it is
decided by the same call. Step 2 above does not only report ports — it reports the **host** it
found them on, and that host becomes `target_host` when nothing else supplied one.

Resolved in this order, first match wins:

| # | Source | Beats |
| --- | --- | --- |
| 1 | `-target-host <host>` | everything below |
| 2 | `LFT_TARGET_HOST` | the config file and below |
| 3 | `target_host:` in this file | discovery and below |
| 4 | **Auto-discovery** — the host reported by step 2 above | the built-in default |
| 5 | `127.0.0.1` | — |

Four things follow from that:

- **Only step 2 supplies a host, so anything that skips step 2 skips the host too.** Pinning
  `ports` — in this file, with `-ports`, or via `LFT_CLIENT_PORTS` — turns the whole scan off,
  and the workspace scan (step 1) reports ports and nothing else. In either case `target_host`
  is whatever you set, or `127.0.0.1`. The two keys are decided by one call, so they are not
  independent: you cannot pin your ports and still have the host discovered.
- **A discovered `localhost` is rewritten to `127.0.0.1`, and that is not cosmetic.**
  `localhost` commonly resolves to `::1` ahead of `127.0.0.1`. A target bound only to IPv4 is
  still reached on the second try where `::1` is *refused* — but where `::1` is *filtered*, by a
  firewall that drops rather than rejects, that same fallback is a connection timeout instead of
  a fast retry. This client exists to show a local Liferay to a customer over a public URL, and
  a stall mid-demo costs immeasurably more than the microsecond the literal saves. So the client
  dials the literal, exactly as it does everywhere else it has to pick one.
- **Anything else discovery reports is used verbatim.** Only the exact string `localhost` is
  rewritten, never anything that merely looks loopback-ish. Both discovery paths report
  `localhost` today, so in practice discovery hands you `127.0.0.1` — the same endpoint the
  fallback would have given you, now set explicitly rather than left empty. The rule matters for
  the case that is coming: the `docker ps` path is the one that could reasonably learn to report
  a container name or a network alias, and rewriting a real hostname would forward your traffic
  somewhere else entirely.
- **`-target-host ""` suppresses nothing.** An empty value never overrides (see
  [Precedence](#precedence)), and it does not switch discovery off either — the client tests the
  host it ended up with, not whether you typed the flag.

One asymmetry worth knowing, unrelated to discovery: `LFT_TARGET_HOST` is parsed as a URL and
reduced to its hostname, so `http://liferay:8080` becomes `liferay`. `-target-host` and
`target_host:` are taken literally, so give those two a bare host or IP.

### Who may reach your tunnel

| Key | Type | Default | What it does |
| --- | --- | --- | --- |
| `passcode` | string | *empty* — no passcode | Passcode visitors must enter before the tunnel serves them. Example: `"letmein"`. |
| `whitelist_ips` | string | *empty* — no restriction | Comma-separated IP addresses and CIDR ranges allowed through. Example: `"203.0.113.4, 198.51.100.0/24"`. |
| `basic_auth` | string | *empty* | HTTP Basic Auth, as `"username:password"`. Applied by the gateway, so it protects the public URL, not just the local app. |
| `rate_limit` | int | `0` — let the gateway decide | Requests per second, per subdomain. The gateway lowers your figure to your account's limit and then to its own `max_tunnel_rate_limit`, and applies those to `0` as well — so `0` means "whatever the gateway allows", not "unlimited". Example: `50`. |

`passcode` and `whitelist_ips` combine as **OR**: a visitor who satisfies either one is let
through. Set only the one you mean.

### Local behaviour

| Key | Type | Default | What it does |
| --- | --- | --- | --- |
| `maintenance_path` | string | *empty* — built-in page | Path to an HTML file served with `503` while the Inspector's maintenance toggle is on. If the file cannot be read, the built-in page is served instead. Example: `"/Users/you/maintenance.html"`. |
| `latency` | duration | `0s` | Simulated round-trip latency added to every proxied request, for demonstrating a slow link. Example: `"200ms"`. |
| `bandwidth` | string | *empty* — unthrottled | Simulated bandwidth ceiling. Accepts `bps`, `kbps`, `mbps`, `gbps` and byte-per-second forms such as `kb/s`; a bare number is bytes per second. Example: `"512kbps"`. |
| `theme` | string | *empty* — your portal preference | Theme for the injected tunnel banner: `light`, `dark`, `system` or `time`. Example: `"dark"`. |
| `log_dir` | string | `~/.lfr-tunnel/logs` | Where the persistent traffic and error logs are written. A leading `~` is expanded. Changing it applies to the next run: the logs already open cannot be moved. Example: `"~/tunnel-logs"`. |
| `hooks` | map | *empty* — nothing runs | Shell commands run when the tunnel moves between gateways: `warning_received`, `stopping`, `stopped`, `starting`, `started`. Each is passed to `/bin/sh -c` with `LFT_EVENT`, `LFT_NODE_ID`, `LFT_SECONDS_REMAINING`, `LFT_FAILOVER_REGION` and `LFT_SUBDOMAIN` set, bounded at 15 seconds, and its exit status is logged but cannot veto the move. A pinned (`-server`) client fires none of them, because it never fails over. See [Client Lifecycle Hooks](getting_started.md#client-lifecycle-hooks-failover-automation) for the full contract. |

---

## Keys that are not settings

One is not a key at all; two were removed. They are listed here so that finding them in the
struct, the example file or someone else's config does not read as a feature you are missing.
`token_file` used to be on this list: it was removed in #1709 as never-implemented and then
implemented properly in #1758, and it is now a real setting — see
[Identity](#identity).

| Key | Status |
| --- | --- |
| `regions_unavailable` | Not a config key. The gateway reports the regions that are currently down, and the client uses that to cache a provisional election rather than a 24-hour one. It cannot be set from the file. |
| `bypass_proxy` | **Removed** (#1709). Never implemented — added with `theme` in July 2026 and read by nothing, in Go or in the UI. |
| `nav_placement` | **Removed** (#1751). Unlike the two above it did work: it selected the Inspector's own navigation layout — `"sidebar"`, or empty for top tabs — from #606 on 17 July 2026 until the dashboard rewrite in #783 deleted the JavaScript that applied it six days later, without saying so. The Go half stayed and kept accepting the key for six weeks, so the Inspector offered to save a setting it could not act on. The July 2026 layout switcher is not coming back; a future Inspector layout option should be designed afresh rather than by reviving this key. |

Leaving either removed key in a config file is harmless. The loader does not reject unknown
keys, so an existing `~/.lfr-tunnel/config.yaml` that still sets them loads exactly as it did
before — they are ignored now, which is what they were doing anyway. The one visible change is
that the Inspector no longer writes them back when it saves the file.

`token_file` is the exception, because it is a setting again: a config file that still carries a
`token_file:` left over from before now **has it honoured**, and the client stops with an error
if the path it names cannot be read. That is the intended behaviour and not a regression — the
path was one you meant to work — but it is the one retired key whose reappearance in an old file
does something.

---

## A complete example

Every setting that does something, with the values a real user is likely to want. Delete what
you do not need; nothing here is required.

```yaml
# ~/.lfr-tunnel/config.yaml

# --- Gateway and region ---

# Start from this gateway but still elect the closest region and fail over.
# Not the same as -server, which would pin this client here.
server_url: "https://tunnel.example.com"

# Leave empty to elect by latency. Setting it skips the probe entirely.
region: ""

# Fallback name -> URL map. Replaced by the live list the gateway serves.
regions: {}

# Stop reporting this client's region probe round-trip times.
disable_latency_report: false

# --- Identity ---

# Keep your token in ~/.lfr-tunnel/token instead of here.
auth_token: ""
subdomain: "your-name-se"
custom_domain: ""

# --- What is exposed ---

# Omit this key entirely to let the client scan your workspace / discover a running
# instance. Listing ports here pins them and skips discovery.
ports:
  - 8080
  - 3000
target_host: "127.0.0.1"
preserve_host: false
insecure_skip_verify: false

# --- Who may reach it. passcode and whitelist_ips combine as OR ---

passcode: ""
whitelist_ips: ""
basic_auth: ""
rate_limit: 0

# --- Local behaviour ---

maintenance_path: ""
latency: "0s"        # e.g. "200ms" to demonstrate a slow link
bandwidth: ""        # e.g. "512kbps"
theme: "system"
log_dir: ""

# --- Run something when the tunnel moves gateway. All optional. ---

hooks:
  warning_received: ""
  stopping: ""
  stopped: ""
  starting: ""
  started: "/usr/local/bin/repoint-virtual-host.sh"
```

The [committed
example](https://github.com/peterrichards-lr/lfr-tunnel/blob/master/resources/client/client-config.example.yaml)
carries the same keys plus `token_file`, so that it stays a complete record of everything the
parser accepts. It carries no removed key: a test rejects any key `ClientConfig` does not
accept, since a user copying one would believe they had applied a setting that does nothing.

---

## Secrets

> [!CAUTION]
> `auth_token`, `passcode` and `basic_auth` are credentials. Never commit this file to a
> repository, and never put it inside a project workspace.

Prefer keeping the token out of the file altogether. `lfr-tunnel login` writes it to
`~/.lfr-tunnel/token` with `0600` permissions, and the client reads it from there whenever
`auth_token` is empty. For the restricted-secrets-file approach used in LDM, see [Step 3 of the
Getting Started Guide](getting_started.md#step-3-authenticate-and-store-your-token).

### Keeping the token out of this file

`~/.lfr-tunnel/token` is picked up automatically, but only from that one path. `token_file:`
names any path you like:

```yaml
# ~/.lfr-tunnel/config.yaml -- contains no credential
server_url: "https://tunnel.example.com"
subdomain: "your-name-se"
token_file: "~/.config/lfr-tunnel/pat"
```

This is the setting to reach for when the config file is going to be **read by someone else** —
pasted into a support thread, committed to a dotfiles repository, copied to a second machine, or
quoted back to you from the startup configuration block. The token stays in a file you never
have to show anyone.

Three behaviours are worth knowing before you rely on it:

* **It wins over `auth_token:`.** If both are set, the file is used and the inline value is
  ignored. You do not have to get the two-step migration right in one edit.
* **A bad path stops the client**, with an error naming the path. It does not fall back to "no
  token", because that arrives later as an authentication failure and sends you to the portal to
  re-issue a PAT that was never the problem.
* **The file's permissions are checked but not enforced.** A group- or world-readable token file
  produces the same start-up warning as `~/.lfr-tunnel/token`, and the client carries on. It is a
  warning rather than a refusal on purpose: a file created with the usual `022` umask is `0644`,
  and the easiest way around a refusal would be putting the token back inline in this file —
  whose own permissions nothing checks at all. Set them yourself: `chmod 600` on whatever
  `token_file:` names.

The startup configuration block reports **which** of these supplied the token, by name and path,
and never the token itself:

```
[Client] Configuration:
  ...
  config file    default location
  token from     token_file (/Users/you/.config/lfr-tunnel/pat)
```

### This file's own permissions

On macOS and Linux the client warns on start-up if a token or secrets file is group- or
world-accessible. It does not check this file's permissions, so set them yourself:

```bash
chmod 600 ~/.lfr-tunnel/config.yaml
```

---

## See also

* [Getting Started Guide](getting_started.md) — installing, registering, claiming a token, and
  the flags you are most likely to use.
* [Liferay SE Guide](liferay-se-guide.md) — team-specific setup, LDM, and EDR path exclusions.
* [Control Plane Setup](server/setup_guide.md) — the *server* configuration file, which is a
  different file with different keys.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-06* | *Last Reviewed: 2026-09-06*
