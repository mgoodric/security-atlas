# Decisions log — #1894: code scanning and CI build on current Go 1.27

- detection_tier_actual: manual_review
- detection_tier_target: contract

Ticket: security-atlas#1894, from the #1889 review (#1839, Go 1.27). The
active default-setup CodeQL built Go on the runner's go1.26.11, and every
`actions/setup-go` step accepted whatever 1.27.x the runner had cached.

## D1 — `toolchain go1.27.4` in `go.mod`, not a return to advanced `codeql.yml`

The ticket offered two fixes. Chose the toolchain line:

- It is one line in the module that owns the Go version. Default-setup
  CodeQL's autobuilder runs with `GOTOOLCHAIN=auto` and switches to the
  named toolchain, so code scanning builds on the same Go as CI.
- Re-enabling `codeql.yml` would run two CodeQL configurations, or mean
  turning default setup off in repo settings. That swaps a one-line fix
  for a second workflow to keep in step with `ci.yml`.
- The line affects only the main module. `go 1.26.0` stays the minimum,
  so importers of `pkg/sdk-go` see no change. Dependabot gomod bumps the
  toolchain line as new patches ship.

The same line goes into `go.work`. In workspace mode, `go` reads the
toolchain line from `go.work` and ignores the one in `go.mod`, and this
repo has a `go.work`.

The `golang:1.27` Docker builders set `GOTOOLCHAIN=local`. There the line
is advisory: only the `go` line is a hard minimum. Image builds are
unaffected.

## D2 — `check-latest: true`, not an exact patch pin

`check-latest: true` on all 17 `setup-go` steps makes each one resolve
the newest 1.27.x instead of using a cached older patch. An exact pin
would need a bump in 17 places for every security patch. The `toolchain`
line from D1 already records the exact patch, and with `GOTOOLCHAIN=auto`
CI switches up to it if setup-go installs an older one.
