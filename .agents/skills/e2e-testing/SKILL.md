---
name: e2e-testing
description: Read before writing or running Playwright/e2e tests for either portal, and before trusting a mutation test against the containerised stack. Covers the non-admin fixture, the rebuild race that makes a mutation test lie, and why absence-only assertions pass on a blank page.
---

# End-to-end testing (Playwright, both portals)

The Go suite is covered by [`edr-constraints`](../edr-constraints/SKILL.md) — `make test` only,
never bare `go test`. This file is about the browser tests in `tests/e2e/ui/`, which run against a
Docker Compose stack and have their own ways of quietly lying to you.

## 1. Every spec used to sign in as an admin

`admin@lfr-demo.local` was the only account any spec used, so **the non-admin view of either
portal had no coverage at all**. That is not a hypothetical gap: it is how #1512 shipped, where
Portal V2's analytics page rendered a personal section perfectly well and was simply unreachable
for the people it was written for. No test could have noticed.

Use `tests/e2e/ui/tests/utils/nonadmin.ts`:

```ts
import { createApprovedUser } from './utils/nonadmin';
const email = `nonadmin-${Date.now()}@lfr-demo.local`;  // unique: re-runs hit a warm database
await createApprovedUser(email);
```

It goes through the real registration path rather than inserting a row, because approval issues
the PAT and sets fields the portal reads — a user conjured straight into the database is subtly
unlike a real one, and a test passing against a state that never occurs is worse than no test.

**The flow has a step that is not obvious from the endpoint names.** `POST /api/register-request`
does *not* notify the admin. It emails the **applicant** a verification link, and the admin hears
nothing until that link is used:

```
register-request  ->  mail to APPLICANT   (setup?token=…)
complete-setup    ->  mail to ADMIN       (admin/approve?email=…&token=…)
admin/approve     ->  account usable, magic-link works
```

Assuming otherwise makes the helper wait for mail that is never going to arrive. Note also that
the admin receives **two** mails per registration and only one carries an approval link, so match
on the applicant's address as well as on `token=`.

## 2. A mutation test against this stack will lie to you twice

Mutation testing — break the fix, confirm the test fails — is the only way to know a browser test
is testing anything. Two traps, both of which produced a confident wrong answer in #1512:

**The UI is compiled into the image.** `cmd/lfr-tunneld/Dockerfile` has a `ui-builder` stage that
runs `pnpm run build` from `COPY ui/`. Editing `ui/src/**` changes nothing until the image is
rebuilt. A mutation applied to the source and tested without `--build` measures the previous
build.

**`docker compose up -d --build` returns before the new container serves.** A Playwright run
started immediately after can still hit the old container. This is the one that cost the most
time: the mutation looked like it had failed to break anything, when the test simply never saw it.

So **verify the artefact under test actually contains the mutation** before believing any result:

```bash
b=$(curl -s http://localhost:8000/portalv2/ | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1)
echo "$b"                                    # hash MUST change when the source changes
curl -s "http://localhost:8000/portalv2/assets/$b" | grep -oE 'path:`/[a-z/-]*analytics`'
```

Note the backticks: the bundler emits route paths as template literals, so grepping for
`"/analytics"` finds nothing and reads as "the route is gone" when it is present.

## 3. Assertions of absence pass on a blank page

Mutation testing found two of four tests in #1512 were vacuous. With the route removed the page
rendered nothing, and both still passed:

- *"sees no admin-only analytics"* — asserts only that admin headings are **absent**, which an
  empty page satisfies perfectly.
- *"the sidebar offers analytics"* — asserted the URL changed after a click. It does, with or
  without a route to match it. Arriving at a URL is not arriving at a page.

**Anchor every absence check on something that must be present.** One positive assertion first,
then the absences. Without it a test proves the page did not render rather than that it rendered
correctly, and reports the two identically.

## 4. The database is shared, so a fixture is a neighbour

Every spec runs against one gateway and one database, in file order, with a single worker. A
fixture that creates data does not only affect its own spec.

`portal_v2_table_scroll` asserts the Admin Users table fits a 1280px viewport. The non-admin
fixture originally used `nonadmin-<13-digit-timestamp>@lfr-demo.local` -- twice the width of
`admin@lfr-demo.local` -- which widened the email column for the spec that runs after it
alphabetically, and broke it **in CI only**: the Linux font stack is wider than macOS's, so it
passed locally every time.

So when adding a fixture:

- **Keep created data comparable in size to what is already there.** A long identifier is not
  free when another spec measures a layout.
- **Assume your spec runs before every spec whose filename sorts after it.** That is the order
  they run in.
- **A local pass is not evidence for CI** where layout is concerned. Font metrics differ, and a
  table that fits here can overflow there.

Note that `docker compose up -d --build` recreates the containers and takes the database with
them, so local runs often start clean while CI accumulates state across the whole suite. That
difference hides exactly this class of bug.

## 5. Running them

```bash
cd tests/e2e && docker compose up -d --build          # stack; wait for /api/version to answer
cd tests/e2e/ui && npx playwright test <spec-name>    # one spec, not the whole suite
```

`tests/e2e/run-ui.sh` does the full sequence including a client tunnel, which most specs do not
need. Docker is outside the EDR constraints that govern host binaries, so the containerised client
is fine to run; the host `lfr-tunnel` binary is not.

---
*Last Updated: 2026-08-27* | *Last Reviewed: 2026-08-27*
