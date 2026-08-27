#!/usr/bin/env bash
# Cut a cspace release from this Mac: tag, then publish binaries and Homebrew
# casks via goreleaser.
#
# Local rather than CI on purpose. One `gh auth token` covers both the release
# and the tap push (the tap is the same account's repo), so there is no Actions
# secret to rot — the previous HOMEBREW_TAP_GITHUB_TOKEN was invalid from rc.36
# through rc.41 while every release still reported success. Running it here also
# means a failure is seen immediately, which matters because a published GitHub
# release is immutable and cannot be re-cut under the same tag.
#
# The sandbox image is NOT published. Apple Container's push to ghcr.io fails
# with BLOB_UPLOAD_UNKNOWN (see
# .cspace/context/findings/2026-08-27-container-image-push-to-ghcr-fails-blob-
# upload-unknown.md); rather than work around it, every host builds its own
# image — `cspace up` does it automatically when the image is missing.
#
# Usage: scripts/release.sh <vX.Y.Z[-rc.N]> [--dry-run]
#
#   --dry-run     run every check and build nothing publishable: no tag,
#                 no release
#
# Environment:
#   CSPACE_RELEASE_ALLOW_BRANCH=1   release from a branch other than main
#   TAG_MESSAGE                     annotated tag message (default: HEAD subject)
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }
step() { echo "==> $*"; }

usage() {
  cat >&2 <<'EOF'
Usage: scripts/release.sh <vX.Y.Z[-rc.N]> [--dry-run]

  --dry-run     run the checks and a throwaway build; publish nothing
EOF
  exit 2
}

TAG=""
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run)    DRY_RUN=1 ;;
    -h|--help)    usage ;;
    -*)           echo "error: unknown flag $arg" >&2; usage ;;
    *)            [ -n "$TAG" ] && { echo "error: more than one tag given" >&2; usage; }; TAG="$arg" ;;
  esac
done

[ -n "$TAG" ] || { echo "error: no tag given" >&2; usage; }

# A release tag is the version. Refusing anything else keeps the GitHub tag and
# the binary's stamped version identical — `cspace image build` derives the
# release it downloads its linux binary from out of that version, so a
# hand-rolled tag would send it at a release that does not exist.
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] \
  || die "tag '$TAG' is not a release tag (want vX.Y.Z or vX.Y.Z-rc.N)"

cd "$(git rev-parse --show-toplevel)" || die "not inside a git repository"

# ── Guards ────────────────────────────────────────────────────────────────
# Untracked files count as dirty here because they count as dirty to
# goreleaser: it refuses to build from a tree that does not match the tag.
dirty="$(git status --porcelain)"
[ -z "$dirty" ] || die "working tree is dirty; commit, remove, or ignore these first:
$dirty"

branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "$branch" != "main" ] && [ "${CSPACE_RELEASE_ALLOW_BRANCH:-0}" != "1" ]; then
  die "on branch '$branch', not main (set CSPACE_RELEASE_ALLOW_BRANCH=1 to override)"
fi

git rev-parse -q --verify "refs/tags/$TAG" >/dev/null \
  && die "tag $TAG already exists locally. Releases are immutable — cut the next rc instead of reusing a tag."
if git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
  die "tag $TAG already exists on origin. Releases are immutable — cut the next rc instead of reusing a tag."
fi

git fetch -q origin "$branch" || die "could not fetch origin/$branch"
if [ "$(git rev-parse HEAD)" != "$(git rev-parse "origin/$branch")" ]; then
  die "HEAD does not match origin/$branch — push your commits first, so the release names a commit others can fetch"
fi

for tool in goreleaser gh git; do
  command -v "$tool" >/dev/null || die "$tool is not installed"
done

# One token covers both jobs: the release itself and the Homebrew tap push,
# which lands in a repo the same account owns.
TOKEN="$(gh auth token 2>/dev/null || true)"
[ -n "$TOKEN" ] || die "no GitHub token from \`gh auth token\` (run \`gh auth login\`)"
export GITHUB_TOKEN="$TOKEN"
export HOMEBREW_TAP_GITHUB_TOKEN="$TOKEN"

step "checks"
make check || die "checks failed — nothing was tagged or published"

if [ "$DRY_RUN" -eq 1 ]; then
  step "dry run: building artifacts without tagging or publishing"
  goreleaser release --clean --skip=publish,announce,validate \
    || die "dry-run build failed"
  echo
  echo "Dry run complete. Nothing was tagged, pushed, or published."
  exit 0
fi

# ── Publish ───────────────────────────────────────────────────────────────
message="${TAG_MESSAGE:-$TAG: $(git log -1 --format=%s)}"
step "tagging $TAG"
git tag -a "$TAG" -m "$message"
git push origin "$TAG"

# Past this point the tag is public. A failure here cannot be fixed by
# re-running: the GitHub release may already exist, and releases are immutable.
trap 'echo "
Release of $TAG failed after the tag was pushed. If the GitHub release was
already created it is immutable — fix the cause and cut ${TAG%.*}.$((${TAG##*.} + 1))
rather than retrying this tag." >&2' ERR

step "publishing binaries and casks"
goreleaser release --clean

trap - ERR
echo
echo "Released $TAG: https://github.com/elliottregan/cspace/releases/tag/$TAG"
echo
echo "Testers: brew update && brew upgrade --cask cspace-rc"
echo "First cspace up on a new host builds the sandbox image locally."
