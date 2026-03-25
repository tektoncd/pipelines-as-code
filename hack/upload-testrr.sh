#!/usr/bin/env bash
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: $0 <go-test-json-file> [<go-test-json-file> ...]" >&2
  exit 2
fi

required_vars=(
  TESTRR_URL
  TESTRR_PROJECT
  TESTRR_USERNAME
  TESTRR_PASSWORD
)

for var_name in "${required_vars[@]}"; do
  if [[ -z "${!var_name:-}" ]]; then
    echo "testrr upload skipped: missing ${var_name}" >&2
    exit 0
  fi
done

declare -a files=()
for path in "$@"; do
  if [[ -s "${path}" ]]; then
    files+=("${path}")
  fi
done

if [[ ${#files[@]} -eq 0 ]]; then
  echo "testrr upload skipped: no non-empty go test json files provided" >&2
  exit 0
fi

upload_url="${TESTRR_URL%/}/api/v1/projects/${TESTRR_PROJECT}/runs"
curl_args=(
  --fail
  --silent
  --show-error
  --retry 3
  --retry-all-errors
  --user "${TESTRR_USERNAME}:${TESTRR_PASSWORD}"
)

for file_path in "${files[@]}"; do
  curl_args+=(-F "files=@${file_path}")
done

optional_fields=(
  TESTRR_BRANCH:branch
  TESTRR_COMMIT_SHA:commit_sha
  TESTRR_BUILD_ID:build_id
  TESTRR_BUILD_URL:build_url
  TESTRR_RUN_LABEL:run_label
  TESTRR_ENVIRONMENT:environment
  TESTRR_STARTED_AT:started_at
)

for field in "${optional_fields[@]}"; do
  env_name="${field%%:*}"
  form_name="${field##*:}"
  if [[ -n "${!env_name:-}" ]]; then
    curl_args+=(-F "${form_name}=${!env_name}")
  fi
done

curl "${curl_args[@]}" "${upload_url}"
