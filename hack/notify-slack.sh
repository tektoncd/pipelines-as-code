#!/usr/bin/env bash
# Send Slack notification for E2E test failures.
# Parses test output logs from downloaded artifacts to build a Block Kit message.
#
# Required env vars:
#   SLACK_WEBHOOK_URL, GITHUB_REPOSITORY, GITHUB_REF_NAME,
#   GITHUB_SERVER_URL, GITHUB_RUN_ID, GITHUB_SHA
#
# Usage: ./hack/notify-slack.sh <artifacts_dir>
set -euo pipefail

main() {
  local artifacts_dir="${1:-artifacts}"

  if [[ -z "${SLACK_WEBHOOK_URL:-}" ]]; then
    echo "SLACK_WEBHOOK_URL is not set, skipping Slack notification"
    return 0
  fi

  if [[ ! -d "${artifacts_dir}" ]]; then
    echo "Artifacts directory '${artifacts_dir}' not found"
    return 1
  fi

  echo "Scanning artifacts in: ${artifacts_dir}"

  local failure_details=""
  local provider_dirs
  provider_dirs=$(find "${artifacts_dir}" -maxdepth 1 -type d -name 'logs-e2e-tests-*' | sort)

  if [[ -z "${provider_dirs}" ]]; then
    echo "No provider artifact directories found matching pattern: ${artifacts_dir}/logs-e2e-tests-*"
    return 0
  fi

  echo "Found provider directories:"
  echo "${provider_dirs}"

  while IFS= read -r provider_dir; do
    local provider
    provider=$(basename "${provider_dir}" | sed 's/logs-e2e-tests-//')
    echo "Processing provider: ${provider} (${provider_dir})"

    local failed_tests=""
    if [[ -f "${provider_dir}/e2e-test-output.json" ]]; then
      echo "  Found e2e-test-output.json"
      failed_tests=$(jq -r 'select(.Action == "fail" and .Test != null) | .Test' "${provider_dir}/e2e-test-output.json" 2>/dev/null | sort -u || true)
      if [[ -n "${failed_tests}" ]]; then
        echo "  Failed tests: ${failed_tests}"
      else
        echo "  No failed tests found in JSON output"
      fi
    else
      echo "  No e2e-test-output.json found"
      ls -la "${provider_dir}" 2>/dev/null || true
    fi

    if [[ -n "${failed_tests}" ]]; then
      # Format each test name in backticks, join with ", "
      local formatted
      # shellcheck disable=SC2016
      formatted=$(echo "${failed_tests}" | sed 's/.*/ `&`/' | paste -sd ',' - | sed 's/,/, /g')
      # Append a line: *provider:* `Test1`, `Test2`
      failure_details="${failure_details}*${provider}:* ${formatted}
"
    fi
  done <<<"${provider_dirs}"

  if [[ -z "${failure_details}" ]]; then
    echo "No failures detected, skipping Slack notification"
    return 0
  fi

  # Strip trailing newline
  failure_details="${failure_details%$'\n'}"

  local run_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
  local commit_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/commit/${GITHUB_SHA}"
  local short_sha="${GITHUB_SHA:0:7}"

  local payload
  payload=$(jq -n \
    --arg repo "${GITHUB_REPOSITORY:-unknown}" \
    --arg branch "${GITHUB_REF_NAME:-unknown}" \
    --arg run_url "${run_url}" \
    --arg commit_url "${commit_url}" \
    --arg short_sha "${short_sha:-unknown}" \
    --arg details "${failure_details}" \
    '{
      "blocks": [
        {
          "type": "header",
          "text": {
            "type": "plain_text",
            "text": "E2E Test Failures",
            "emoji": true
          }
        },
        {
          "type": "context",
          "elements": [
            {
              "type": "mrkdwn",
              "text": ($repo + " | " + $branch + " | <" + $commit_url + "|" + $short_sha + ">")
            }
          ]
        },
        {
          "type": "section",
          "text": {
            "type": "mrkdwn",
            "text": $details
          }
        },
        {
          "type": "actions",
          "elements": [
            {
              "type": "button",
              "text": {
                "type": "plain_text",
                "text": "View Run",
                "emoji": true
              },
              "url": $run_url
            }
          ]
        }
      ]
    }')

  echo "Sending Slack notification..."
  curl -sf -X POST -H 'Content-type: application/json' --data "${payload}" "${SLACK_WEBHOOK_URL}"
  echo
  echo "Slack notification sent."
}

main "$@"
