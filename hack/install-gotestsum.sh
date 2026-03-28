#!/usr/bin/env bash
# description: Download gotestsum binary from GitHub releases.
# this lets us pin the version and avoid slow `go install` in CI.
# Author: Chmouel Boudjnah <chmouel@chmouel.com>
set -eufo pipefail

VERSION=${1:-}
TARGETDIR=${2:-}

[[ -z ${VERSION} || -z ${TARGETDIR} ]] && { echo "Usage: $0 <version> <targetdir>" && exit 1; }
[[ -d ${TARGETDIR} ]] || mkdir -p "${TARGETDIR}"
[[ -x ${TARGETDIR}/gotestsum ]] && {
  "${TARGETDIR}/gotestsum" --version 2>/dev/null | grep -q "${VERSION}" && exit 0
  rm -f "${TARGETDIR}/gotestsum"
}

detect_os_arch() {
  local os arch

  case "$(uname -s)" in
  Linux*)  os=linux ;;
  Darwin*) os=darwin ;;
  *) echo "Unknown OS" && exit 1 ;;
  esac

  case "$(uname -m)" in
  x86_64)           arch=amd64 ;;
  arm64 | aarch64)  arch=arm64 ;;
  *) echo "Unknown arch" && exit 1 ;;
  esac

  echo "${os}_${arch}"
}

os_arch=$(detect_os_arch)
download_url="https://github.com/gotestyourself/gotestsum/releases/download/v${VERSION}/gotestsum_${VERSION}_${os_arch}.tar.gz"

token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
curl_args=(-sL --fail-early -f)
[[ -n "${token}" ]] && curl_args+=(-H "Authorization: Bearer ${token}")

echo -n "Downloading gotestsum ${VERSION} (${os_arch}) to ${TARGETDIR}: "
curl "${curl_args[@]}" -o- "${download_url}" | tar -xz -C "${TARGETDIR}" gotestsum
echo "Done"
