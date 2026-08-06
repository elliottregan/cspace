---
title: Apple Container 1.2.0 mounts /proc/sys read-only, so the entrypoint's inbound DNAT silently blackholes all inbound TCP
date: 2026-08-06
kind: finding
status: open
category: bug
tags: apple-container, networking, entrypoint, dnat, cmd-up, supervisor, 1.2.0
---

## Summary
On Apple Container 1.2.0 every `cspace up` fails at phase 7/8 with:

```
Error: waiting for sandbox /health: sandbox stuck in "supervisor" phase after 1m0s
```

The supervisor is fine — it is running, serving, and returns `HTTP 200` on
`/health` *from inside the container*. What is broken is host → sandbox TCP.
`cspace up` polls `http://<container-ip>:6201/health` (`cmd_up.go:874`,
`ctlURL := fmt.Sprintf("http://%s:%d", ip, supervisorPort)`), and every packet
of that poll is dropped inside the sandbox.

Cause: `lib/runtime/scripts/cspace-entrypoint.sh` installs a PREROUTING rule
that DNATs **all** inbound TCP on the external interface to `127.0.0.1`, and
relies on `net.ipv4.conf.{all,eth0}.route_localnet=1` to make that legal. On
1.2.0 the sysctl write fails and the DNAT rule succeeds, which is the worst
possible combination: the rule rewrites every inbound TCP packet to
`127.0.0.1`, and the kernel then discards it as a martian because
`route_localnet` is still `0`.

## Details

Observed inside a sandbox on Apple Container 1.2.0 (kernel 6.18.15, the Kata
3.28.0 build that 1.2.0 installs by default):

```
# /proc/sys is read-only
proc on /proc/sys type proc (ro,relatime)

# so the sysctl write fails
sysctl: permission denied on key "net.ipv4.conf.all.route_localnet"
echo 1 > /proc/sys/net/ipv4/conf/all/route_localnet
  -> cannot create ...: Read-only file system

# route_localnet stays 0
all: 0
eth0: 0

# but the DNAT rule installs fine and matches traffic
Chain PREROUTING (policy ACCEPT)
 523 33424 DNAT  6 -- eth0 * 0.0.0.0/0  0.0.0.0/0  to:127.0.0.1
```

The asymmetry is the trap: iptables/nftables is configured over **netlink**, which
still works, while sysctl goes through **/proc/sys**, which is now read-only.
`cspace-entrypoint.sh:200-201` writes the sysctls with
`>/dev/null 2>&1 || true`, so the failure is completely silent — the boot log
shows nothing, and the only symptom is the unrelated-looking `/health` timeout
a minute later.

Symptom triangulation, all on 1.2.0:

- `ping <sandbox-ip>` from the host: **works**, 0% loss. ICMP is protocol 1 and
  the DNAT rule matches only protocol 6, so ICMP is never rewritten. This is
  why the sandbox looks reachable.
- `nc -z <sandbox-ip> 6201` from the host: **fails**.
- `curl -H "Authorization: Bearer $CSPACE_CONTROL_TOKEN" http://127.0.0.1:6201/health`
  from *inside* the sandbox: **HTTP 200**.
- A plain `alpine` container with a netcat listener on 8080: **reachable from
  the host**, proving Apple Container 1.2.0 host→container TCP is healthy in
  general and that this is cspace's own rule doing the damage.
- `iptables -t nat -D PREROUTING -i eth0 -p tcp -j DNAT --to-destination 127.0.0.1`
  → host→sandbox `6201` goes to **OPEN** immediately, authenticated `/health`
  from the host returns **HTTP 200**, and `cspace agent status <sandbox>`
  works end to end.

That last line is the proof and also points at the fix.

**The DNAT is not needed for the supervisor.** `ss -lnt` inside the sandbox
shows `LISTEN 0.0.0.0:6201` — the supervisor already binds all interfaces, so
DNAT-to-loopback buys it nothing and currently costs it everything. Per the
entrypoint comment the rule exists so that services which bind *only* loopback
inside a sandbox are still reachable from vmnet peers (also noted in CLAUDE.md's
security posture section). That convenience now breaks every 0.0.0.0 service,
because PREROUTING rewrites inbound TCP regardless of what the destination
service bound to.

Not yet established: whether 1.1.x mounted `/proc/sys` read-write. The
behavior is consistent with the security hardening in the 1.2.0 release
(XPC validation, kernel-archive digest verification) or with a change in the
`containerization` 0.37.0 dependency, but this has not been verified against a
1.1.x install. Worth confirming before filing upstream.

Suggested directions, roughly in order of preference:

1. **Make the DNAT conditional on the sysctl actually succeeding.** Read back
   `/proc/sys/net/ipv4/conf/all/route_localnet` and only add the PREROUTING
   rule when it is `1`. Fails safe: 0.0.0.0 services keep working, and only
   the loopback-only convenience is lost. Smallest change, no behavior loss
   where the sysctl still works.
2. **Stop relying on the DNAT at all**, and require in-sandbox services to bind
   `0.0.0.0`. Removes a surprising, security-relevant rule (it is what makes
   "loopback-only" services reachable by vmnet peers — see the security posture
   note in CLAUDE.md) at the cost of breaking any project service that binds
   loopback only.
3. **Set the sysctl out-of-band.** 1.2.0 adds `--kernel-arg`, so
   `route_localnet` may be settable as a kernel boot parameter rather than via
   `/proc/sys`. Unverified.

Either way the `>/dev/null 2>&1 || true` on those sysctl writes should go — a
silent failure here costs a full 60s boot timeout and gives a symptom
(`stuck in "supervisor" phase`) that points nowhere near the cause.

## Updates
### 2026-08-06 — @agent — status: open
Found while smoke-testing Apple Container 1.2.0 after upgrading from a machine
with no Apple Container installed at all. Diagnosis is complete and the fix is
verified by hand (deleting the rule restores full function), but no code change
has been made — choosing between the three directions above is a design call,
and direction 2 in particular changes a documented security property.
