---
name: liferay-target-testing
description: Read before verifying how Liferay itself behaves behind the tunnel (Host header, virtual hosts, virtual.hosts.valid.hosts, generated links, preserve_host) by running Liferay in Docker. Covers the control hostname every run needs, the pre-built database that silently ignores instance properties, basic auth answering 403 (and the sign-in route that works instead), and why Liferay's 500 body cannot identify its cause.
---

# Verifying Liferay's behaviour as a tunnel target

The SE guide (`docs/liferay-se-guide.md`) makes claims about Liferay rather than about this repo's
code: which hostname Liferay generates links for, what it refuses, and what fixes that. They can
only be checked against a real Liferay. Run it in Docker. That is outside the EDR constraints on
host binaries ([`edr-constraints`](../edr-constraints/SKILL.md)), and so is `curl` against it.
Every finding below was measured on `liferay/dxp:2026.q1.14-lts` on 2026-11-23 (#2561).

## 1. Every run needs a control hostname

A request that returns 200 after a "fix" proves nothing unless a hostname the fix does **not**
cover still fails in the same run. The control is what separates "the fix worked" from "the check
was never going to fail" (AGENTS.md, *A passing check is evidence only if it could have failed*).
Send the same `GET /` with only `Host` changed:

```bash
docker run -d --name <unique-name> -p 127.0.0.1:<free-port>:8080 liferay/dxp:2026.q1.14-lts
# ready when the log says "Server startup in" (30-90s); poll the log, don't sleep blindly
for h in localhost:<port> dev-tomcat.lfr-demo.se other.lfr-demo.se; do
  curl -s -H "Host: $h" -o /dev/null -w "$h -> %{http_code}\n" http://127.0.0.1:<port>/
done
```

Before any fix, `localhost` returns 200 and both tunnel hostnames return 500. Afterwards, the
covered hostname should return 200 and the control should still return 500.

Name the container uniquely and remove only that container when you are done. A port already in
use (say, `18080`) belongs to someone else's container, so pick another port and leave theirs alone.

## 2. The 500 body cannot tell you its cause

An unaccepted hostname fails in `ValidHostNameFilter`, which throws
`RuntimeException: Invalid host name <host>`. That text appears **only in Liferay's log**
(`docker logs <name> | grep 'Invalid host name'`). The response is `errors/code.jsp`, which is the
`<error-page>` for every uncaught exception, so the body is the same as for any other 500. It also
sends the browser to `http://localhost/web/guest`. Any detection, in the client or in a test, has
to be a **differential probe**: repeat the request with `Host` set to the local target and compare
the two status codes. Never match on the body.

## 3. What each fix covers

`PortalImpl.isValidPortalDomain` accepts a hostname if any one of these matches it:
`virtual.hosts.valid.hosts`, a `VirtualHost` row (instance or site), `web.server.host`, or the CDN
host.

| Fix | Covers | Measured |
|---|---|---|
| `virtual.hosts.valid.hosts` (container: `LIFERAY_VIRTUAL_PERIOD_HOSTS_PERIOD_VALID_PERIOD_HOSTS`) | Wildcards work, so `*.lfr-demo.se` covers any subdomain and survives a subdomain change. **One entry per domain**: `*.lfr-demo.online` is a separate entry | `.se` subdomains 200; `.online` control 500 |
| Instance Virtual Host (Control Panel → Instance Settings → Virtual Hosts) | **Exactly one hostname**, which has to be set again when the tunnel subdomain changes | that host 200; `other.lfr-demo.se` 500 |

## 4. Traps that produce a false result

- **Instance properties set through the environment can be ignored.** The image ships a
  pre-built Hypersonic database. `company.default.virtual.host.name` is read only when an instance
  is created, so setting `LIFERAY_COMPANY_PERIOD_DEFAULT_PERIOD_VIRTUAL_PERIOD_HOST_PERIOD_NAME`
  does nothing, and the test then "shows" that a virtual host does not fix the 500. Check what
  actually landed before you trust a run:
  `docker exec <name> grep -i 'INSERT INTO VIRTUALHOST' /opt/liferay/data/hypersonic/lportal.script`.
  Portal properties that are read at request time, such as `virtual.hosts.valid.hosts`, do apply.
- **Basic auth answers 403 everywhere, even for your own account.** With the default admin
  (`test@liferay.com` / `test`), `/o/headless-admin-user/v1.0/my-user-account` returned 403, and
  setting `passwords.default.policy.change.required=false` through the environment did not change
  that. Signing in through the login form shows why the account is blocked: it is sent to a
  forced password change. Basic auth was **not** retried after that change, so whether the change
  alone unblocks it is unverified. The browser route below is the one that is known to work. Use
  it with a cookie jar:
  1. `GET /c/portal/login`, and take the form `action` (it carries `p_auth`) and `formDate`.
  2. POST `_com_liferay_login_web_portlet_LoginPortlet_{formDate,login,password}`. The result is
     redirected to `/c/portal/update_password`.
  3. POST that page's hidden fields (`p_l_id`, `ticketId`, `ticketKey`) to reach the real form.
     Then POST the real form back with its own hidden fields plus `password1` and `password2`.
  4. Read `authToken: '…'` from a Control Panel page, and pass it as `p_auth` to JSON-WS calls.
     For example, `POST /api/jsonws/company/update-company` with
     `companyId, virtualHost, mx, maxUsers, active` sets the instance Virtual Host through the
     service the Instance Settings form uses.

  Use a throwaway password. It exists only in that container.

## 5. Reading Liferay's source

To find out why Liferay does something, read the source rather than infer it from behaviour. Use
a `liferay-portal` checkout if you have one, and `git grep` for the exception text. Cite the file
in the issue. Then confirm the behaviour in a container, because the source describes the current
branch and the image is a specific release.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-23* | *Last Reviewed: 2026-11-23*
