# Verify Release Artifacts

security-atlas release artifacts are signed with Sigstore cosign keyless
signing through GitHub Actions OIDC. There are no signing keys in the
repository or in GitHub secrets.

Use cosign v3.x for the commands below.

## Verify a Binary Archive

```sh
REPO=mgoodric/security-atlas
TAG=vX.Y.Z
VERSION="${TAG#v}"
ASSET="security-atlas_${VERSION}_linux_amd64.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${TAG}"

curl -fsSLO "${BASE}/${ASSET}"
curl -fsSLO "${BASE}/security-atlas_${VERSION}_checksums.txt"
curl -fsSLO "${BASE}/security-atlas_${VERSION}_checksums.txt.sigstore.json"

cosign verify-blob \
  --certificate-identity-regexp "https://github.com/${REPO}/\.github/workflows/release\.yml@.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle "security-atlas_${VERSION}_checksums.txt.sigstore.json" \
  "security-atlas_${VERSION}_checksums.txt"

sha256sum --ignore-missing -c "security-atlas_${VERSION}_checksums.txt"
```

The cosign signature covers the checksum file. The checksum file covers
each release archive.

## Verify a Container Image

Stable release images are signed after the multi-arch manifest list is
published. Verify the tag you intend to run:

```sh
REPO=mgoodric/security-atlas
IMAGE=ghcr.io/${REPO}
TAG=vX.Y.Z

cosign verify "${IMAGE}:${TAG}" \
  --certificate-identity-regexp "https://github.com/${REPO}/\.github/workflows/container-publish\.yml@.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The same command applies to the other release images:

```sh
ghcr.io/mgoodric/security-atlas-cli:vX.Y.Z
ghcr.io/mgoodric/security-atlas-web:vX.Y.Z
ghcr.io/mgoodric/security-atlas-bootstrap:vX.Y.Z
```

Edge images (`:edge` and `:main-<sha7>`) are deliberately unsigned. They
carry build provenance and SBOM attestations, but cosign signatures are
reserved for promoted release tags.

## Verify Provenance

Binary archives and stable container images also carry GitHub
attestations.

```sh
gh attestation verify "${ASSET}" \
  --repo "${REPO}" \
  --cert-identity-regex "https://github.com/${REPO}/\.github/workflows/release\.yml@.*" \
  --cert-oidc-issuer https://token.actions.githubusercontent.com
```

For container images, resolve the digest first:

```sh
DIGEST=$(docker buildx imagetools inspect "${IMAGE}:${TAG}" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')

gh attestation verify "oci://${IMAGE}@${DIGEST}" \
  --repo "${REPO}" \
  --cert-identity-regex "https://github.com/${REPO}/\.github/workflows/container-publish\.yml@.*" \
  --cert-oidc-issuer https://token.actions.githubusercontent.com
```
