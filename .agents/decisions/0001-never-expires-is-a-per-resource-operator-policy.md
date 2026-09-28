STATUS: Accepted 2026-09-26

# 0001. Permanence is a per-resource operator policy, not a role capability

## Decision

Whether a token, subdomain or custom domain may be granted permanently is decided by server
config, one policy per resource, defaulting to "not offered at all". In the owner's words:

> "allowing never expires on tokens, subdomains and custom domains need to be configurable via the
> server config. They need to be separated so they can be set independently. The default should be
> that the option for never expires is not available at all, then there should be two states, admin
> approval and no approval required. This should apply to both versions of the portal and the
> client."

The Liferay gateway's own posture is `tokens: allowed`, `subdomains: approval`,
`custom_domains: allowed` (#2264).

## Cost

`tokens: allowed` is a **widening**: a non-expiring PAT becomes available to every role, where it
had been admin/owner-only. That is deliberate, and it is recorded here because the config file
cannot say why. The default tightens in the other direction, so a gateway that upgrades without
setting the key stops offering permanence it previously offered — accepted, because a policy
governs new grants only and nothing existing is retroactively expired.

## Enforced by

`pkg/config/never_expires_test.go` (the default is asserted, not assumed),
five server-side gates added in #2265 / PR #2274, and `resources/server/server-config.example.yaml`.
Operator guidance is `docs/server/setup_guide.md` §8.13 (#2268 / PR #2282).

## Revisit when

A gateway operator needs a fourth state, or when a custom domain needs its own role-level expiry
setting — #2276 deliberately left no custom-domain equivalent of `subdomain_expiry_days`, so a
custom domain that may not be permanent takes the plain default rather than borrowing the subdomain
role setting.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
