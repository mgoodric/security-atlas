#!/usr/bin/env bash
#
# modules-route-coverage.sh -- falsifier for isa/modules.md ISC-2: "Every
# server route belongs to exactly one module, core or optional."
#
# The answer is the Go guard test TestEveryRouteBelongsToAModule
# (internal/api/modules_route_guard_test.go, #2171). It builds the
# production router, walks every registered route, and resolves each one
# through the module registry (internal/featureflag Modules). A route no
# module owns (ErrNoModule), or one two modules own equally
# (ErrAmbiguousModule), fails the test and is named in its output.
#
# No database: the router builds without one, so this is a pure probe.
#
# Exit 0  every registered route resolves to exactly one module.
# Exit 1  at least one does not (claim false); the routes are printed.
# Exit 3  cannot run: no Go toolchain, or the package does not build (a
#         module download or compile failure is not an answer).
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"
cd "$ROOT" || exit 3

if ! command -v go >/dev/null 2>&1; then
    echo "modules-route-coverage: go not on PATH" >&2
    exit 3
fi

if ! go vet ./internal/api/ >/dev/null 2>&1; then
    echo "modules-route-coverage: internal/api does not build; cannot answer" >&2
    exit 3
fi

out="$(ATLAS_TEST_MODE=1 go test -count=1 -run '^TestEveryRouteBelongsToAModule$' ./internal/api/ 2>&1)"
status=$?
if [[ $status -eq 0 ]]; then
    echo "modules-route-coverage: every registered route belongs to exactly one module"
    exit 0
fi
printf '%s\n' "$out"
echo "modules-route-coverage: a registered route belongs to no module, or to more than one" >&2
exit 1
