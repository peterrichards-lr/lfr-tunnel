import { createHash, randomBytes } from 'crypto';
import { connect, Socket } from 'net';

/**
 * A WebSocket client that can choose its own Host header (#2249).
 *
 * The stack is reached at localhost:8000 and a tunnel is addressed by Host, which is how every
 * spec here drives tunnelled traffic (`page.request.get('http://localhost:8000', { headers: {
 * Host } })` in analytics.spec.ts). No browser API lets a page choose that header, and Playwright
 * has no WebSocket client of its own, so the handshake is written onto a raw socket here.
 *
 * That is also what makes the probe useful as evidence. The proxies under test do not parse
 * WebSocket frames -- httputil.ReverseProxy sees a 101 and hijacks the connection -- so what is
 * being exercised is the ordinary HTTP upgrade, byte for byte, with nothing about it supplied by
 * a library that might paper over a malformed response.
 *
 * `open` resolves for a NON-101 answer too, with the status line intact. That is deliberate:
 * #2179 answered 502 to exactly this request while ordinary traffic kept working, and a probe
 * that threw on it would report a fixture error where the finding is the status code.
 */

/** RFC 6455 section 1.3. */
const WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

export const OPCODE_TEXT = 0x1;

export interface WsFrame {
  opcode: number;
  payload: Buffer;
}

export interface WsProbe {
  /** e.g. `HTTP/1.1 101 Switching Protocols`. Present whatever the server answered. */
  statusLine: string;
  /** Lower-cased header names from the handshake response. */
  headers: Record<string, string>;
  /** True only for a 101 whose Sec-WebSocket-Accept matches the key that was sent. */
  upgraded: boolean;
  send(text: string): void;
  /** The next complete frame, or a rejection naming the wait that failed. */
  receive(timeoutMs?: number): Promise<WsFrame>;
  close(): void;
}

function expectedAccept(key: string): string {
  return createHash('sha1')
    .update(key + WS_GUID)
    .digest('base64');
}

/** One masked client frame. A client MUST mask (RFC 6455 section 5.3). */
function encodeMaskedFrame(opcode: number, payload: Buffer): Buffer {
  const mask = randomBytes(4);
  const masked = Buffer.alloc(payload.length);
  for (let i = 0; i < payload.length; i++) {
    masked[i] = payload[i] ^ mask[i % 4];
  }
  let header: Buffer;
  if (payload.length < 126) {
    header = Buffer.from([0x80 | opcode, 0x80 | payload.length]);
  } else if (payload.length <= 0xffff) {
    header = Buffer.from([
      0x80 | opcode,
      0x80 | 126,
      payload.length >> 8,
      payload.length & 0xff,
    ]);
  } else {
    throw new Error('wsprobe sends short frames only');
  }
  return Buffer.concat([header, mask, masked]);
}

/**
 * Pulls one frame off the front of `buf`, or returns null when more bytes are needed.
 *
 * A server never masks, but the mask bit is honoured anyway so that a peer which does is
 * decoded rather than silently read as garbage that happens not to match.
 */
function decodeFrame(buf: Buffer): { frame: WsFrame; rest: Buffer } | null {
  if (buf.length < 2) return null;
  const opcode = buf[0] & 0x0f;
  const masked = (buf[1] & 0x80) !== 0;
  let length = buf[1] & 0x7f;
  let offset = 2;
  if (length === 126) {
    if (buf.length < offset + 2) return null;
    length = buf.readUInt16BE(offset);
    offset += 2;
  } else if (length === 127) {
    if (buf.length < offset + 8) return null;
    const big = buf.readBigUInt64BE(offset);
    if (big > BigInt(1 << 20))
      throw new Error('wsprobe received an oversized frame');
    length = Number(big);
    offset += 8;
  }
  let mask: Buffer | null = null;
  if (masked) {
    if (buf.length < offset + 4) return null;
    mask = buf.subarray(offset, offset + 4);
    offset += 4;
  }
  if (buf.length < offset + length) return null;
  const payload = Buffer.from(buf.subarray(offset, offset + length));
  if (mask) {
    for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
  }
  return { frame: { opcode, payload }, rest: buf.subarray(offset + length) };
}

export interface WsProbeOptions {
  /** Where to dial. The tunnel is reached through the stack's nginx, on localhost. */
  port: number;
  /** The Host header, which is what selects the tunnel. Not necessarily where we dialled. */
  hostHeader: string;
  path: string;
  host?: string;
  /** How long to wait for the handshake response. A hang is the #2213 symptom. */
  timeoutMs?: number;
}

export async function openWebSocket(options: WsProbeOptions): Promise<WsProbe> {
  const { port, hostHeader, path } = options;
  const host = options.host ?? '127.0.0.1';
  const timeoutMs = options.timeoutMs ?? 10000;

  const key = randomBytes(16).toString('base64');
  const socket: Socket = connect({ host, port });
  socket.setNoDelay(true);

  let buffered = Buffer.alloc(0);
  const pending: WsFrame[] = [];
  let waiter: ((f: WsFrame) => void) | null = null;
  let socketError: Error | null = null;
  let ended = false;

  const drainFrames = () => {
    for (;;) {
      const next = decodeFrame(buffered);
      if (!next) return;
      buffered = next.rest;
      if (waiter) {
        const resolve = waiter;
        waiter = null;
        resolve(next.frame);
      } else {
        pending.push(next.frame);
      }
    }
  };

  const handshake = await new Promise<{
    statusLine: string;
    headers: Record<string, string>;
  }>((resolve, reject) => {
    const timer = setTimeout(() => {
      // Named, because a hang and a refusal are different findings and #2213 was the hang.
      reject(
        new Error(
          `no handshake response from ${host}:${port} (Host: ${hostHeader}) within ${timeoutMs}ms`,
        ),
      );
    }, timeoutMs);

    const onHeaders = (data: Buffer) => {
      buffered = Buffer.concat([buffered, data]);
      const end = buffered.indexOf('\r\n\r\n');
      if (end < 0) return;
      const raw = buffered.subarray(0, end).toString('latin1');
      buffered = buffered.subarray(end + 4);
      socket.off('data', onHeaders);
      socket.on('data', (more: Buffer) => {
        buffered = Buffer.concat([buffered, more]);
        drainFrames();
      });
      clearTimeout(timer);
      const lines = raw.split('\r\n');
      const headers: Record<string, string> = {};
      for (const line of lines.slice(1)) {
        const colon = line.indexOf(':');
        if (colon > 0) {
          headers[line.slice(0, colon).trim().toLowerCase()] = line
            .slice(colon + 1)
            .trim();
        }
      }
      resolve({ statusLine: lines[0], headers });
      drainFrames();
    };

    socket.on('error', (err) => {
      socketError = err;
      clearTimeout(timer);
      reject(
        new Error(
          `connecting to ${host}:${port} (Host: ${hostHeader}): ${err.message}`,
        ),
      );
    });
    socket.on('close', () => {
      ended = true;
    });
    socket.on('connect', () => {
      socket.write(
        `GET ${path} HTTP/1.1\r\n` +
          `Host: ${hostHeader}\r\n` +
          'Upgrade: websocket\r\n' +
          'Connection: Upgrade\r\n' +
          `Sec-WebSocket-Key: ${key}\r\n` +
          'Sec-WebSocket-Version: 13\r\n\r\n',
      );
    });
    socket.on('data', onHeaders);
  });

  const upgraded =
    /^HTTP\/1\.1 101 /.test(handshake.statusLine) &&
    handshake.headers['sec-websocket-accept'] === expectedAccept(key);

  return {
    statusLine: handshake.statusLine,
    headers: handshake.headers,
    upgraded,
    send(text: string) {
      socket.write(encodeMaskedFrame(OPCODE_TEXT, Buffer.from(text, 'utf8')));
    },
    receive(waitMs = 10000): Promise<WsFrame> {
      const queued = pending.shift();
      if (queued) return Promise.resolve(queued);
      if (socketError) return Promise.reject(socketError);
      return new Promise<WsFrame>((resolve, reject) => {
        const timer = setTimeout(() => {
          waiter = null;
          reject(
            new Error(
              ended
                ? `the connection closed before a frame arrived (Host: ${hostHeader})`
                : `no frame within ${waitMs}ms (Host: ${hostHeader})`,
            ),
          );
        }, waitMs);
        waiter = (f) => {
          clearTimeout(timer);
          resolve(f);
        };
      });
    },
    close() {
      socket.destroy();
    },
  };
}
