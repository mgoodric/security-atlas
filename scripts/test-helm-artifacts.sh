#!/usr/bin/env bash
#
# test-helm-artifacts.sh — helm template test for the artifact-store
# wiring in deploy/helm/templates/configmap.yaml (security-atlas#1939).
#
# Renders the ConfigMap under several value sets and asserts the
# ARTIFACTS_S3_PUBLIC_ENDPOINT / ARTIFACTS_S3_FORCE_PATH_STYLE keys the
# atlas server reads (internal/artifact/build.go):
#
#   1. defaults: neither key is rendered, so the server falls back to
#      ARTIFACTS_S3_ENDPOINT and path-style-on-when-endpoint-set.
#   2. artifacts.s3PublicEndpoint set: rendered verbatim.
#   3. artifacts.forcePathStyle=false / true: rendered as "false" / "true".
#   4. values-production.yaml: path-style off (AWS virtual-hosted).
#   5. artifacts.forcePathStyle=maybe: the render fails.
#
# Needs helm on PATH. Exit 0 = all assertions pass, 1 = one failed.

set -euo pipefail

chart="$(cd "$(dirname "${BASH_SOURCE[0]}")/../deploy/helm" && pwd)"
failures=0

render() {
  helm template t "$chart" --show-only templates/configmap.yaml "$@"
}

expect_line() {
  local name="$1" out="$2" line="$3"
  if grep -qxF "  $line" <<<"$out"; then
    echo "ok   $name: $line"
  else
    echo "FAIL $name: missing '$line'"
    failures=$((failures + 1))
  fi
}

expect_absent() {
  local name="$1" out="$2" key="$3"
  if grep -q "^  $key:" <<<"$out"; then
    echo "FAIL $name: $key rendered, want absent"
    failures=$((failures + 1))
  else
    echo "ok   $name: $key absent"
  fi
}

out="$(render)"
expect_line defaults "$out" 'ARTIFACTS_S3_ENDPOINT: "http://t-security-atlas-minio:9000"'
expect_absent defaults "$out" ARTIFACTS_S3_PUBLIC_ENDPOINT
expect_absent defaults "$out" ARTIFACTS_S3_FORCE_PATH_STYLE

out="$(render --set artifacts.s3PublicEndpoint=https://minio.example.com)"
expect_line public-endpoint "$out" 'ARTIFACTS_S3_PUBLIC_ENDPOINT: "https://minio.example.com"'

out="$(render --set artifacts.forcePathStyle=false)"
expect_line path-style-false "$out" 'ARTIFACTS_S3_FORCE_PATH_STYLE: "false"'

out="$(render --set artifacts.forcePathStyle=true)"
expect_line path-style-true "$out" 'ARTIFACTS_S3_FORCE_PATH_STYLE: "true"'

out="$(render -f "$chart/values-production.yaml")"
expect_line production "$out" 'ARTIFACTS_S3_FORCE_PATH_STYLE: "false"'
expect_absent production "$out" ARTIFACTS_S3_PUBLIC_ENDPOINT

if render --set artifacts.forcePathStyle=maybe >/dev/null 2>&1; then
  echo "FAIL path-style-invalid: render succeeded, want failure"
  failures=$((failures + 1))
else
  echo "ok   path-style-invalid: render refused"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures assertion(s) failed"
  exit 1
fi
echo "all helm artifact assertions passed"
