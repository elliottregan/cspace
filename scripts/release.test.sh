#!/usr/bin/env bash
# Tests scripts/release.sh against a throwaway git repo with stubbed
# goreleaser / container / gh / make. Nothing here touches the network, the
# real repo, or a real registry.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/release.sh"
fail() { echo "FAIL: $1"; exit 1; }

# make_repo prints the path to a fresh clone whose origin is a bare repo, with
# one commit on main and release.sh in place. $1, if given, becomes an extra
# untracked file (to exercise the dirty-tree gate).
make_repo() {
  local tmp; tmp="$(mktemp -d)"
  git init -q --bare "$tmp/origin.git"
  git init -q -b main "$tmp/work"
  (
    cd "$tmp/work" || exit 1
    git config user.email t@example.com
    git config user.name Test
    mkdir -p scripts
    cp "$SCRIPT" scripts/release.sh
    echo "module example.com/x" > go.mod
    git add -A
    git commit -qm "initial commit"
    git remote add origin "$tmp/origin.git"
    git push -q origin main
    git branch -q --set-upstream-to=origin/main main 2>/dev/null || true
  )
  [ -n "${1:-}" ] && echo "junk" > "$tmp/work/$1"
  echo "$tmp"
}

# stub_bin writes stubs that log their argv to $1/calls.log and succeed.
stub_bin() {
  local tmp="$1" bin="$1/bin"
  mkdir -p "$bin"
  for tool in goreleaser container gh make; do
    cat > "$bin/$tool" <<EOF
#!/usr/bin/env bash
echo "$tool \$*" >> "$tmp/calls.log"
case "$tool \$1" in
  "gh auth")  echo "gho_stubtoken" ;;
  "container system") echo "status running" ;;
esac
exit 0
EOF
    chmod +x "$bin/$tool"
  done
}

# run_release runs the script in the temp repo with stubs on PATH, sets RC, and
# leaves combined output in $out. Not called in a command substitution: RC must
# land in the caller's scope, which a subshell would swallow.
run_release() {
  local tmp="$1"; shift
  ( cd "$tmp/work" && PATH="$tmp/bin:$PATH" bash scripts/release.sh "$@" ) > "$tmp/out.log" 2>&1
  RC=$?
  out="$(cat "$tmp/out.log")"
}

# ── no tag ────────────────────────────────────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
run_release "$tmp"
[ "$RC" -eq 0 ] && fail "no-tag: exited 0, want non-zero"
echo "$out" | grep -qi "usage" || fail "no-tag: no usage message: $out"

# ── malformed tag ─────────────────────────────────────────────────────────
for bad in 1.0.0 v1.0 vX.Y.Z latest; do
  tmp="$(make_repo)"; stub_bin "$tmp"
  run_release "$tmp" "$bad"
  [ "$RC" -eq 0 ] && fail "bad tag '$bad' accepted"
done

# ── well-formed tags are accepted (dry run) ───────────────────────────────
for good in v1.0.0 v1.0.0-rc.47 v2.13.4-rc.100; do
  tmp="$(make_repo)"; stub_bin "$tmp"
  run_release "$tmp" "$good" --dry-run
  [ "$RC" -eq 0 ] || fail "good tag '$good' rejected: $out"
done

# ── dirty tree ────────────────────────────────────────────────────────────
tmp="$(make_repo "stray.txt")"; stub_bin "$tmp"
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] && fail "dirty tree: exited 0, want non-zero"
echo "$out" | grep -q "stray.txt" || fail "dirty tree: message does not name the offending path: $out"

# ── tag already exists ────────────────────────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
(cd "$tmp/work" && git tag v1.0.0-rc.47)
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] && fail "existing tag: exited 0, want non-zero"
echo "$out" | grep -qi "already exists" || fail "existing tag: unclear message: $out"

# ── HEAD not pushed ───────────────────────────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
(cd "$tmp/work" && echo x >> go.mod && git commit -qam "unpushed work")
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] && fail "unpushed HEAD: exited 0, want non-zero"
echo "$out" | grep -qi "push" || fail "unpushed HEAD: message does not say to push: $out"

# ── dry run publishes nothing ─────────────────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
run_release "$tmp" v1.0.0-rc.47 --dry-run
[ "$RC" -eq 0 ] || fail "dry run failed: $out"
(cd "$tmp/work" && git rev-parse -q --verify refs/tags/v1.0.0-rc.47 >/dev/null) \
  && fail "dry run created a tag"
grep -q "goreleaser release .*--skip" "$tmp/calls.log" \
  || fail "dry run did not run goreleaser in skip-publish mode: $(cat "$tmp/calls.log")"
grep -q "container image push" "$tmp/calls.log" && fail "dry run pushed an image"

# ── full run: tag, publish, image ─────────────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] || fail "full run failed: $out"
(cd "$tmp/work" && git rev-parse -q --verify refs/tags/v1.0.0-rc.47 >/dev/null) \
  || fail "full run did not create the tag"
(cd "$tmp/work" && git ls-remote --tags origin | grep -q v1.0.0-rc.47) \
  || fail "full run did not push the tag to origin"
grep -q "make check" "$tmp/calls.log" || fail "full run skipped make check"
grep -qE "goreleaser release( |.*)--clean" "$tmp/calls.log" \
  || fail "full run did not invoke goreleaser release --clean: $(cat "$tmp/calls.log")"
grep -q "container build.*ghcr.io/elliottregan/cspace:v1.0.0-rc.47" "$tmp/calls.log" \
  || fail "full run did not build the image with the release tag: $(cat "$tmp/calls.log")"
grep -q "CSPACE_VERSION=v1.0.0-rc.47" "$tmp/calls.log" \
  || fail "image build did not bake the release version label: $(cat "$tmp/calls.log")"
grep -q "container image push ghcr.io/elliottregan/cspace:v1.0.0-rc.47" "$tmp/calls.log" \
  || fail "full run did not push the image: $(cat "$tmp/calls.log")"

# ── --skip-image stops at binaries ────────────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
run_release "$tmp" v1.0.0-rc.47 --skip-image
[ "$RC" -eq 0 ] || fail "--skip-image run failed: $out"
grep -q "container image push" "$tmp/calls.log" && fail "--skip-image pushed an image"
grep -qE "goreleaser release" "$tmp/calls.log" || fail "--skip-image skipped the binaries too"

# ── check failure aborts before tagging ───────────────────────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
cat > "$tmp/bin/make" <<EOF
#!/usr/bin/env bash
echo "make \$*" >> "$tmp/calls.log"
exit 1
EOF
chmod +x "$tmp/bin/make"
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] && fail "failing checks did not abort the release"
(cd "$tmp/work" && git rev-parse -q --verify refs/tags/v1.0.0-rc.47 >/dev/null) \
  && fail "failing checks still created a tag"

# ── a stopped apiserver is not "running" ──────────────────────────────────
# `container system status` prints "apiserver is not running and not registered
# with launchd" when it is down — a substring match on "running" reads that as
# healthy, which is the exact false positive parseSystemStatus exists to avoid.
tmp="$(make_repo)"; stub_bin "$tmp"
cat > "$tmp/bin/container" <<EOF
#!/usr/bin/env bash
echo "container \$*" >> "$tmp/calls.log"
if [ "\$1 \$2" = "system status" ]; then
  echo "apiserver is not running and not registered with launchd"
  exit 0
fi
exit 0
EOF
chmod +x "$tmp/bin/container"
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] && fail "released with a stopped apiserver"
echo "$out" | grep -qi "container system start" || fail "stopped apiserver: message does not say how to start it: $out"
(cd "$tmp/work" && git rev-parse -q --verify refs/tags/v1.0.0-rc.47 >/dev/null) \
  && fail "stopped apiserver still created a tag"

# ── failure after the tag is pushed explains immutability ─────────────────
tmp="$(make_repo)"; stub_bin "$tmp"
cat > "$tmp/bin/goreleaser" <<EOF
#!/usr/bin/env bash
echo "goreleaser \$*" >> "$tmp/calls.log"
exit 1
EOF
chmod +x "$tmp/bin/goreleaser"
run_release "$tmp" v1.0.0-rc.47
[ "$RC" -eq 0 ] && fail "goreleaser failure did not fail the release"
echo "$out" | grep -qi "immutable" || fail "post-tag failure does not explain immutability: $out"
echo "$out" | grep -q "v1.0.0-rc.48" || fail "post-tag failure does not name the next tag to cut: $out"

echo "PASS"
