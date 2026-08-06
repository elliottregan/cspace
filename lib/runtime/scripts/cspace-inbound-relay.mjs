// cspace inbound loopback relay.
//
// Fallback for the PREROUTING DNAT in cspace-entrypoint.sh. That rule makes
// loopback-bound dev servers reachable from outside the microVM by rewriting
// inbound TCP destinations to 127.0.0.1, which is only legal when
// net.ipv4.conf.<if>.route_localnet=1. Apple Container 1.2.0 mounts /proc/sys
// read-only, so that sysctl write fails while the DNAT rule still installs —
// and the combination blackholes every inbound connection, including the
// supervisor's own control port.
// (cs-finding 2026-08-06-apple-container-1-2-mounts-proc-sys-read-only-breaking-inbound-dnat)
//
// This achieves the same outcome in userspace: listen on the external
// interface and forward to loopback. No sysctl, no netfilter, so a read-only
// /proc/sys is irrelevant. The entrypoint starts this only when it has
// confirmed route_localnet could NOT be set, so where the DNAT still works
// nothing changes.
//
// Discovery is dynamic and requires no declaration. That is deliberate:
// cspace parses container.ports and forwardPorts but never consumes them, and
// the ports a project actually binds (vite, nuxt, convex, bun, or a server an
// agent decided to start) are not predictable up front. Matching the DNAT's
// zero-config behavior is the point.
//
// Modeled on the CDP relay in cspace-entrypoint.sh: Node rather than socat
// (socat resolves its target once at startup), fresh dial per connection,
// supervised by an outer respawn loop.

import net from 'node:net';
import fs from 'node:fs';
import os from 'node:os';

const POLL_MS = Number(process.env.CSPACE_RELAY_POLL_MS || 2000);
const LISTEN_STATE = '0A'; // TCP_LISTEN in /proc/net/tcp

// Never relay: 53 is the in-sandbox dnsmasq, deliberately loopback-only so
// only this sandbox's resolver uses it.
const NEVER_RELAY = new Set([53]);

const relays = new Map(); // port -> net.Server
// Ports whose bind failed. A port can be permanently unbindable — e.g. a
// service holding BOTH 127.0.0.1:P and <external-ip>:P — and without this the
// reconcile would retry every tick forever, appending to an unrotated log on
// tmpfs (~43k lines/day per stuck port). Retry with backoff instead, so a
// transient collision still recovers but a permanent one goes quiet.
const failed = new Map(); // port -> { until: epochMs, delayMs }
const FAIL_BACKOFF_MIN_MS = 5_000;
const FAIL_BACKOFF_MAX_MS = 5 * 60_000;

// Bind the external (vmnet) address, NOT 0.0.0.0. The wildcard subsumes
// 127.0.0.1, so binding it on a port whose loopback address is already bound
// fails with EADDRINUSE — and that loopback listener is exactly the case this
// exists to serve. Binding the specific external address does not collide.
// CSPACE_RELAY_BIND is set by the entrypoint from the interface it already
// resolved via `ip route get`. Prefer it: enumerating interfaces here picks
// the first non-`lo` non-internal IPv4, which with docker-in-docker could be
// docker0 rather than eth0 — silently binding relays to a bridge address.
// The enumeration is only a fallback for running this script by hand.
function externalIPv4() {
  const injected = process.env.CSPACE_RELAY_BIND;
  if (injected) return injected;
  for (const [name, addrs] of Object.entries(os.networkInterfaces())) {
    if (name === 'lo' || name.startsWith('docker') || name.startsWith('br-')) continue;
    for (const a of addrs || []) {
      if (a.family === 'IPv4' && !a.internal) return a.address;
    }
  }
  return null;
}

// Parse /proc/net/tcp rather than shelling to `ss`: no external dependency,
// and it is the same data ss reads. local_address is HEX_IP:HEX_PORT with the
// IPv4 address in little-endian byte order (0100007F === 127.0.0.1).
function readListeners(path) {
  let raw;
  try {
    raw = fs.readFileSync(path, 'utf8');
  } catch {
    return [];
  }
  const out = [];
  for (const line of raw.split('\n').slice(1)) {
    const f = line.trim().split(/\s+/);
    if (f.length < 4 || f[3] !== LISTEN_STATE) continue;
    const [hexAddr, hexPort] = f[1].split(':');
    if (!hexAddr || !hexPort) continue;
    out.push({ addr: hexAddr.toUpperCase(), port: parseInt(hexPort, 16) });
  }
  return out;
}

// A port needs a relay when something listens on 127.0.0.1 and nothing already
// listens on a wildcard address. Relaying a port that is already bound to
// 0.0.0.0 (or [::]) would shadow a service that is correctly exposed already.
function portsNeedingRelay() {
  const loopback = new Set();
  const wildcard = new Set();
  for (const { addr, port } of readListeners('/proc/net/tcp')) {
    if (addr === '0100007F') loopback.add(port);
    else if (addr === '00000000') wildcard.add(port);
  }
  // [::] accepts IPv4 too unless bindv6only is set, so treat it as wildcard.
  // ::1 is IPv6 loopback; we do not relay IPv6 , only avoid shadowing.
  for (const { addr, port } of readListeners('/proc/net/tcp6')) {
    if (/^0{32}$/.test(addr)) wildcard.add(port);
  }
  const need = new Set();
  for (const p of loopback) {
    if (!wildcard.has(p) && !NEVER_RELAY.has(p)) need.add(p);
  }
  return need;
}

function startRelay(port, bindAddr) {
  const server = net.createServer((client) => {
    const up = net.connect({ host: '127.0.0.1', port });
    client.pipe(up);
    up.pipe(client);
    const done = () => { client.destroy(); up.destroy(); };
    client.on('error', done);
    up.on('error', done);
  });
  server.on('error', (err) => {
    // Most likely EADDRINUSE: something already holds bindAddr:port. Back off
    // rather than retrying every tick — see `failed` above.
    const prev = failed.get(port);
    const delayMs = prev ? Math.min(prev.delayMs * 2, FAIL_BACKOFF_MAX_MS) : FAIL_BACKOFF_MIN_MS;
    failed.set(port, { until: Date.now() + delayMs, delayMs });
    if (!prev) console.error(`relay ${port}: ${err.code || err.message} (backing off)`);
    relays.delete(port);
    try { server.close(); } catch { /* already closing */ }
  });
  server.listen(port, bindAddr, () => {
    failed.delete(port);
    console.log(`relay: ${bindAddr}:${port} -> 127.0.0.1:${port}`);
  });
  relays.set(port, server);
}

function reconcile() {
  const bindAddr = externalIPv4();
  if (!bindAddr) {
    console.error('no external IPv4 interface; nothing to relay onto');
    return;
  }
  const need = portsNeedingRelay();
  const now = Date.now();
  for (const port of need) {
    if (relays.has(port)) continue;
    const f = failed.get(port);
    if (f && now < f.until) continue; // still backing off
    startRelay(port, bindAddr);
  }
  for (const [port, server] of relays) {
    if (!need.has(port)) {
      console.log(`relay: stopping :${port} (loopback listener gone)`);
      try { server.close(); } catch { /* ignore */ }
      relays.delete(port);
    }
  }
  // Forget backoff state for ports that no longer want a relay, so a later
  // reappearance starts clean rather than inheriting a 5-minute delay.
  for (const port of failed.keys()) {
    if (!need.has(port)) failed.delete(port);
  }
}

console.log(`cspace-inbound-relay: polling every ${POLL_MS}ms`);
reconcile();
setInterval(reconcile, POLL_MS);
