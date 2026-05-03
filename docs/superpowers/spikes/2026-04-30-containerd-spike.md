# containerd / Linux Spike — 2026-04-30

Companion to `2026-04-30-apple-container-spike.md`. Goal: confirm whether the
same OCI image we built for Apple Container (`cspace-prototype:latest`, from
`lib/templates/Dockerfile.prototype`) can run on Linux/containerd without
modification, and that the `internal/substrate/substrate.go` interface is
implementable for containerd. This is a **paper feasibility study** — see
"What was NOT verified" at the bottom.

## Environment

- Host attempted: **none reachable from this session.** The dev box is
  macOS 26.4 / arm64; no Linux VM (lima/UTM/Multipass) was provisioned and
  no remote Linux is wired up. Findings below come from public docs +
  reasoning against the existing Apple Container adapter, not measurements.
- containerd version expected: **2.2.3** (current stable, released
  2026-04-14) or **2.3 LTS** (the first annual LTS, also April 2026). Both
  ship the v2 Go client at `github.com/containerd/containerd/v2/client`.
- nerdctl version expected: **2.2.2** (released 2026-03-31). Distributed
  as a single static binary; the "full" tarball
  (`nerdctl-full-<ver>-linux-<arch>.tar.gz`) bundles containerd + runc +
  CNI plugins + BuildKit, which is the simplest install for an evaluation
  host.
- Target distro for P5 verification: any current Linux with cgroups v2 —
  Ubuntu 24.04+, Debian 12+, or Fedora 40+ all work. Kernel >= 5.15 is the
  practical floor for containerd 2.x because of the seccomp + cgroups v2
  features it expects.

## OCI image compatibility

- `cspace-prototype:latest` (Debian 12 + Bun + Claude Code + cspace + the
  supervisor binary) is built with `docker buildx` to a standard OCI image.
  containerd's image store consumes the same OCI image-spec v1.1
  manifests + layers that Apple Container does, so the artifact is
  drop-in compatible at the registry / tarball level.
- Apple Container also runs OCI images on Linux microVMs (kata-static
  arm64 kernel, see the Apple Container spike), so the runtime contract
  the image already targets is **Linux + glibc + the binary's GOARCH**.
  No image changes are required to satisfy containerd's contract.
- **Architecture caveat.** Our prototype image is currently built
  `linux/arm64` only (host is Apple Silicon, Apple Container's microVM is
  arm64). On an `arm64` Linux host, containerd will pull and run it
  unchanged. On an `amd64` Linux host, the runtime will refuse to start
  the container — the OCI manifest does not advertise an `amd64` variant.
  Two ways to address this in P5:
  1. Build multi-arch (`docker buildx build --platform
     linux/amd64,linux/arm64 …`) and push a multi-arch index. Adds a
     cross-build step (and an emulation layer on the build host) but
     keeps a single image reference.
  2. Maintain two tagged images (`…:latest-arm64`, `…:latest-amd64`) and
     resolve at run time from the host's `runtime.GOARCH`. Cheaper to
     build, costs us a tag-resolution branch in `prototype-up`.
- The P0 `cspace-prototype:latest` image lives in a local OCI store,
  not a registry. For Linux verification it would need to be either
  pushed to a registry the Linux host can reach, or exported with
  `docker save -o image.tar` and imported with `nerdctl load -i
  image.tar` / `ctr -n default image import image.tar`.

## Adapter feasibility

Two paths. Both are viable. The shapes of the calls are below — the
implementation patterns mirror `internal/substrate/applecontainer/adapter.go`
in the shell-out case, and a thin gRPC wrapper in the Go-client case.

### Path A — `nerdctl` shell-out adapter

Same shape as `applecontainer.Adapter`: build argv, `exec.CommandContext`,
parse stdout. Drop-in replacement at the substrate boundary.

#### `Run(ctx, RunSpec) error`

```
nerdctl run -d \
  --name <spec.Name> \
  -e KEY=VAL ... \
  -v /host:/container[:ro] ... \
  -p HOST:CONTAINER ... \
  <spec.Image> <spec.Command...>
```

Same flag spelling as Apple's `container run` for every flag we use today
(`-d`, `--name`, `-e`, `-v`, `-p` / `--publish`, `--network`). The Apple
adapter's argv builder transplants almost line-for-line. Caveat: nerdctl
honors more `-v` modifiers than `container` does (`:ro`, `:rro`,
`:rprivate`); cspace only sets `:ro` today and that flag is supported.

#### `Exec(ctx, name, cmd, ExecOpts) (ExecResult, error)`

```
nerdctl exec [-i] [-w <workdir>] [-e KEY=VAL ...] <name> <cmd...>
```

`-w` and `-e` exist with the same names as in the Apple adapter; stdio is
captured from the host process the same way (stdout/stderr buffers, exit
code via `*exec.ExitError`). Note that nerdctl's exec does **not** require
`-t` for non-interactive use; default mode is detached-stdio with clean
separation of stdout/stderr — same behavior we observed for Apple's
`container exec`.

#### `Stop(ctx, name) error`

```
nerdctl stop -t 10 <name>
nerdctl rm <name>
```

Idempotent in the same way `applecontainer.Adapter.Stop` is: discard
errors from both, return nil. `nerdctl stop` defaults to a 10-second
SIGTERM grace before SIGKILL (Apple's `container stop` defaults to 5);
worth aligning whichever way we want, but the Apple adapter currently
relies on the default so we'd just use the default here too.

#### `IP(ctx, name) (string, error)`

```
nerdctl inspect --format '{{.NetworkSettings.IPAddress}}' <name>
```

nerdctl supports `--format` (Apple's `container inspect` does **not**, which
is why the Apple adapter parses raw JSON). The simpler route is the same
JSON parse the Apple adapter uses, just at a different field path.
nerdctl emits Docker-compatible `NetworkSettings`:

```json
{
  "NetworkSettings": {
    "IPAddress": "10.4.0.5",
    "IPPrefixLen": 24,
    "Networks": {
      "bridge": {
        "IPAddress": "10.4.0.5",
        "IPPrefixLen": 24
      }
    }
  }
}
```

Field path: `[0].NetworkSettings.IPAddress` (or
`Networks.bridge.IPAddress`). No CIDR suffix to strip — unlike Apple's
`networks[].ipv4Address` which embeds `/24`. Type for the parse:

```go
type inspectRecord struct {
    NetworkSettings struct {
        IPAddress string `json:"IPAddress"`
    } `json:"NetworkSettings"`
}
```

#### `Available() bool`

```go
_, err := exec.LookPath("nerdctl")
return err == nil
```

Identical to the Apple adapter's `LookPath("container")`. Optionally also
poke the containerd socket
(`stat("/run/containerd/containerd.sock")`) to detect a CLI-without-daemon
state, but that mirrors the Apple adapter's deliberate choice **not** to
check apiserver health and we should match that.

### Path B — containerd Go gRPC client adapter

Direct gRPC, no subprocess. Imports:

```go
import (
    "github.com/containerd/containerd/v2/client"
    "github.com/containerd/containerd/v2/pkg/cio"
    "github.com/containerd/containerd/v2/pkg/namespaces"
    "github.com/containerd/containerd/v2/pkg/oci"
)
```

Default socket: `/run/containerd/containerd.sock`. Default namespace:
`default` (Kubernetes uses `k8s.io`; we'd carve out `cspace`).

#### `Run(ctx, RunSpec) error`

```go
c, err := client.New("/run/containerd/containerd.sock")
ctx = namespaces.WithNamespace(ctx, "cspace")
img, err := c.Pull(ctx, spec.Image, client.WithPullUnpack)

container, err := c.NewContainer(ctx, spec.Name,
    client.WithNewSnapshot(spec.Name+"-snap", img),
    client.WithNewSpec(
        oci.WithImageConfig(img),
        oci.WithProcessArgs(spec.Command...),
        oci.WithEnv(envSlice(spec.Env)),
        oci.WithMounts(specMounts(spec.Mounts)),
    ),
)

task, err := container.NewTask(ctx, cio.LogFile("/var/log/cspace/"+spec.Name+".log"))
err = task.Start(ctx)
```

`PublishPort` is the painful one: the Go client gives you the OCI runtime
spec, not Docker-style port publishing. To replicate `-p HOST:CONTAINER`
you either (a) set up CNI yourself with the bridge plugin and add a
port-mapping capability arg to the CNI call, or (b) lean on the fact
that for cspace we already concluded (in the Apple Container spike) that
direct-IP routing makes `--publish` unnecessary for cross-sandbox
messaging. P0's "preview port" workflow is the only consumer of
`PublishPort`; on Linux with a routable bridge subnet, even that can be
served via the container IP from the same host.

#### `Exec(ctx, name, cmd, ExecOpts) (ExecResult, error)`

```go
container, err := c.LoadContainer(ctx, name)
task, err := container.Task(ctx, nil)

procSpec := &specs.Process{
    Args: cmd,
    Env:  envSlice(opts.Env),
    Cwd:  opts.WorkDir,
}
var stdout, stderr bytes.Buffer
process, err := task.Exec(ctx, "exec-"+randID(), procSpec,
    cio.NewCreator(cio.WithStreams(nil, &stdout, &stderr)))
err = process.Start(ctx)

statusCh, err := process.Wait(ctx)
status := <-statusCh
return substrate.ExecResult{
    Stdout:   stdout.String(),
    Stderr:   stderr.String(),
    ExitCode: int(status.ExitCode()),
}, status.Error()
```

Slightly more code than the shell-out, but exit codes and stdio are
first-class instead of going through `*exec.ExitError`.

#### `Stop(ctx, name) error`

```go
container, err := c.LoadContainer(ctx, name)
task, _ := container.Task(ctx, nil)
if task != nil {
    _ = task.Kill(ctx, syscall.SIGTERM)
    statusCh, _ := task.Wait(ctx)
    select {
    case <-statusCh:
    case <-time.After(10 * time.Second):
        _ = task.Kill(ctx, syscall.SIGKILL)
        <-statusCh
    }
    _, _ = task.Delete(ctx)
}
_ = container.Delete(ctx, client.WithSnapshotCleanup)
return nil
```

Idempotency comes free — `LoadContainer` returns
`errdefs.ErrNotFound` when the container is gone, which we swallow.

#### `IP(ctx, name) (string, error)`

containerd does not own networking; CNI plugins do. The IP isn't
recoverable from the containerd Go API alone — you'd read it from the
network namespace (`/var/run/netns/<ns>` then `ip addr show eth0`),
or invoke the CNI bridge plugin's stored result file at
`/var/lib/cni/results/<container-id>` (a JSON file the bridge plugin
writes when it allocates an IP).

Practical choice if going this route: ship a small helper that reads the
CNI result JSON. Field path:
`{ "ips": [ { "address": "10.4.0.5/24", … } ] }`.

This is the one place the Go-client path is significantly more code than
the nerdctl shell-out (which gets `IPAddress` for free from the inspect
JSON nerdctl synthesizes by *also* reading the CNI result file).

#### `Available() bool`

```go
c, err := client.New("/run/containerd/containerd.sock")
if err != nil { return false }
defer c.Close()
_, err = c.Version(ctx)
return err == nil
```

Slightly stronger than `LookPath` — it confirms the daemon is reachable.

## Networking on Linux

- **CNI, not vmnet.** Apple Container uses `container-network-vmnet`
  (NAT mode, `192.168.64.0/24`). containerd uses CNI plugins; nerdctl
  ships a default config at `/etc/cni/net.d/nerdctl-bridge.conflist`
  that creates a Linux bridge `nerdctl0` with subnet **`10.4.0.0/24`**,
  gateway `10.4.0.1`. Each container gets a `veth` pair into that
  bridge and a sequential IP from the subnet.
- **Host -> container reachability.** Direct on Linux. The bridge IP
  range is on the host's routing table by virtue of `nerdctl0` being a
  host-side interface, so `ping 10.4.0.5` from the host works without
  `--publish`. This matches the Apple Container behaviour we exploited
  in P0 — direct-IP messaging works on both substrates.
- **Container -> container reachability.** Direct, on the same bridge.
  Two containers with no extra config see each other at their bridge
  IPs. nerdctl additionally registers each container's name in CNI's
  per-network DNS (`dnsname` plugin) when the network is created with
  it, so sibling-by-name resolution may "just work" on Linux where it
  did **not** on Apple Container — but we shouldn't depend on it,
  because the dnsname plugin is optional and not always in the default
  conflist.
- **Cross-container reachability is the same posture as Apple
  Container** (Task 1 spike's recommendation): a shared bridge + routable
  IPs, registry-mediated name resolution, no host port publishing
  needed for cross-sandbox messaging.
- **Linux-specific gotchas to verify on real hardware:**
  - `net.ipv4.ip_forward=1` is required for the bridge to forward
    egress traffic; nerdctl-full enables this on install but a
    bare containerd may not.
  - `iptables` (or `nftables`) NAT rules for bridge egress: nerdctl
    inserts `MASQUERADE` rules; if the host runs a strict firewall
    (firewalld, ufw) those rules can be wiped on reload.
  - Rootless containerd is a supported deployment but uses
    user-mode networking (slirp4netns / RootlessKit); the bridge
    subnet and IP visibility differ. cspace P5 should target
    rootful containerd for parity with the Apple Container model
    until rootless is explicitly requested.
  - Default bridge subnet (`10.4.0.0/24`) collides with some VPNs
    (Tailscale CGNAT range starts at `100.64.0.0/10`, fine; some
    corporate VPNs use `10.4.0.0/8` aggregates, not fine). The
    subnet is configurable via `nerdctl network create` or by
    editing the conflist.

## Recommendation

**Choose Path A — `nerdctl` shell-out adapter.**

Reasoning:

1. **Symmetry with the Apple Container adapter.** `applecontainer.Adapter`
   is a 158-line shell-out. A nerdctl adapter ports the same patterns
   (argv builder, JSON inspect parse, idempotent stop+rm) with field-path
   and flag-default tweaks. Maintaining two adapters that look the same
   keeps the substrate interface honest and the codebase legible.
2. **CNI integration is solved.** The single hardest thing in Path B is
   reading the assigned IP from CNI plugin output. nerdctl already does
   that work and exposes it as `NetworkSettings.IPAddress` in the Docker-
   compatible inspect JSON. We get networking + IP discovery + port
   publishing for free.
3. **Bug surface is smaller.** Go-client path B touches the OCI runtime
   spec (`oci.WithMounts`, `specs.Process`), which means we own
   compatibility with kernel/security-module quirks (seccomp profiles,
   cgroup paths, snapshot drivers) that nerdctl handles centrally.
4. **No daemon-free benefit at this layer.** containerd is itself a
   daemon, so "no nerdctl" still leaves you needing a running
   containerd. Path B's deployment-footprint argument only matters if
   we're willing to operate containerd ourselves; we're not, in P0/P1.

We should document Path B as the **escape hatch** if nerdctl ever
diverges from us (CLI flag deprecation, JSON shape change). The
v2 Go client is stable and well-documented; reaching for it later is
not a crisis.

**Engineer-day estimate for full P5 implementation: 6–9 engineer-days
for the nerdctl adapter alone.**

| Work item                                                     | Days |
| ------------------------------------------------------------- | ---- |
| Adapter scaffolding + Run/Exec/Stop/IP/Available + unit tests | 2    |
| CI Linux integration test (lima or GitHub Actions ubuntu-24)  | 1    |
| Multi-arch image build pipeline (`buildx`)                    | 1    |
| Networking parity test (cross-sandbox messaging)              | 1    |
| Bootstrap docs + `cspace doctor` Linux preflight              | 0.5  |
| Buffer for unknowns (rootless, distro variance)               | 1.5–3.5 |

The original issue's "1–2 weeks" estimate is **roughly correct** for the
adapter and its surrounding plumbing. It is **not** sufficient if P5 also
includes: rootless support, multi-distro CI, or a production Linux
release. Treat 1–2 weeks as the floor.

## What was NOT verified in this spike

Nothing was executed against a Linux host. Specifically:

- We did not actually pull, run, or exec `cspace-prototype:latest` under
  containerd. The image's compatibility is asserted from OCI-spec
  arguments, not measured.
- We did not measure containerd's cold-start vs. warm-start latency,
  so cannot compare against Apple Container's ~5.9 s cold / ~0.7 s warm
  numbers.
- We did not verify HMR / WebSocket / dev-server traffic over the CNI
  bridge — for cspace's "browser sidecar" pattern to work, long-lived
  WebSocket connections through the bridge need to be confirmed.
- We did not verify cross-arch behaviour. `linux/arm64` host is the
  presumed match for our current image; `linux/amd64` will need a
  multi-arch build and a real amd64 host to test against.
- We did not verify rootless containerd, nor any non-Ubuntu/Debian
  distro. RHEL-family hosts may have SELinux interactions
  (container-selinux policy) that nerdctl's mount labelling
  (`:Z`, `:z`) is meant to handle but we haven't exercised.
- We did not verify firewall-aware behaviour (firewalld/ufw reloads
  wiping CNI iptables rules), which is a known operational footgun on
  long-running hosts.

These need real Linux hardware before P5 starts. The cheapest path to
get that: a `lima` VM on the existing macOS dev box (Ubuntu 24.04, with
containerd-full preinstalled via the lima containerd template). That
would knock out everything except the multi-arch / multi-distro items
in a single afternoon.
