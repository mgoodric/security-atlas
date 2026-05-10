# 039 — CLI binary distribution + release pipeline

**Cluster:** Infra / deploy
**Estimate:** 1d
**Type:** AFK

## Narrative

Establish the release pipeline for the `security-atlas` server binary and the `security-atlas-cli` CLI: tagged releases trigger GitHub Actions to build signed binaries for macOS (Intel + Apple Silicon), Linux (x86_64 + arm64), and Windows (x86_64). Distribute via GitHub Releases. Binary signing with cosign (matching the evidence-bundle signing pattern). Homebrew formula for macOS. The slice delivers value because CI users can install the push CLI with one command — adoption-friction-zero for the push integration story.

## Acceptance criteria

- [ ] AC-1: Tagged release `v0.1.0` triggers GoReleaser-style workflow; produces signed binaries for 5 OS/arch targets
- [ ] AC-2: `cosign verify-blob` validates downloaded binaries against published signatures
- [ ] AC-3: Homebrew tap published; `brew install security-atlas/tap/security-atlas-cli` works on macOS
- [ ] AC-4: Linux users can `curl -sSL https://get.security-atlas.io | sh` (or equivalent) to install
- [ ] AC-5: `security-atlas-cli --version` returns the release tag
- [ ] AC-6: Release notes auto-generated from conventional-commit history

## Constitutional invariants honored

- **Replacement-grade criterion 7 (installable in 4h):** CLI install is one command away
- **Anti-pattern rejected (proprietary collector agents):** the CLI is the universal push escape hatch — no agent needed

## Canvas references

- `Plans/canvas/09-tech-stack.md` (CLI release pipeline)
- `Plans/EVIDENCE_SDK.md` §6 (push CLI)

## Dependencies

- #001, #003

## Anti-criteria (P0)

- Does NOT publish unsigned binaries
- Does NOT skip cosign verification step in release pipeline
- Does NOT couple to any non-permissive third-party package channel

## Skill mix (3–5)

- GoReleaser config
- GitHub Actions release workflow
- cosign signing
- Homebrew formula
- Cross-compilation
