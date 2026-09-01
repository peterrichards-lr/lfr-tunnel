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

## 2b. Five more ways a mutation test lies, all seen in one day

Section 2 covers the stale container. These are different, and each one produced a confident
wrong answer -- either "the test caught it" when nothing was mutated, or "the test is vacuous"
when the mutation never reached the build. All five were found on 2026-09-01 while fixing #1617,
#1622, #1640, #1647, #1648 and #1651.

**A mutation that does not compile is not a mutation.** Deleting a call often orphans its
variable or import, and `pnpm run build` fails on the unused symbol. The Docker build then fails
too, the old image keeps serving, and the tests pass -- against unmutated code. Seen three times
in a day: removing `formatDate(value)`, removing an `isReachable()` filter, and dropping a
`padNodeDaily` call site. Keep the symbol referenced:

```ts
setGoTo(GO_TO.filter((s) => (s.path ? isReachable(s.path) : true)));
// mutant, still compiles:
void isReachable;
setGoTo(GO_TO);
```

**Always check the build's exit code.** `pnpm run build > /dev/null 2>&1` hides the failure that
causes the trap above. If the build exits non-zero the run proves nothing.

**A marker in a comment does not survive the bundler.** `/* MUTANTMARKER */` is stripped from
both JS and CSS output, so grepping the served asset for it always returns 0 -- which reads as
"the mutation is not live" no matter what is true. Put the marker in a string literal, a class
name or an identifier.

**A marker that occurs anyway proves nothing.** Grepping the bundle for `admin/analytics` or
`Expires` "confirmed" mutations that had never applied, because those strings appear elsewhere in
the app. Pick something that exists only in the mutant.

**A changed asset hash is not proof the intended thing changed.** It proves *something* did. A
`str.replace(old, new, 1)` targeting a CSS declaration hit the **first** match in the file --
`.px-md`, hundreds of lines above `.pagination-row` -- so the hash moved, the mutation looked
live, and the real rule was untouched. Assert on the rule itself:

```bash
curl -s "http://localhost:8000/portalv2/assets/$css" | grep -o '\.pagination-row{[^}]*}'
```

**So: make every mutation self-identifying, and assert it in the served artefact.** Write the
edit with an `assert` that the anchor was found *and* that the file changed, then grep the served
bundle for something unique to the mutant. Anything less and a green run means nothing.

```python
old = "  padding-left: var(--spacing-md);"
assert old in s, "anchor not found -- the mutation would silently no-op"
s2 = s.replace(old, "", 1)
assert s2 != s
```

Scoping matters as much as the assert: target the specific rule or function, not the first
textual match in the file.

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

**Delete what you create.** This is the actual fix, and sizing is not a substitute for it.
`tests/e2e/ui/tests/utils/nonadmin.ts` exports `deleteUser` for this:

```ts
const nonAdminEmail = `nb${Date.now().toString().slice(-6)}@lfr-demo.local`;
test.afterAll(async () => {
  await deleteUser(nonAdminEmail);
});
```

Three specs were written on 2026-08-29 using a deliberately short local part *and no cleanup*,
on the reading that keeping the row narrow was enough. All three failed CI on
`portal_v2_table_scroll` anyway: `nb123456@lfr-demo.local` is still three characters wider than
`admin@lfr-demo.local`, and the rows accumulate across runs. Shortening reduces the width, it
does not remove it.

So when adding a fixture:

- **Remove it in `afterAll`.** Not optional, even for a read-only-looking test.
- **Keep created data comparable in size to what is already there** as well. A long identifier
  is not free when another spec measures a layout, and cleanup does not help the run it is in.
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
*Last Updated: 2026-09-01* | *Last Reviewed: 2026-09-01*
