#!/usr/bin/env bash
#
# test-helm-oidc-key.sh — helm template test for the OIDC client-secret key
# mount in deploy/helm/templates/atlas-deployment.yaml (security-atlas#2609).
#
# The key Secret is mounted root-owned, mode 0440, and the atlas image runs
# as distroless nonroot (uid/gid 65532). Without fsGroup the server cannot
# read ATLAS_OIDC_SECRET_KEY_FILE and the boot is fatal, so the invariant is:
# whenever the key volume renders, the atlas pod has an fsGroup.
#
#   1. defaults: no key volume, no _FILE env, no fsGroup added.
#   2. oidc.secretKey.existingSecret set: _FILE env, read-only mount,
#      volume from that Secret with defaultMode 0440, fsGroup 65532.
#   3. an explicit podSecurityContext.fsGroup wins over 65532.
#   4. values-production.yaml: the mount is off until a Secret is named.
#   5. values-production.yaml + existingSecret: fsGroup 65532 is added.
#   6. the NATS pod gets no fsGroup from the key mount.
#
# Needs helm on PATH. Exit 0 = all assertions pass, 1 = one failed.

set -euo pipefail

chart="$(cd "$(dirname "${BASH_SOURCE[0]}")/../deploy/helm" && pwd)"
failures=0

render() {
  helm template t "$chart" --show-only templates/atlas-deployment.yaml "$@"
}

expect() {
  local name="$1" out="$2" pattern="$3"
  if grep -qE "$pattern" <<<"$out"; then
    echo "ok   $name: $pattern"
  else
    echo "FAIL $name: missing $pattern"
    failures=$((failures + 1))
  fi
}

expect_absent() {
  local name="$1" out="$2" pattern="$3"
  if grep -qE "$pattern" <<<"$out"; then
    echo "FAIL $name: $pattern rendered, want absent"
    failures=$((failures + 1))
  else
    echo "ok   $name: $pattern absent"
  fi
}

# The invariant, checked on every render: a key volume implies an fsGroup.
expect_fsgroup_with_mount() {
  local name="$1" out="$2"
  if grep -qE '^ +- name: oidc-secret-key$' <<<"$out" && ! grep -qE '^ +fsGroup: [0-9]+$' <<<"$out"; then
    echo "FAIL $name: key volume rendered without an fsGroup"
    failures=$((failures + 1))
  else
    echo "ok   $name: key volume implies fsGroup"
  fi
}

out="$(render)"
expect_absent defaults "$out" 'ATLAS_OIDC_SECRET_KEY_FILE'
expect_absent defaults "$out" 'oidc-secret-key'
expect_absent defaults "$out" 'fsGroup'
expect_fsgroup_with_mount defaults "$out"

out="$(render --set oidc.secretKey.existingSecret=atlas-oidc-key)"
expect mounted "$out" '^ +- name: ATLAS_OIDC_SECRET_KEY_FILE$'
expect mounted "$out" '^ +value: /etc/security-atlas/oidc/oidc-secret-key$'
expect mounted "$out" '^ +mountPath: /etc/security-atlas/oidc$'
expect mounted "$out" '^ +readOnly: true$'
expect mounted "$out" '^ +secretName: atlas-oidc-key$'
expect mounted "$out" '^ +defaultMode: 0440$'
expect mounted "$out" '^ +fsGroup: 65532$'
expect_fsgroup_with_mount mounted "$out"

out="$(render --set oidc.secretKey.existingSecret=atlas-oidc-key --set podSecurityContext.fsGroup=1234)"
expect fsgroup-override "$out" '^ +fsGroup: 1234$'
expect_absent fsgroup-override "$out" '^ +fsGroup: 65532$'
expect_fsgroup_with_mount fsgroup-override "$out"

out="$(render -f "$chart/values-production.yaml")"
expect_absent production "$out" 'ATLAS_OIDC_SECRET_KEY_FILE'
expect_absent production "$out" 'oidc-secret-key'
expect_fsgroup_with_mount production "$out"

out="$(render -f "$chart/values-production.yaml" --set oidc.secretKey.existingSecret=security-atlas-oidc-secret-key)"
expect production-mounted "$out" '^ +fsGroup: 65532$'
expect production-mounted "$out" '^ +defaultMode: 0440$'
expect_fsgroup_with_mount production-mounted "$out"

out="$(helm template t "$chart" --show-only templates/nats-statefulset.yaml --set oidc.secretKey.existingSecret=atlas-oidc-key)"
expect_absent nats "$out" 'fsGroup'

if [ "$failures" -ne 0 ]; then
  echo "$failures assertion(s) failed"
  exit 1
fi
echo "all helm OIDC key assertions passed"
