#!/usr/bin/env bash
# description: Download htmltest binary from github directly.
# this let us pin the version the way we want it.
set -eufo pipefail
set -x

TARGET_VERSION=${1:-}
TARGETDIR=${2:-}

[[ -z ${TARGET_VERSION} || -z ${TARGETDIR} ]] && { echo "Usage: $0 <version> [targetdir]" && exit 1; }
[[ -d ${TARGETDIR} ]] || mkdir -p ${TARGETDIR}
[[ -x ${TARGETDIR}/htmltest ]] && {
	if version_output=$(${TARGETDIR}/htmltest --version 2>&1); then
		grep -q "${TARGET_VERSION}" <<<"${version_output}" && exit 0
	fi
	rm -f ${TARGETDIR}/htmltest
}

detect_os_arch() {
	local os
	local arch

	# Detect OS
	case "$(uname -s)" in
	Linux*) os=linux ;;
	Darwin*) os=macos ;;
	*) os="UNKNOWN" ;;
	esac

	# Detect architecture
	case "$(uname -m)" in
	x86_64) arch=amd64 ;;
	arm64) arch=arm64 ;;
	aarch64) arch=arm64 ;;
	*) arch="UNKNOWN" ;;
	esac

	[[ ${os} == "UNKNOWN" ]] && echo "Unknown OS" && exit 1
	[[ ${arch} == "UNKNOWN" ]] && echo "Unknown Arch" && exit 1

	echo "${os}_${arch}"
}

os_arch=$(detect_os_arch)

# htmltest release naming convention: htmltest_<version>_<os>_<arch>.tar.gz
# version in the URL does not have the 'v' prefix in the filename but has it in the tag
download_url=https://github.com/wjdp/htmltest/releases/download/v${TARGET_VERSION}/htmltest_${TARGET_VERSION}_${os_arch}.tar.gz

# Use HUB_TOKEN for authenticated requests if available (avoids GitHub rate limits in CI)
curl_auth=()
if [[ -n ${HUB_TOKEN:-} ]]; then
	curl_auth=(-H "Authorization: Bearer ${HUB_TOKEN}")
fi

echo -n "Downloading ${download_url} to ${TARGETDIR}: "
curl -s -L --fail-early -f "${curl_auth[@]+"${curl_auth[@]}"}" -o- "${download_url}" | tar -xz -C "${TARGETDIR}" htmltest
echo "Done"
