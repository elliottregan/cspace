# Apple Container Spike — 2026-04-30

Exploratory spike for Phase 0 of the sandbox-architecture prototype. Goal: install
Apple's `container` CLI (`github.com/apple/container`), exercise the basic
lifecycle, and characterise its networking, exec, mount, log, and stop semantics
well enough to unblock the downstream P0 tasks (substrate adapter, host
registry, cross-sandbox messaging).

## Environment

- macOS 26.4 (Darwin 25.4.0), build 25E246
- Apple Silicon: yes (`uname -m` -> `arm64`)
- `container` CLI version: `0.12.3` (build: release)
- Install method: `brew install container` (Homebrew core formula, not a cask).
  No `.pkg` download was needed; the Homebrew bottle was used directly.
- Runtime initialisation: `container system start` registered the apiserver
  with `launchd`. The first invocation prints
  `Error: failed to read user input` because it tries to interactively prompt
  for a kernel install, but the apiserver itself comes up fine. The kernel was
  installed non-interactively with
  `container system kernel set --recommended`, which downloads the
  kata-containers static kernel
  (`kata-static-3.28.0-arm64.tar.zst`).
- No sudo / GUI / admin prompt was required for any step in this spike.
  `container system dns create` is documented as requiring admin, but we did
  not need it.

## Boot times

All measurements taken on the host with `time`. The "warm" path means the
image is already pulled and the runtime VM has been used at least once during
the session.

| Operation                                                | Wall time |
| -------------------------------------------------------- | --------- |
| `container image pull docker.io/library/alpine:latest`   | 10.7 s (cold; pulls all platforms in the OCI index)   |
| First `container run --rm alpine echo …` after pull       | 5.86 s (one-time fetch+unpack of init image, then VM boot) |
| Warm `container run --rm alpine echo …` (run #2)          | 0.71 s    |
| Warm `container run --rm alpine echo …` (run #3)          | 0.66 s    |

The cold-run cost is dominated by a one-time fetch of an "init image"
(`[4/6] Fetching init image`) — the helper rootfs that boots the microVM —
not the alpine image itself. After that completes, container start is
sub-second. Each `container run` is its own microVM, but the per-VM boot is
fast enough (~0.6 s) that we don't need to keep a long-running VM pool.

## Networking

- **IP scheme.** A built-in `default` network exists, plugin
  `container-network-vmnet`, mode `nat`, IPv4 subnet `192.168.64.0/24`,
  gateway `192.168.64.1`, IPv6 ULA subnet `fda1:50f2:2a68:9f6a::/64`. New
  containers receive sequential addresses (`probe1` -> `192.168.64.5`,
  `probe2` -> `192.168.64.6`, etc.). Inside the container `eth0` carries the
  IPv4 address and a global IPv6 address; the default route points at
  `192.168.64.1`.
- **Host -> container reachability.** Direct: `ping 192.168.64.5` from the
  host succeeded with ~0.5 ms RTT. A TCP test (`nc -l -p 8765` inside the
  container, `nc 192.168.64.5 8765` from the host) delivered the payload
  with no `--publish` flag.
- **Container -> container reachability.** Direct: `ping 192.168.64.6` from
  inside `probe1` succeeded with ~0.7 ms RTT. Sibling resolution by name does
  *not* work out of the box — `getent hosts probeB` and `ping probeB` from
  inside `probeA` both fail. `/etc/hosts` only contains the container's own
  hostname plus loopback; `/etc/resolv.conf` points at the gateway as the
  only nameserver. Cross-container calls must use the IP, or the operator
  must register a DNS domain via `container system dns create` (admin
  required).
- **Container -> host gateway.** The host *is* the gateway: `ifconfig` on the
  host shows an interface owning `192.168.64.1/24`, and host->gateway ping
  RTT is 0.07 ms (loopback-class). From inside `probe1`, both `ping
  192.168.64.1` and a TCP connect to a host listener on `192.168.64.1:47591`
  succeeded immediately. There is no `host.docker.internal` /
  `host.containers.internal` shortcut — `getent hosts host.docker.internal`
  returns nothing — but the gateway IP itself is the host.
- **`--publish` semantics.** `container run` does support `-p`/`--publish`
  and `--publish-socket`, but for our use case it is not needed: the
  container's IP is already routable from the host. Publish exists for
  cases where the host wants to bind on `127.0.0.1` rather than expose the
  microVM IP.
- **Conclusion.** Direct IPs work in both directions and between siblings.
  cspace P0 cross-sandbox messaging does not need `hostfwd` / port-publishing
  to function. The IP is, however, assigned dynamically at run time, so we
  still need a registry that maps "sandbox name" -> "current IP". Name-based
  DNS would only become free if we use `container system dns create`, which
  requires admin and adds a fragile global-state dependency we shouldn't
  take on for a prototype.

## Exec / logs / mounts / stop

- **Bind mounts.** `-v "$PWD:/workspace"` produced an RW bind mount: a write
  from inside the container (`echo … > /workspace/.spike-test-touch`) showed
  up on the host immediately, owned by the host UID (no UID remapping
  surprises observed for files created by a root-running process inside the
  container — they appeared as `elliott:staff` on the host because the macOS
  bind-mount layer retains the host owner). RO is presumably available via
  the standard `:ro` suffix but was not explicitly tested.
- **`container exec` stdio.** stdout and stderr are *separated* — redirecting
  them into different host files (`>/tmp/spike-stdout 2>/tmp/spike-stderr`)
  produced clean, non-interleaved output. Exit codes propagate faithfully:
  `container exec … sh -c 'exit 7'` returns `7` to the host shell.
- **`container logs`.** Captures the init process's stdio. Supports a
  one-shot read, `-n` for tail-N lines, and `-f`/`--follow` for streaming.
  A separate `--boot` mode exposes the kata kernel boot log, which is
  occasionally useful for debugging VM startup but is not part of the normal
  application stream.
- **`container stop`.** Sends `SIGTERM` by default with a 5-second grace
  before `SIGKILL`. Both knobs are tunable: `--signal` and `--time`.
  Verified empirically:
  - A trap-aware container (`trap "echo got-sigterm; exit 0" TERM`) shut
    down in ~1.0 s and `got-sigterm` was visible in `container logs`.
  - A SIGTERM-ignoring container (`trap "" TERM`) was killed at the 5 s
    deadline (wall time 5.35 s).

## Surprises and gotchas

1. **Interactive kernel prompt on first `system start`.** The CLI tries to
   read from stdin to prompt for the kata-containers kernel download and
   exits with `Error: failed to read user input` if stdin isn't a TTY. The
   apiserver does come up regardless; the user just has no kernel and
   therefore no `container run`. The fix is `container system kernel set
   --recommended`, which downloads non-interactively. Anything that
   automates the install of `container` (cspace bootstrapping included)
   needs to run that command explicitly.
2. **Pull is multi-arch even on Apple Silicon.** `container image pull
   alpine:latest` unpacked all 7 platform variants (amd64, arm/v6, arm/v7,
   arm64/v8, 386, ppc64le, riscv64, s390x). It still only took ~10 s for
   alpine, but for fatter base images the multi-arch unpack will be
   surprisingly slow. We may want to investigate
   `container image pull --platform linux/arm64` for the prototype image.
3. **No automatic sibling-name DNS.** Coming from Docker Compose this is the
   biggest behavioural delta: containers do not see each other's names, and
   `host.docker.internal` does not exist. The host gateway IP
   (`192.168.64.1`) is the only "well-known" address the container gets for
   free.
4. **First-run init-image fetch.** Even after pulling alpine, the first
   `container run` of the session does a separate `[4/6] Fetching init
   image` pull (~63 MB). This explains the 5.9 s "cold run" vs. 0.7 s
   subsequent runs. Operators should expect a one-time delay per machine,
   not per image.
5. **Container IP is not stable across runs.** `probe1` got `.5` and
   `.11` in two different sessions of this spike. The registry layer in P0
   needs to record the IP at start time and treat it as ephemeral.
6. **Default resource limits look generous.** `container ls` shows each
   sandbox at 4 CPUs / 1024 MB by default. We may want to cap this for
   cspace sandboxes so a single agent can't starve the host.
7. **MTU is 1280**, not 1500. Probably fine for everything we do (it
   matches IPv6's minimum), but worth noting if anything tunnels.

## Implication for cspace P0

- **Substrate adapter (Task 2)** can rely on a simple, Docker-shaped CLI
  surface: `container run -d --name … -v … <image> <cmd>`,
  `container exec`, `container logs [-f]`, `container stop -t N`,
  `container rm`. The verbs we depend on all exist with semantics close
  enough to Docker that the adapter is mostly a translation layer. The
  `system start` / `system kernel set --recommended` bootstrap should be a
  one-shot `cspace` precondition check.
- **Sandbox registry (Task 5) and cross-sandbox messaging (Task 9):**
  **Recommendation: direct-IP, registry-mediated** — *not* hostfwd-and-host-registry.
  - `container inspect --format` (or the JSON output we already used)
    gives us the IPv4 at start time. The host registry stores
    `name -> {ipv4, supervisor port}`; `cspace prototype-send` looks up the
    target and connects directly to `http://<ipv4>:<port>`.
  - This avoids the entire complexity of allocating, tracking, and
    cleaning up host-side published ports. The microVM subnet is private
    to the host, so binding the supervisor on `0.0.0.0` inside the
    sandbox is acceptable.
  - The host-side daemon (Task 8) still has a job: it owns the
    name->IP mapping, dispenses sandbox names, and notices when
    `container ls` says a container is gone so it can prune the record.
    But it does *not* need to be a TCP forwarder.
  - A hybrid posture is available if we ever need to expose a sandbox
    *to the user's browser on `localhost`* (the cspace TUI's existing
    "preview port" workflow). For that case, fall back to
    `-p 127.0.0.1:HOSTPORT:CONTAINERPORT`. Cross-sandbox traffic should
    not pay that cost.
- **Exec stream handling (Task 4 / supervisor):** stdout/stderr are
  cleanly separated and exit codes propagate, so the existing supervisor
  NDJSON-on-stdout pattern translates with no changes. Logs are persisted
  by `container` itself, so we get a free fallback channel even if the
  supervisor's stdio pipe is dropped — same role the `/logs/events`
  bind-mount plays today.
- **Stop semantics:** the 5-second SIGTERM grace is the same default as
  Docker. The supervisor's existing `interrupt`/`status` socket commands
  do not need changes; `container stop -t 30` is the obvious way to give
  long-running agent shutdowns headroom.
- **Boot budget:** ~0.7 s warm start per sandbox is fast enough that we
  can afford a `container run` per agent task without pooling. We should
  still pre-pull the prototype image and trigger one warm-up run during
  `cspace system bootstrap` to absorb the one-time init-image fetch.
