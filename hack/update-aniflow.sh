#!/usr/bin/env bash
# Copy the aniflow diagram player into docs/static/aniflow.
#
# Usage: hack/update-aniflow.sh [ANIFLOW_CHECKOUT]
#
# Without an argument, clone https://github.com/chmouel/aniflow at
# ANIFLOW_REF (default: main). The commit used is written to
# docs/static/aniflow/VERSION.
set -euo pipefail

TOP=$(git rev-parse --show-toplevel)
DEST=${TOP}/docs/static/aniflow
SRC=${1:-}

if [[ -z ${SRC} ]]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "${tmp}"' EXIT
  git clone -q https://github.com/chmouel/aniflow "${tmp}/aniflow"
  git -C "${tmp}/aniflow" checkout -q "${ANIFLOW_REF:-main}"
  SRC=${tmp}/aniflow
fi

for f in src/aniflow/static/aniflow.min.js src/aniflow/static/aniflow-diagrams.css; do
  [[ -f ${SRC}/${f} ]] || {
    echo "${SRC}/${f} is missing, is this an aniflow checkout?" >&2
    exit 1
  }
done

rm -rf "${DEST}"
mkdir -p "${DEST}/icons"
cp "${SRC}/src/aniflow/static/aniflow.min.js" "${SRC}/src/aniflow/static/aniflow-diagrams.css" "${DEST}/"
cp "${SRC}/LICENSE" "${DEST}/LICENSE"
cp "${SRC}"/src/aniflow/icons/*.svg "${SRC}/src/aniflow/icons/LICENSE.lucide" "${DEST}/icons/"
git -C "${SRC}" rev-parse HEAD >"${DEST}/VERSION"

echo "aniflow $(cat "${DEST}/VERSION") copied to ${DEST#"${TOP}"/}"
