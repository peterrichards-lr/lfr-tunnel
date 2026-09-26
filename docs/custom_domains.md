# Using Your Own Domain

By default a tunnel is served at `https://<your-name>.<gateway-domain>`. You can instead serve it
at a domain you own — `https://demo.customer.com` — with a real certificate, for a demo that has
to look like the customer's own site.

There is one thing only you can do, and the gateway does the rest.

## The short version

1. **Point the domain at the gateway** with a `CNAME` record.
2. **Register it in the portal**, under *Custom Domains* on your dashboard.
3. **Connect with `-domain`**:

   ```bash
   lfr-tunnel -domain demo.customer.com
   ```

The gateway obtains and installs the TLS certificate itself. You never touch Let's Encrypt.

## 1. Create the CNAME

Point the name at your gateway's hostname:

```
demo.customer.com.    CNAME    tunnel.lfr-demo.se.
```

Use the hostname your team's gateway actually serves — ask whoever runs it if you are not sure.

**Do this before you connect, not after.** The gateway proves you control the domain by answering
a challenge on it: it serves a one-time file over HTTP at
`http://demo.customer.com/.well-known/acme-challenge/…`, and Let's Encrypt fetches that over the
public internet. If the name does not resolve to the gateway at that moment, there is nothing to
fetch and no certificate is issued.

That is also why nobody has to verify your ownership separately. **Creating the CNAME is the
proof** — it is a record only someone with authority over the domain can add, and a name that has
not been pointed here can never be served no matter who registers it.

### If the domain is already serving something

Pointing it at the tunnel takes it away from wherever it points now. Use a name you can spare —
`demo.`, `preview.`, `sandbox.` — rather than the customer's live `www`.

## 2. Register it in the portal

Open the portal, find **Custom Domains** on your dashboard, and add the domain there.

You need to do this before connecting. A client that connects with `-domain` for a name you have
not registered is refused, and says so.

Two things worth knowing:

- **You get one custom domain by default.** The quota is separate from your subdomain quota, and
  the panel shows how much of it you have used. Ask an admin if you need more.
- **The reservation is permanent on gateways configured for it.** The reasoning is that nobody
  else can claim a name you control through DNS, so there is no shared namespace to reclaim and
  nothing to expire for. Whether that reasoning is accepted is now the operator's setting rather
  than a fixed rule (`never_expires.custom_domains`), so on some gateways a custom domain carries
  an expiry like a subdomain does, and on others an admin grants the permanence on request. The
  panel shows which of those applies to the domain you hold.
- **If it does expire, it expires on its own clock.** A custom domain's lifetime comes from
  `custom_domain_expiry_days`, not from the subdomain setting — they are separate because the two
  resources are not comparable. Unset, a custom domain gets 90 days where a subdomain gets 7.

## 3. Connect

```bash
lfr-tunnel -domain demo.customer.com
```

Or put it in your client configuration file, so you do not repeat it:

```yaml
custom_domain: "demo.customer.com"
```

See the [Client Configuration File](client_configuration.md) reference for where that file lives.

## Watching it provision

The first connection is when the gateway sets your domain up: it writes an nginx site, requests
the certificate, and switches the site to HTTPS. That takes a few seconds and is visible in the
portal — the **Custom Domain Status** panel shows each stage as it completes, and marks the one
that failed if something goes wrong.

The commonest failure is the CNAME: either it does not exist yet, or DNS has not propagated. If a
stage fails for that reason, fix the record and reconnect the client: registration alone does not
provision anything, and the gateway runs the setup again on the next connection that carries the
domain. The status panel is read-only for you — retrying in place is an administrator action, on
the fleet-wide *Custom Domains* page.

## Giving the domain up

Release it from the portal, in the same place you registered it.

**Release it rather than just disconnecting.** Releasing is the action that tears down the nginx
site and the certificate on the gateway; disconnecting the client only ends the tunnel, and leaves
both behind. Once released, the name is free for anyone to register.

## What the gateway does for you

Worth stating, because it is easy to assume you have more to do than you do:

| | |
|---|---|
| DNS | **yours** — the `CNAME`, once |
| Registering the name | **yours** — once, in the portal |
| TLS certificate | the gateway's — requested, installed and renewed for you |
| ACME challenge | the gateway's — served from its own webroot |
| nginx configuration | the gateway's — written and removed automatically |

The certificate is requested under the server owner's contact address, not yours, and renews
without anyone doing anything.

## If your gateway does not offer this

Custom domains depend on a hook the server owner enables, and on some setup on the gateway host.
If the option is missing from your portal, that is why — see
[Control Plane Setup §4.7](server/setup_guide.md) for what an operator needs to do.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-26* | *Last Reviewed: 2026-09-26*
