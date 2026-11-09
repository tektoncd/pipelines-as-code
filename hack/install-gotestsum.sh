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
  if version_output=$("${TARGETDIR}/gotestsum" --version 2>/dev/null); then
    grep -q "${VERSION}" <<<"${version_output}" && exit 0
  fi
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
asset_name="gotestsum_${VERSION}_${os_arch}.tar.gz"
download_url="https://github.com/gotestyourself/gotestsum/releases/download/v${VERSION}/${asset_name}"

token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
curl_args=(-sSL -f --connect-timeout 20 --retry 5 --retry-delay 3 --retry-all-errors)

tmpfile=$(mktemp)
trap 'rm -f "${tmpfile}"' EXIT

# github.com release URLs ignore tokens, so authenticated downloads must go
# through the REST API asset endpoint.
if [[ -n "${token}" ]] && command -v jq >/dev/null; then
  curl_args+=(-H "Authorization: Bearer ${token}")
  curl "${curl_args[@]}" -o "${tmpfile}" \
    "https://api.github.com/repos/gotestyourself/gotestsum/releases/tags/v${VERSION}"
  download_url=$(jq -er --arg name "${asset_name}" '.assets[] | select(.name == $name) | .url' "${tmpfile}")
  curl_args+=(-H "Accept: application/octet-stream")
fi

echo -n "Downloading gotestsum ${VERSION} (${os_arch}) to ${TARGETDIR}: "
# Download to a file first so curl retries on a truncated response instead of
# handing a partial stream to tar.
curl "${curl_args[@]}" -o "${tmpfile}" "${download_url}"
tar -xzf "${tmpfile}" -C "${TARGETDIR}" gotestsum
echo "Done"
