#!/usr/bin/env sh
# security-atlas-cli installer
# Usage:  curl -sSL https://get.security-atlas.io | sh
#
# Or pinned:  curl -sSL https://get.security-atlas.io | VERSION=v0.1.0 sh
#
# Environment variables:
#   VERSION    Tag to install. "latest" (default) resolves via the GitHub
#              redirect at /releases/latest.
#   PREFIX     Install prefix. Defaults to /usr/local/bin if writable,
#              otherwise $HOME/.local/bin.
#   OS, ARCH   Override detection (rare; useful for cross-platform packaging).
#   VERIFY     "checksum" (default) | "cosign" | "none". When "cosign" the
#              installer additionally verifies the cosign signature of the
#              checksums file (recommended for supply-chain-conscious users).
#   DRY_RUN    Non-empty to print intent without writing anything.
#
# Constitutional invariants honored:
# - Always verifies sha256 against the published checksums file.
#   Defaults to NOT silently degrade when verification fails — exits non-zero.
# - Never executes downloaded code beyond moving the binary into PREFIX.
# - Does not couple to a non-permissive package channel (anti-criterion).
#
# Portable to bash, dash, busybox sh. No bashisms.

set -eu

REPO="mgoodric/security-atlas"
BINARY="security-atlas-cli"
RELEASE_BASE="https://github.com/${REPO}/releases"

VERSION="${VERSION:-latest}"
VERIFY="${VERIFY:-checksum}"
DRY_RUN="${DRY_RUN:-}"

log()   { printf '%s\n' "install.sh: $*" >&2; }
fatal() { log "fatal: $*"; exit 1; }

# detect_os normalizes uname output to the GoReleaser archive token.
detect_os() {
    if [ -n "${OS:-}" ]; then printf '%s' "${OS}"; return; fi
    u=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "${u}" in
        linux*)  printf 'linux'  ;;
        darwin*) printf 'darwin' ;;
        msys*|mingw*|cygwin*) printf 'windows' ;;
        *) fatal "unsupported OS: ${u}" ;;
    esac
}

# detect_arch normalizes uname -m to GoReleaser tokens (amd64 / arm64).
detect_arch() {
    if [ -n "${ARCH:-}" ]; then printf '%s' "${ARCH}"; return; fi
    m=$(uname -m)
    case "${m}" in
        x86_64|amd64)  printf 'amd64' ;;
        arm64|aarch64) printf 'arm64' ;;
        *) fatal "unsupported arch: ${m}" ;;
    esac
}

# resolve_version turns "latest" into the actual tag by inspecting the
# /releases/latest redirect. Quiet curl flags + -L means we get the resolved
# URL on stderr-free stdout.
resolve_version() {
    if [ "${VERSION}" != "latest" ]; then
        printf '%s' "${VERSION}"
        return
    fi
    # -o /dev/null discards the body; -w "%{url_effective}" prints the
    # final URL after following redirects. -s silences progress.
    url=$(curl -sSL -o /dev/null -w '%{url_effective}' "${RELEASE_BASE}/latest")
    # url ends in .../releases/tag/vX.Y.Z
    tag="${url##*/}"
    if [ -z "${tag}" ] || [ "${tag}" = "latest" ]; then
        fatal "could not resolve latest version (got '${url}')"
    fi
    printf '%s' "${tag}"
}

# pick_prefix chooses the install dir. /usr/local/bin if writable;
# otherwise ~/.local/bin (created if missing).
pick_prefix() {
    if [ -n "${PREFIX:-}" ]; then
        printf '%s' "${PREFIX}"
        return
    fi
    if [ -w /usr/local/bin ] 2>/dev/null; then
        printf '/usr/local/bin'
        return
    fi
    p="${HOME}/.local/bin"
    [ -d "${p}" ] || mkdir -p "${p}"
    printf '%s' "${p}"
}

# verify_sha256 checks one file against the published checksums file.
verify_sha256() {
    file="$1"
    checksums="$2"
    base=$(basename "${file}")
    want=$(grep "  ${base}\$" "${checksums}" | awk '{print $1}')
    [ -n "${want}" ] || fatal "no checksum entry for ${base} in ${checksums}"
    if command -v sha256sum >/dev/null 2>&1; then
        got=$(sha256sum "${file}" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        got=$(shasum -a 256 "${file}" | awk '{print $1}')
    else
        fatal "neither sha256sum nor shasum found; cannot verify"
    fi
    if [ "${got}" != "${want}" ]; then
        fatal "sha256 mismatch for ${base}: want ${want}, got ${got}"
    fi
}

# verify_cosign optionally verifies the cosign signature on the checksums
# file. Requires the cosign binary on PATH.
verify_cosign() {
    checksums="$1"
    sig="$2"
    cert="$3"
    if ! command -v cosign >/dev/null 2>&1; then
        fatal "VERIFY=cosign requested but cosign is not on PATH"
    fi
    cosign verify-blob \
        --certificate-identity-regexp "https://github.com/${REPO}/\.github/workflows/release\.yml@.*" \
        --certificate-oidc-issuer https://token.actions.githubusercontent.com \
        --certificate "${cert}" \
        --signature  "${sig}" \
        "${checksums}" >&2
}

main() {
    os=$(detect_os)
    arch=$(detect_arch)
    tag=$(resolve_version)
    version="${tag#v}"
    prefix=$(pick_prefix)

    ext='tar.gz'
    if [ "${os}" = "windows" ]; then ext='zip'; fi

    archive="security-atlas_${version}_${os}_${arch}.${ext}"
    checksums="security-atlas_${version}_checksums.txt"
    base_url="${RELEASE_BASE}/download/${tag}"

    log "version=${tag} os=${os} arch=${arch} prefix=${prefix} verify=${VERIFY}"

    if [ -n "${DRY_RUN}" ]; then
        log "DRY_RUN set; would download ${base_url}/${archive}"
        return 0
    fi

    tmp=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf ${tmp}" EXIT

    curl -fSL "${base_url}/${archive}"   -o "${tmp}/${archive}"
    curl -fSL "${base_url}/${checksums}" -o "${tmp}/${checksums}"

    verify_sha256 "${tmp}/${archive}" "${tmp}/${checksums}"

    if [ "${VERIFY}" = "cosign" ]; then
        curl -fSL "${base_url}/${checksums}.sig" -o "${tmp}/${checksums}.sig"
        curl -fSL "${base_url}/${checksums}.pem" -o "${tmp}/${checksums}.pem"
        verify_cosign "${tmp}/${checksums}" "${tmp}/${checksums}.sig" "${tmp}/${checksums}.pem"
    elif [ "${VERIFY}" != "checksum" ] && [ "${VERIFY}" != "none" ]; then
        fatal "VERIFY must be one of: checksum, cosign, none (got '${VERIFY}')"
    fi

    # Extract.
    case "${ext}" in
        tar.gz) tar -xzf "${tmp}/${archive}" -C "${tmp}" ;;
        zip)    unzip -q "${tmp}/${archive}" -d "${tmp}" ;;
    esac

    bin_src="${tmp}/${BINARY}"
    [ "${os}" = "windows" ] && bin_src="${bin_src}.exe"
    [ -f "${bin_src}" ] || fatal "expected ${bin_src} inside archive; layout drift?"

    install -m 0755 "${bin_src}" "${prefix}/$(basename "${bin_src}")"
    log "installed $(basename "${bin_src}") to ${prefix}"
    log "run: ${BINARY} --version"
}

main "$@"
