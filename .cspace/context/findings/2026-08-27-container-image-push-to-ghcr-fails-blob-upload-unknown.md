---
title: container image push to ghcr fails with BLOB_UPLOAD_UNKNOWN
date: 2026-08-27
kind: finding
status: wontfix
category: bug
tags: release, image, apple-container, ghcr
---

## Summary
`container image push ghcr.io/elliottregan/cspace:<tag>` (Apple Container
1.3.0) fails against ghcr.io. First observed cutting v1.0.0-rc.47, the first
release that publishes the sandbox image:

```
Pushing image ghcr.io/elliottregan/cspace:v1.0.0-rc.47 100% (30 blobs, 349.3 MB) [49s]
Error: HTTP request to https://ghcr.io/v2/elliottregan/cspace/blobs/upload/
16.183365e0-...?digest=sha256:b85069f9... failed with response: 404 Not Found.
Reason: {"errors":[{"message":"blob upload unknown to registry",
"code":"BLOB_UPLOAD_UNKNOWN"}]}
```

The GitHub release and both Homebrew casks published normally; only the image
step failed, so rc.47 exists without a published image and every host falls
back to a local build (`ensureSandboxImage`, by design). No user-visible
breakage, but the image half of the release is not delivered.

## Details
The failure is on the *commit* of a blob upload — the PUT that finalizes an
upload session — not on the transfer. ghcr returns 404 BLOB_UPLOAD_UNKNOWN
when the upload session it is handed is not one it recognizes: a session that
expired, or a URL the client reconstructed rather than following ghcr's
`Location` header verbatim. That points at Apple Container's push client
rather than at credentials: the token authenticated (login succeeded, and
30 blobs were accepted), and a scope problem would surface as 403, not 404.

Environment: Apple Container 1.3.0, macOS 25.5.0, token from `gh auth token`
with `write:packages`, package did not yet exist on ghcr (first push creates
it).

A second attempt got substantially further — past the 49s mark, uploading for
~12 minutes without the immediate error — before it was stopped by hand, so
it is not established whether the first failure was transient or whether the
retry would have completed. Both possibilities are still open.

Alternatives if it proves systematic: push with Docker instead (29.4.0 with
the containerd snapshotter is installed on the release host) by saving the OCI
tar and `docker load`/`docker push`, or build the image with `docker buildx`
in the first place — the Dockerfile is a plain linux/arm64 build and does not
need Apple Container's builder. `scripts/release.sh` would gain a pusher
selection rather than assuming `container image push`.

Note the image push is *not* bound by GitHub's release immutability: it can be
retried against the same tag without cutting a new rc.

## Updates
- 2026-08-27: Logged after v1.0.0-rc.47. Release published (binaries + casks);
  image absent from ghcr. Status open.
- 2026-08-27: wontfix — image publishing dropped rather than worked around.
  The pull path is removed from `cspace up` (a CLI that pulls from a registry
  nothing publishes would fail a network call on every stale or absent image
  before falling back), `cspace image pull` is gone, and `scripts/release.sh`
  no longer touches the container CLI. What survives from that work: `cspace
  up` builds a missing image automatically instead of failing the boot, and
  `make cspace-image` passes `--build-arg CSPACE_VERSION` so a locally-built
  image is not stale by construction. The measured case for publishing is in
  the comment on issue #68 if this is revisited — the blocker is this bug,
  not the economics.
