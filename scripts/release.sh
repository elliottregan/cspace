#!/usr/bin/env bash
# Cut a cspace release from this Mac: tag, publish binaries + Homebrew casks
# via goreleaser, and publish the sandbox image to ghcr.io.
#
# Local rather than CI on purpose. Apple Container needs Virtualization.framework,
# which GitHub's hosted macOS runners do not expose, so a Mac is the only place
# that can build the sandbox image and cut the binaries in one pass. Running it
# here also means a failed release is caught before anything is published —
# a published GitHub release is immutable and cannot be re-cut under the same
# tag (see the rc.41 tap-less release).
#
# Usage: scripts/release.sh <vX.Y.Z[-rc.N]> [--dry-run] [--skip-image]
#
#   --dry-run     run every check and build nothing publishable: no tag, no
#                 release, no image push
#   --skip-image  publish binaries + casks only, leaving the sandbox image at
#                 the previous release
#
# Environment:
#   CSPACE_RELEASE_ALLOW_BRANCH=1   release from a branch other than main
#   TAG_MESSAGE                     annotated tag message (default: HEAD subject)
#
# One-time setup: the gh token needs write:packages to push the image
# (`gh auth refresh -s write:packages`), and the ghcr package is created
# private on first push — make it public once, or `cspace up` on other hosts
# cannot pull it anonymously.
set -euo pipefail

GHCR_REPO="ghcr.io/elliottregan/cspace"
GHCR_USER="elliottregan"
LOCAL_IMAGE="cspace:latest"
DOCKERFILE="lib/templates/Dockerfile"

die() { echo "error: $*" >&2; exit 1; }
step() { echo "==> $*"; }

usage() {
  cat >&2 <<'EOF'
Usage: scripts/release.sh <vX.Y.Z[-rc.N]> [--dry-run] [--skip-image]

  --dry-run     run the checks and a throwaway build; publish nothing
  --skip-image  skip the sandbox image; publish binaries and casks only
EOF
  exit 2
}

TAG=""
DRY_RUN=0
SKIP_IMAGE=0
for arg in "$@"; do
  case "$arg" in
    --dry-run)    DRY_RUN=1 ;;
    --skip-image) SKIP_IMAGE=1 ;;
    -h|--help)    usage ;;
    -*)           echo "error: unknown flag $arg" >&2; usage ;;
    *)            [ -n "$TAG" ] && { echo "error: more than one tag given" >&2; usage; }; TAG="$arg" ;;
  esac
done

[ -n "$TAG" ] || { echo "error: no tag given" >&2; usage; }

# A release tag is the version. Refusing anything else keeps the GitHub tag,
# the binary's stamped version, and the image tag identical — cspace up derives
# the image ref from its own version, so a hand-rolled tag would leave the CLI
# pulling an image that does not exist.
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
if [ "$SKIP_IMAGE" -eq 0 ]; then
  command -v container >/dev/null || die "container (Apple Container) is not installed; re-run with --skip-image"
  # Read the text, not just the exit code, and never with a bare "running"
  # match: a stopped apiserver reports "apiserver is not running and not
  # registered with launchd", which contains the word.
  status_out="$(container system status 2>&1 || true)"
  if echo "$status_out" | grep -qi "not running" ||
     ! echo "$status_out" | grep -qiE "status[[:space:]]+running|is running"; then
    die "Apple Container's apiserver is not running (start it with \`container system start\`):
$status_out"
  fi
fi

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
  if [ "$SKIP_IMAGE" -eq 0 ]; then
    step "dry run: building the sandbox image as $GHCR_REPO:$TAG (not pushed)"
    make cspace-linux
    container build --platform linux/arm64 \
      --tag "$GHCR_REPO:$TAG" \
      --file "$DOCKERFILE" \
      --build-arg "CSPACE_VERSION=$TAG" \
      . || die "image build failed"
  fi
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

if [ "$SKIP_IMAGE" -eq 1 ]; then
  trap - ERR
  echo
  echo "Released $TAG (binaries + casks). Sandbox image left at the previous release."
  exit 0
fi

step "building the sandbox image"
make cspace-linux
container build --platform linux/arm64 \
  --tag "$GHCR_REPO:$TAG" \
  --file "$DOCKERFILE" \
  --build-arg "CSPACE_VERSION=$TAG" \
  .
# Point this Mac's local tag at what was just built, so the maintainer's own
# `cspace up` doesn't turn around and pull the image it just made.
container image tag "$GHCR_REPO:$TAG" "$LOCAL_IMAGE"

step "pushing $GHCR_REPO:$TAG"
printf '%s' "$TOKEN" | container registry login ghcr.io --username "$GHCR_USER" --password-stdin \
  || die "ghcr login failed — the token needs the write:packages scope (\`gh auth refresh -s write:packages\`)"
container image push "$GHCR_REPO:$TAG"

trap - ERR
echo
echo "Released $TAG:"
echo "  binaries + casks   https://github.com/elliottregan/cspace/releases/tag/$TAG"
echo "  sandbox image      $GHCR_REPO:$TAG"
echo
echo "Testers: brew update && brew upgrade --cask cspace-rc"
echo
echo "If this was the first image push, make the ghcr package public once at"
echo "https://github.com/users/$GHCR_USER/packages/container/cspace/settings —"
echo "it is created private, and \`cspace up\` pulls anonymously."
