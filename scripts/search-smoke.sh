#!/usr/bin/env bash
# Exercises the full search stack against the cspace repo itself.
# Assumes search sidecars are reachable (run from inside a cspace container,
# or adjust env / search.yaml for your environment). Requires jq.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

echo "=== Building binaries ==="
make build

echo "=== Indexing code corpus ==="
./bin/cspace-search index --corpus=code --quiet

echo "=== Indexing commits corpus ==="
./bin/cspace-search index --corpus=commits --quiet

echo "=== Clustering code corpus ==="
./bin/cspace-search clusters --corpus=code

echo "=== Query: 'routing from client to host' ==="
./bin/cspace-search query --corpus=code "routing from client to host" --json > /tmp/hits.json
jq '.results[] | {path,score}' /tmp/hits.json

# Assertion: the routing chain should return at least one hit in the docker
# package and one in compose templates.
if ! jq -e '.results[].path | select(test("internal/docker/"))' /tmp/hits.json >/dev/null; then
  echo "FAIL: no hit under internal/docker/"
  exit 1
fi
if ! jq -e '.results[].path | select(test("docker-compose"))' /tmp/hits.json >/dev/null; then
  echo "FAIL: no hit under compose templates"
  exit 1
fi

echo "=== Smoke test passed. ==="
