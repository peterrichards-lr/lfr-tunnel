STATUS: Accepted 2026-08-11

# 0003. Release distribution is two-tier, and the public tier is unsigned on purpose

## Decision

Binaries published to GitHub Releases, Homebrew and Scoop are **unsigned by deliberate choice**.
The signed artefacts are the ones the central server distributes itself, together with its install
scripts — that path is the Liferay-specific EDR-verification route and is not pushed onto other
users.

## Cost

Anyone installing from the public tier gets no signature to verify, and the project cannot point at
one. Accepted: signing the public tier would mean either publishing Liferay's signing identity or
maintaining a second one for a community audience that does not need EDR attestation.

## Enforced by

`unenforced by design` for the public tier. The signed tier is real and checked:
`.agents/skills/lfr-tunnel-ops/SKILL.md` §4 (signing) and §5 (deploying client binaries), and
`make verify-release`, which re-checks that a published release carries every built artefact.

Note that `minisign` covers only self-upgrade integrity. macOS codesign, Windows Authenticode and
Linux GPG are separate and still required — see §4 of the ops skill.

## Revisit when

A distribution channel starts requiring a signature (a Homebrew or Scoop policy change), or a
non-Liferay user has a genuine need to verify a public-tier download.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
