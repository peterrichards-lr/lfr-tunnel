import { test, expect, APIRequestContext } from '@playwright/test';
import { exec } from 'child_process';
import { promisify } from 'util';
import { adminRequestContext } from './utils/nonadmin';
import { openWebSocket, OPCODE_TEXT } from './utils/wsprobe';

const execAsync = promisify(exec);

/**
 * A visitor's WebSocket, through a real tunnel, end to end (#2249, proposal item 1).
 *
 * This is the shape the suite has never driven. 74 specs, 1 of them carrying traffic through a
 * tunnel at all, and that one an ordinary GET -- so when the gateway started answering 502 to
 * every upgraded request it shipped on 2026-06-10 and was found on 2026-09-22 by someone reading
 * the code (#2179), with the sibling defect in the client's own interceptor found the same way
 * the next day (#2213). Both were fixed with unit tests, around `trackingTransport`
 * (pkg/server/proxy_upgrade_byte_accounting_test.go) and `interceptorTransport`
 * (pkg/client/interceptor_upgrade_test.go). Each proves its own half in isolation. Nothing put a
 * socket in at one end of the assembled data plane and read it out of the other, which is the
 * only level at which "a visitor's WebSocket works" is a statement about the product.
 *
 * The path exercised here, in full:
 *
 *   probe -> nginx (:8000) -> lfr-tunneld's ReverseProxy + trackingTransport
 *         -> the tunnel -> the client's InterceptorEngine + interceptorTransport
 *         -> ws-echo
 *
 * TWO CONTROLS, because "the WebSocket failed" is shared by every failure mode in that chain and
 * an assertion that several causes satisfy names none of them (github-workflow SKILL 5c). The
 * first dials ws-echo directly, so a broken fixture cannot be read as a broken data plane. The
 * second sends an ordinary GET through the tunnel, so "the tunnel is down" cannot be read as
 * "the upgrade was refused" -- which matters because #2179 was precisely the second with the
 * first still working.
 *
 * No browser is used, and none is requested, so no browser is launched. A tunnel is addressed by
 * Host header and no browser API can set one; `page.request` cannot open a WebSocket either.
 * What the proxies do with an upgrade is decided entirely by the HTTP handshake -- ReverseProxy
 * hijacks on a 101 and shuttles raw bytes -- so a raw socket exercises the same code with less
 * that can go wrong. See tests/e2e/ui/tests/utils/wsprobe.ts.
 */

const PROJECT = process.env.E2E_PROJECT_NAME || 'e2e';
const CLIENT_CONTAINER = `${PROJECT}-lfr-tunnel-1`;
const PROXY_PORT = 8000;
/** Published by the ws-echo service in tests/e2e/docker-compose.yml, for the direct CONTROL. */
const WS_ECHO_PORT = Number(process.env.E2E_WS_ECHO_PORT || 8090);
/** The port ws-echo listens on inside the compose network -- what the client is pointed at. */
const WS_ECHO_TARGET_PORT = 8090;
/** Fixed rather than generated; see the note beside the client invocation in beforeAll. */
const SUBDOMAIN = 'wsecho2249';

interface Lease {
  subdomain_prefix: string;
  full_host: string;
  status: string;
  bytes_in: number;
  bytes_out: number;
}

let ctx: APIRequestContext;
let tokenID: string | number | undefined;
let subdomain = '';
let fullHost = '';

async function leases(): Promise<Lease[]> {
  const res = await ctx.get(`http://localhost:${PROXY_PORT}/api/admin/leases`);
  if (!res.ok()) {
    throw new Error(
      `GET /api/admin/leases answered ${res.status()} ${await res.text()}`,
    );
  }
  // `?? []`, because the endpoint marshals a nil slice and answers a bare `null` when no tunnel
  // is registered. Without it the first poll throws a TypeError out of the fixture and the
  // failure is attributed to whichever test happened to trigger beforeAll.
  return ((await res.json()) as Lease[] | null) ?? [];
}

async function ourLease(): Promise<Lease> {
  const found = (await leases()).find((l) => l.subdomain_prefix === subdomain);
  if (!found) throw new Error(`no lease for ${subdomain}`);
  return found;
}

test.describe('A visitor WebSocket through a real tunnel', () => {
  test.describe.configure({ timeout: 90_000 });

  test.beforeAll(async () => {
    ctx = await adminRequestContext('driving a data-plane tunnel');

    // A token, through the real endpoint. analytics.spec.ts scrapes one out of the portal UI;
    // this spec's subject is below the portal entirely, and a browser round trip to obtain a
    // credential would be wall-clock spent on something no assertion here is about.
    const created = await ctx.post(
      `http://localhost:${PROXY_PORT}/api/tokens`,
      {
        data: { name: 'ws data-plane e2e', expires_in_days: 1 },
      },
    );
    if (!created.ok()) {
      throw new Error(
        `creating the client token failed: ${created.status()} ${await created.text()}`,
      );
    }
    const token = await created.json();
    tokenID = token.id;
    expect(
      token.raw_token,
      'the API must return the raw token exactly once',
    ).toBeTruthy();

    // An explicit subdomain, for two reasons. The client refuses to start a second BACKGROUND
    // tunnel for a subdomain it is already running, and analytics.spec.ts leaves one up for the
    // rest of the suite -- without this, `./lfr-tunnel -background` here exits with "a
    // background tunnel for subdomain X is already running". And a fixed name means this spec
    // creates exactly one auto-reservation row however often it runs, rather than one per run
    // accumulating in the shared database (e2e-testing SKILL 4).
    //
    // Stopped first, unconditionally, so a previous run that died before its afterAll does not
    // make this one fail for a reason that has nothing to do with the subject.
    await execAsync(
      `docker exec ${CLIENT_CONTAINER} ./lfr-tunnel -stop -subdomain ${SUBDOMAIN}`,
    ).catch(() => undefined);

    // -target-host points this tunnel at ws-echo instead of the mock-target every other spec
    // uses, so the origin can actually be upgraded.
    const { stdout, stderr } = await execAsync(
      `docker exec ${CLIENT_CONTAINER} ./lfr-tunnel -token ${token.raw_token} ` +
        `-server http://tunnel.lfr-demo.local -ports ${WS_ECHO_TARGET_PORT} ` +
        `-target-host ws-echo -subdomain ${SUBDOMAIN} -background`,
    );
    if (!`${stdout}\n${stderr}`.includes(`subdomain '${SUBDOMAIN}'`)) {
      throw new Error(
        `the client did not report starting ${SUBDOMAIN}:\n${stdout}\n${stderr}`,
      );
    }
    subdomain = SUBDOMAIN;

    // Readiness is "the gateway holds a lease and the origin answers through it", NOT the
    // lease's `status` field. The heartbeat that maintains that field dials the target host on
    // the INTERCEPTOR's local port, so it reports `down` within one 5s tick for every tunnel
    // whose target host is not the default -- a healthy tunnel included (#2270, found by this
    // spec). Asserting `up` here would make this file fail for a defect it is not about;
    // asserting the traffic is both the readiness signal and the thing that matters. Restore
    // the status assertion when #2270 lands.
    await expect
      .poll(
        async () =>
          (await leases().catch(() => [])).some(
            (l) => l.subdomain_prefix === subdomain,
          ),
        {
          timeout: 30_000,
          message: `the gateway never reported a lease for ${subdomain}`,
        },
      )
      .toBe(true);
    fullHost = (await ourLease()).full_host;
  });

  test.afterAll(async () => {
    // Removed rather than left running: the stack is shared by every spec in the run and, with
    // E2E_KEEP_STACK, by the next one too (e2e-testing SKILL 4).
    if (subdomain) {
      await execAsync(
        `docker exec ${CLIENT_CONTAINER} ./lfr-tunnel -stop -subdomain ${subdomain}`,
      ).catch((err) => console.warn(`stopping the tunnel: ${err}`));
    }
    if (ctx) {
      if (tokenID !== undefined) {
        await ctx
          .delete(`http://localhost:${PROXY_PORT}/api/tokens/${tokenID}`)
          .catch((err) => console.warn(`revoking the client token: ${err}`));
      }
      await ctx.dispose();
    }
  });

  test('CONTROL: ws-echo upgrades and echoes when dialled directly', async () => {
    // If this fails the finding is the fixture, not the data plane. Nothing below can be read
    // as evidence about the tunnel while this is red.
    const ws = await openWebSocket({
      port: WS_ECHO_PORT,
      hostHeader: `localhost:${WS_ECHO_PORT}`,
      path: '/echo',
    });
    try {
      expect(ws.statusLine).toContain('101');
      expect(
        ws.upgraded,
        'the handshake must carry a Sec-WebSocket-Accept derived from the key that was sent',
      ).toBe(true);
      ws.send('direct');
      const frame = await ws.receive(5000);
      expect(frame.opcode).toBe(OPCODE_TEXT);
      expect(frame.payload.toString('utf8')).toBe('direct');
    } finally {
      ws.close();
    }
  });

  test('CONTROL: ordinary traffic reaches the origin through the tunnel', async () => {
    // The tunnel carries a plain GET. #2179 left this working and answered 502 only to an
    // upgrade, so without this a red subject test cannot be told apart from a tunnel that never
    // came up.
    //
    // Polled rather than asserted once, and placed before the subject rather than in beforeAll,
    // so it is this file's readiness gate as well as its control. A poll in beforeAll would have
    // made the assertion here true by construction -- documentation, not a guard.
    await expect
      .poll(
        async () =>
          (
            await ctx.get(`http://localhost:${PROXY_PORT}/`, {
              headers: { Host: fullHost },
            })
          ).status(),
        { timeout: 30_000, message: `nothing answered through ${fullHost}` },
      )
      .toBe(200);

    const res = await ctx.get(`http://localhost:${PROXY_PORT}/`, {
      headers: { Host: fullHost },
    });
    // The body, not only the status: a 200 from the gateway's own error page or from a
    // different tunnel would satisfy a status check on its own.
    expect(await res.text()).toContain('ws-echo');
  });

  test('a visitor WebSocket is upgraded and carries frames both ways', async () => {
    const ws = await openWebSocket({
      port: PROXY_PORT,
      hostHeader: fullHost,
      path: '/echo',
    });
    try {
      // The status line, not merely "it did not throw". #2179 answered
      // `502 Bad Gateway` here and #2213 answered nothing for five seconds; both are visible
      // in this assertion and in the timeout the probe names, and neither is the same failure
      // as a connection that was refused.
      expect(
        ws.statusLine,
        'the gateway must answer the upgrade with 101, not a 502 or a plain 200',
      ).toContain('101 Switching Protocols');
      expect(ws.upgraded).toBe(true);

      // Both directions after the handshake, which is the half a status code cannot show: the
      // proxies hijack the connection on a 101 and copy raw bytes, so a 101 that is not
      // followed by a working pipe is still a broken WebSocket for the visitor.
      const marker = `visitor-${Date.now()}`;
      ws.send(marker);
      const first = await ws.receive(10_000);
      expect(first.opcode).toBe(OPCODE_TEXT);
      expect(first.payload.toString('utf8')).toBe(marker);

      // A second exchange on the same socket, because a one-shot relay that closes after the
      // first frame would satisfy everything above.
      ws.send(`${marker}-again`);
      const second = await ws.receive(10_000);
      expect(second.payload.toString('utf8')).toBe(`${marker}-again`);
    } finally {
      ws.close();
    }
  });

  test("an upgraded connection's bytes are counted against the lease", async () => {
    // The direction the issue was originally FILED in: #2179 was reported as bytes going
    // uncounted, and the counting was never reached because the upgrade was not. It is a
    // separate assertion because a 101 followed by a working pipe can be delivered by a
    // transport that has stopped counting -- `trackingUpgradedConn` is the only place an
    // upgraded session's traffic is visible at all (pkg/server/proxy.go).
    const ws = await openWebSocket({
      port: PROXY_PORT,
      hostHeader: fullHost,
      path: '/echo',
    });
    try {
      expect(ws.upgraded).toBe(true);

      // Baseline taken AFTER the handshake, deliberately. The request line and headers of the
      // upgrade are themselves counted, and they are bigger than the payload below -- so a
      // baseline taken before the handshake would let the handshake alone satisfy this test
      // while the frames went uncounted.
      await expect
        .poll(async () => (await ourLease()).bytes_in, { timeout: 10_000 })
        .toBeGreaterThan(0);
      const before = await ourLease();

      const payload = 'x'.repeat(512);
      ws.send(payload);
      const echoed = await ws.receive(10_000);
      expect(echoed.payload.toString('utf8')).toBe(payload);

      await expect
        .poll(async () => (await ourLease()).bytes_in - before.bytes_in, {
          timeout: 10_000,
          message: 'the frames a visitor sent must be counted as Data In',
        })
        .toBeGreaterThanOrEqual(payload.length);
      await expect
        .poll(async () => (await ourLease()).bytes_out - before.bytes_out, {
          timeout: 10_000,
          message:
            'the frames the origin sent back must be counted as Data Out',
        })
        .toBeGreaterThanOrEqual(payload.length);
    } finally {
      ws.close();
    }
  });
});
