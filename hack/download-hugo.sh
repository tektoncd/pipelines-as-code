#!/usr/bin/env bash
# description: Download hugo binary from github directly.
# this let us pin the version the way we want it.
# Author: Chmouel Boudjnah <chmouel@chmouel.com>
set -eufo pipefail
set -x

TARGET_VERSION=${1:-}
TARGETDIR=${2:-}

[[ -z ${TARGET_VERSION} || -z ${TARGETDIR} ]] && { echo "Usage: $0 <version> [targetdir]" && exit 1; }
[[ -d ${TARGETDIR} ]] || mkdir -p ${TARGETDIR}
[[ -x ${TARGETDIR}/hugo ]] && {
  if version_output=$(${TARGETDIR}/hugo version); then
    grep -q "${TARGET_VERSION}.*extended " <<<"${version_output}" && exit 0
  fi
  rm -f ${TARGETDIR}/hugo
}

detect_os_arch() {
  local os
  local arch

  # Detect OS
  case "$(uname -s)" in
  Linux*) os=Linux ;;
  Darwin*) os=macOS ;;
  *) os="UNKNOWN" ;;
  esac

  # Detect architecture
  case "$(uname -m)" in
  x86_64) arch=64bit ;;
  arm64) arch=ARM64 ;;
  aarch64) arch=ARM64 ;;
  *) arch="UNKNOWN" ;;
  esac

  [[ ${os} == "UNKNOWN" ]] && echo "Unknown OS" && exit 1
  [[ ${arch} == "UNKNOWN" ]] && echo "Unknown Arch" && exit 1

  echo "${os}-${arch}"
}

os_arch=$(detect_os_arch)
download_url=https://github.com/gohugoio/hugo/releases/download/v${TARGET_VERSION}/hugo_extended_${TARGET_VERSION}_${os_arch}.tar.gz

# If we are on a 64 bit arch we can use the go install method because older hugo don't have a binary for it
[[ ${os_arch} == *ARM64 ]] && {
  export GOBIN=${TARGETDIR}
  go install -tags extended -mod=mod github.com/gohugoio/hugo@v${TARGET_VERSION}
  exit 0
}
echo -n "Downloading ${download_url} to ${TARGETDIR}: "
curl -s -L --fail-early -f -o- ${download_url} | tar -xz -C ${TARGETDIR} hugo
echo "Done"
