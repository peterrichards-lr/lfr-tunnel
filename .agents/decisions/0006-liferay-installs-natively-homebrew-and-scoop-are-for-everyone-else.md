STATUS: Accepted 2026-11-18

# 0006. On Liferay's EDR-managed machines the native installer is the only install route; Homebrew and Scoop stay for everyone else

## Decision

The owner, on #2506: "for Liferay we drop Homebrew/Scoop but this is still a useful delivery
mechanism for other people so I would like to keep it."

- **Liferay (EDR-managed):** the native installers only, putting the client at
  `<home>/liferay/lfr-tunnel/lfr-tunnel(.exe)`. That is the one path per OS the EDR exclusions
  are requested for. Homebrew and Scoop installs are unsupported there, and no exclusion is
  requested for their paths.
- **Everyone else:** the Homebrew tap and Scoop bucket stay published, from the same release, as a
  supported route on machines without that EDR.

## Cost

Two families of install path keep existing, so the docs have to say which one applies where. An SE
who installs from Homebrew out of habit gets a client the EDR does not exclude. The tap and bucket
also stay as release machinery that Liferay itself does not use.

## Enforced by

`docs/liferay-se-guide.md` tells SEs not to install from Homebrew or Scoop. `docs/infosec.md` §3 is
the exclusion list, and #2507 removes its Homebrew rule and makes it the single table of paths, with
a check that the installers and docs agree with it. The tap and bucket are published by
`scripts/update-tap-bucket.sh` from `.github/workflows/release.yml`; nothing stops a Liferay user
running them, so the EDR-managed half is otherwise unenforced by design.

## Revisit when

Liferay's EDR exclusions can name a package manager's path (or a signer) as reliably as the native
path, or the tap and bucket stop being used outside Liferay.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-18* | *Last Reviewed: 2026-11-18*
