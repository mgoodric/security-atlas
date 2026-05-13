# Security policy

## Supported versions

Until security-atlas reaches v1.0.0, security fixes are made on the latest tagged release only. After v1.0.0, the most recent two minor versions on the `main` line will receive backported fixes.

| Version             | Supported       |
| ------------------- | --------------- |
| Pre-1.0 (`0.x.y`)   | Latest tag only |
| `main` (unreleased) | Best effort     |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Use GitHub's private vulnerability reporting:

1. Navigate to https://github.com/mgoodric/security-atlas/security/advisories/new
2. Fill in the advisory form with reproduction steps, affected versions, and severity assessment.
3. The maintainer will acknowledge receipt within five business days.

Alternatively, email the maintainer at the address listed on their public GitHub profile, with `[security-atlas]` in the subject line. Encrypted mail (PGP) is welcome; the key is published on the maintainer's profile.

## What to include

A good report contains:

- **Affected component** — which package, binary, or endpoint
- **Affected versions** — commit SHA or release tag
- **Impact** — what an attacker can achieve (data exposure, RCE, auth bypass, denial of service)
- **Reproduction steps** — minimal sequence to demonstrate the issue
- **Proof-of-concept** — exploit code or a screenshot, where feasible
- **Suggested fix** — optional, but appreciated

## Response timeline

| Stage                         | Target                                           |
| ----------------------------- | ------------------------------------------------ |
| Acknowledgement               | 5 business days                                  |
| Initial assessment + severity | 10 business days                                 |
| Fix targeted in a release     | 30 days for high / critical                      |
| Public disclosure             | After patched release, coordinated with reporter |

For critical vulnerabilities affecting production deployments, the maintainer may issue an out-of-band patch release before the standard release cadence.

## Disclosure policy

security-atlas follows coordinated disclosure:

- The reporter and the maintainer agree a disclosure date once a fix is available.
- A GitHub Security Advisory is published with a CVE identifier (requested via GitHub) for issues with assigned-CVE-worthy impact.
- Credit is given in the advisory to the reporter unless they request anonymity.

## Scope

In scope:

- Code in this repository (Go, TypeScript, Python)
- Container images published to `ghcr.io/mgoodric/security-atlas`
- Default deployment artifacts under `deploy/`
- Connector code under `connectors/`

Out of scope:

- Third-party services the platform integrates with (AWS, GitHub, Okta, etc.) — report to the upstream vendor
- Self-hosted instances misconfigured by operators (missing TLS, RLS disabled, weak admin credentials)
- The maintainer's personal infrastructure or accounts

## Safe harbor

The maintainer will not pursue legal action against researchers who follow this policy in good faith. Acting in good faith means:

- Avoiding privacy violations and destruction of data
- Not accessing or modifying data belonging to others
- Reporting vulnerabilities promptly and giving the maintainer reasonable time to respond before public disclosure

## Recognition

A `SECURITY-ACKNOWLEDGEMENTS.md` file at the repo root will record researchers who have responsibly disclosed vulnerabilities, with their permission.

---

`SPDX-License-Identifier: Apache-2.0`
