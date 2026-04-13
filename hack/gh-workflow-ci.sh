#!/usr/bin/env bash
# shellcheck disable=SC2038,SC2153
# Helper script for GitHub Actions CI, used from e2e tests.
set -exufo pipefail

export PAC_API_INSTRUMENTATION_DIR=/tmp/api-instrumentation
export TEST_GITLAB_API_URL=https://gitlab.pipelinesascode.com
export TEST_GITLAB_GROUP=pac-e2e-tests

create_pac_github_app_secret() {
  # Read from environment variables instead of arguments
  local app_private_key="${PAC_GITHUB_PRIVATE_KEY}"
  local application_id="${PAC_GITHUB_APPLICATION_ID}"
  local webhook_secret="${PAC_WEBHOOK_SECRET}"

  kubectl delete secret -n pipelines-as-code pipelines-as-code-secret || true
  kubectl -n pipelines-as-code create secret generic pipelines-as-code-secret \
    --from-literal github-private-key="${app_private_key}" \
    --from-literal github-application-id="${application_id}" \
    --from-literal webhook.secret="${webhook_secret}"
  kubectl patch configmap -n pipelines-as-code -p "{\"data\":{\"bitbucket-cloud-check-source-ip\": \"false\"}}" \
    --type merge pipelines-as-code

  # restart controller
  kubectl -n pipelines-as-code delete pod -l app.kubernetes.io/name=controller

  echo -n "Waiting for controller to restart"
  i=0
  while true; do
    [[ ${i} == 120 ]] && exit 1
    ep=$(kubectl get ep -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.subsets[*].addresses[*].ip}')
    [[ -n ${ep} ]] && break
    sleep 2
    echo -n "."
    i=$((i + 1))
  done
  echo
}

create_second_github_app_controller_on_ghe() {
  # Read from environment variables instead of arguments
  local test_github_second_smee_url="${TEST_GITHUB_SECOND_SMEE_URL}"
  local test_github_second_private_key="${TEST_GITHUB_SECOND_PRIVATE_KEY}"
  local test_github_second_application_id="${TEST_GITHUB_SECOND_APPLICATION_ID}"
  local test_github_second_webhook_secret="${TEST_GITHUB_SECOND_WEBHOOK_SECRET}"

  # install uv
  type -p uv >/dev/null 2>&1 || { curl -LsSf https://astral.sh/uv/install.sh | sh; }

  ./hack/second-controller.py \
    --controller-image="ko" \
    --smee-url="${test_github_second_smee_url}" \
    --ingress-domain="paac-127-0-0-1.nip.io" \
    --namespace="pipelines-as-code" \
    ghe | tee /tmp/generated.yaml

  ko apply --insecure-registry -f /tmp/generated.yaml
  kubectl delete secret -n pipelines-as-code ghe-secret || true
  kubectl -n pipelines-as-code create secret generic ghe-secret \
    --from-literal github-private-key="${test_github_second_private_key}" \
    --from-literal github-application-id="${test_github_second_application_id}" \
    --from-literal webhook.secret="${test_github_second_webhook_secret}"
  sed "s/name: pipelines-as-code/name: ghe-configmap/" <config/302-pac-configmap.yaml | kubectl apply -n pipelines-as-code -f-
  kubectl patch configmap -n pipelines-as-code ghe-configmap -p '{"data":{"application-name": "Pipelines as Code GHE"}}'
  kubectl -n pipelines-as-code delete pod -l app.kubernetes.io/name=ghe-controller
}

get_tests() {
  local target="$1"
  local -a testfiles
  local all_tests
  mapfile -t testfiles < <(find test/ -maxdepth 1 -name '*.go')
  all_tests=$(grep -hioP '^func[[:space:]]+Test[[:alnum:]_]+' "${testfiles[@]}" | sed -E 's/^func[[:space:]]+//')

  local -a gitea_tests=()
  if [[ "${target}" == *"gitea"* ]]; then
    # Filter Gitea tests, excluding Concurrency tests
    mapfile -t gitea_tests < <(echo "${all_tests}" | grep -iP '^TestGitea' 2>/dev/null | grep -ivP 'Concurrency' 2>/dev/null | sort 2>/dev/null)
    # Remove any non-Gitea entries that might have been captured
    local -a filtered_tests
    for test in "${gitea_tests[@]}"; do
      if [[ "${test}" =~ ^TestGitea ]] && [[ ! "${test}" =~ Concurrency ]]; then
        filtered_tests+=("${test}")
      fi
    done
    gitea_tests=("${filtered_tests[@]}")
  fi

  local -a ghe_tests=()
  if [[ "${target}" == github_ghe* ]]; then
    mapfile -t ghe_tests < <(echo "${all_tests}" | grep -iP 'GithubGHE' 2>/dev/null | grep -ivP 'Concurrency' 2>/dev/null | sort 2>/dev/null)
  fi

  local -a github_tests=()
  if [[ "${target}" == *"github"* ]] && [[ "${target}" != github_ghe* ]] && [[ "${target}" != "github_second_controller" ]]; then
    mapfile -t github_tests < <(echo "${all_tests}" | grep -iP '^TestGithub' 2>/dev/null | grep -ivP 'Concurrency|GithubGHE' 2>/dev/null | sort 2>/dev/null)
  fi

  # Calculate chunk sizes for splitting gitea tests into 3 parts
  local chunk_size remainder
  if [[ ${#gitea_tests[@]} -gt 0 ]]; then
    chunk_size=$((${#gitea_tests[@]} / 3))
    remainder=$((${#gitea_tests[@]} % 3))
  fi

  # Calculate chunk sizes for splitting GHE tests into 3 parts
  local ghe_chunk_size ghe_remainder
  if [[ ${#ghe_tests[@]} -gt 0 ]]; then
    ghe_chunk_size=$((${#ghe_tests[@]} / 3))
    ghe_remainder=$((${#ghe_tests[@]} % 3))
  fi

  # TODO: revert once the new workflow matrix lands on main.
  # Backward compat: github_1/github_2 chunking for pull_request_target
  # which runs the workflow YAML from main (old target names).
  local github_chunk_size github_remainder
  if [[ ${#github_tests[@]} -gt 0 ]]; then
    github_chunk_size=$((${#github_tests[@]} / 2))
    github_remainder=$((${#github_tests[@]} % 2))
  fi

  case "${target}" in
  flaky)
    # no-op: flaky tests have been absorbed into their natural categories.
    # Kept for backward compat since pull_request_target uses main's YAML
    # which still references 'flaky'.
    ;;

  concurrency)
    printf '%s\n' "${all_tests}" | grep -iP 'Concurrency|Others'
    ;;
  github_public)
    if [[ ${#github_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${github_tests[@]}"
    fi
    ;;
  # TODO: revert - remove github_1, github_2, github_second_controller aliases
  # once the new workflow matrix lands on main. These exist because
  # pull_request_target runs the workflow YAML from main which still sends old
  # target names.
  github_1)
    if [[ ${#github_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${github_tests[@]:0:$((github_chunk_size + github_remainder))}"
    fi
    ;;
  github_2)
    if [[ ${#github_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${github_tests[@]:$((github_chunk_size + github_remainder))}"
    fi
    ;;
  # TODO: revert - remove github_second_controller, github_ghe aliases
  # once the new workflow matrix lands on main. These exist because
  # pull_request_target runs the workflow YAML from main which still sends old
  # target names.
  github_second_controller | github_ghe)
    printf '%s\n' "${all_tests}" | grep -iP 'GithubGHE' | grep -ivP 'Concurrency'
    ;;
  github_ghe_1)
    if [[ ${#ghe_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${ghe_tests[@]:0:${ghe_chunk_size}}"
    fi
    ;;
  github_ghe_2)
    if [[ ${#ghe_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${ghe_tests[@]:${ghe_chunk_size}:${ghe_chunk_size}}"
    fi
    ;;
  github_ghe_3)
    if [[ ${#ghe_tests[@]} -gt 0 ]]; then
      local start_idx=$((ghe_chunk_size * 2))
      printf '%s\n' "${ghe_tests[@]:${start_idx}:$((ghe_chunk_size + ghe_remainder))}"
    fi
    ;;
  gitlab_bitbucket)
    printf '%s\n' "${all_tests}" | grep -iP 'Gitlab|Bitbucket' | grep -ivP 'Concurrency'
    ;;
  gitea_1)
    if [[ ${#gitea_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${gitea_tests[@]:0:${chunk_size}}"
    fi
    ;;
  gitea_2)
    if [[ ${#gitea_tests[@]} -gt 0 ]]; then
      printf '%s\n' "${gitea_tests[@]:${chunk_size}:${chunk_size}}"
    fi
    ;;
  gitea_3)
    if [[ ${#gitea_tests[@]} -gt 0 ]]; then
      local start_idx=$((chunk_size * 2))
      printf '%s\n' "${gitea_tests[@]:${start_idx}:$((chunk_size + remainder))}"
    fi
    ;;
  *)
    echo "Invalid target: ${target}"
    echo "supported targets: github_public, github_ghe_1, github_ghe_2, github_ghe_3, gitlab_bitbucket, gitea_1, gitea_2, gitea_3, concurrency, flaky"
    echo "backward compat aliases: github_1, github_2, github_second_controller, github_ghe"
    ;;
  esac
}

run_e2e_tests() {
  set +x
  target="${TEST_PROVIDER}"
  export PAC_E2E_KEEP_NS=true

  mapfile -t tests < <(get_tests "${target}")
  echo "About to run ${#tests[@]} tests: ${tests[*]}"

  if [[ ${#tests[@]} -eq 0 || (-z "${tests[0]}" && ${#tests[@]} -eq 1) ]]; then
    echo "No tests to run for target '${target}', exiting successfully."
    return 0
  fi

  mkdir -p /tmp/logs

  local test_pattern
  local test_status=0
  local raw_output=/tmp/logs/e2e-test-output.json

  # shellcheck disable=SC2001
  test_pattern="$(echo "${tests[*]}" | sed 's/ /|/g')"
  ./hack/install-gotestsum.sh 1.13.0 "${HOME}/go/bin"
  env GODEBUG=asynctimerchan=1 \
    gotestsum --format standard-verbose --jsonfile "${raw_output}" -- \
    -race -failfast -timeout 45m -count=1 -tags=e2e -run "${test_pattern}" ./test || test_status=$?
  if ! TESTRR_RUN_LABEL="${TESTRR_RUN_LABEL:-gha-e2e-${target}}" ./hack/upload-testrr.sh "${raw_output}"; then
    echo "::warning::testrr upload failed; continuing without failing GitHub Actions"
  fi
  return "${test_status}"
}

output_logs() {
  if command -v "snazy" >/dev/null 2>&1; then
    snazy --extra-fields --skip-line-regexp="^(Reconcile (succeeded|error)|Updating webhook)" -f error -f fatal /tmp/logs/pac-pods.log
  else
    # snazy for the poors
    python -c "import sys,json,datetime; [print(f'• { (lambda t: datetime.datetime.fromisoformat(t.rstrip(\"Z\")).strftime(\"%H:%M:%S\") if isinstance(t,str) else datetime.datetime.fromtimestamp(t).strftime(\"%H:%M:%S\"))(json.loads(l.strip())[\"ts\"] )} {json.loads(l.strip()).get(\"msg\",\"\")}') if l.strip().startswith('{') else print(l.strip()) for l in sys.stdin]" \
      </tmp/logs/pac-pods.log
  fi
}

collect_logs() {
  # Read from environment variables (use default empty value for optional vars)
  local test_gitea_smee_url="${TEST_GITEA_SMEEURL:-}"
  local github_ghe_smee_url="${TEST_GITHUB_SECOND_SMEE_URL:-}"
  local test_gitlab_smee_url="${TEST_GITLAB_SMEEURL:-}"

  mkdir -p /tmp/logs
  # Output logs to stdout so we can see via the web interface directly
  kubectl logs -n pipelines-as-code -l app.kubernetes.io/part-of=pipelines-as-code \
    --all-containers=true --tail=1000 >/tmp/logs/pac-pods.log
  kind export logs /tmp/logs

  # Collect all gosmee data in organized directory
  mkdir -p /tmp/logs/gosmee
  [[ -d /tmp/gosmee-replay ]] && cp -a /tmp/gosmee-replay /tmp/logs/gosmee/replay
  [[ -d /tmp/gosmee-replay-ghe ]] && cp -a /tmp/gosmee-replay-ghe /tmp/logs/gosmee/replay-ghe
  [[ -f /tmp/gosmee-main.log ]] && cp /tmp/gosmee-main.log /tmp/logs/gosmee/main.log
  [[ -f /tmp/gosmee-ghe.log ]] && cp /tmp/gosmee-ghe.log /tmp/logs/gosmee/ghe.log
  [[ -d /tmp/gosmee-replay-gitlab ]] && cp -a /tmp/gosmee-replay-gitlab /tmp/logs/gosmee/replay-gitlab
  [[ -f /tmp/gosmee-gitlab.log ]] && cp /tmp/gosmee-gitlab.log /tmp/logs/gosmee/gitlab.log

  kubectl get pipelineruns -A -o yaml >/tmp/logs/pac-pipelineruns.yaml
  kubectl get repositories.pipelinesascode.tekton.dev -A -o yaml >/tmp/logs/pac-repositories.yaml
  kubectl get configmap -n pipelines-as-code -o yaml >/tmp/logs/pac-configmap
  kubectl get events -A >/tmp/logs/events

  allNamespaces=$(kubectl get namespaces -o jsonpath='{.items[*].metadata.name}')
  for ns in ${allNamespaces}; do
    mkdir -p /tmp/logs/ns/${ns}
    for type in pods pipelineruns repositories configmap; do
      kubectl get ${type} -n ${ns} -o yaml >/tmp/logs/ns/${ns}/${type}.yaml
    done
    kubectl -n ${ns} get events >/tmp/logs/ns/${ns}/events
  done

  if [[ -d ${PAC_API_INSTRUMENTATION_DIR} && -n "$(ls -A ${PAC_API_INSTRUMENTATION_DIR})" ]]; then
    echo "Copying API instrumentation logs from ${PAC_API_INSTRUMENTATION_DIR}"
    cp -a ${PAC_API_INSTRUMENTATION_DIR} /tmp/logs/$(basename ${PAC_API_INSTRUMENTATION_DIR})
  fi

  for url in "${test_gitea_smee_url}" "${github_ghe_smee_url}" "${test_gitlab_smee_url}"; do
    [[ -z "${url}" ]] && continue
    find /tmp/logs -type f -exec grep -l "${url}" {} \; | xargs -r sed -i "s|${url}|SMEE_URL|g"
  done

  detect_panic
}

generate_github_summary() {
  local raw_output="/tmp/logs/e2e-test-output.json"
  local target="${TEST_PROVIDER:-unknown}"

  if [[ -z "${GITHUB_STEP_SUMMARY:-}" ]]; then
    echo "GITHUB_STEP_SUMMARY not set, skipping summary generation"
    return 0
  fi

  if [[ ! -f "${raw_output}" ]]; then
    echo "No test output found at ${raw_output}, skipping summary generation"
    return 0
  fi

  python3 ./hack/generate_github_summary.py "${raw_output}" "${target}" >>"${GITHUB_STEP_SUMMARY}"
}

detect_panic() {
  # shellcheck disable=SC2016
  (find /tmp/logs/ -type f -regex '.*/pipelines-as-code.*/[0-9]\.log$' | xargs -r sed -n '/stderr F panic:.*/,$p' | head -n 80) >/tmp/panic.log
  if [[ -s /tmp/panic.log ]]; then
    set +x
    echo "=====================  PANIC DETECTED ====================="
    echo "**********************************************************************"
    cat /tmp/panic.log
    echo "**********************************************************************"
    exit 1
  fi
}

help() {
  cat <<EOF
  Usage: $0 <command>

  Shell script to run e2e tests from GitHub Actions CI

  Required environment variables depend on the command being executed.

  create_pac_github_app_secret
    Create the secret for the github app
    Required env vars: PAC_GITHUB_PRIVATE_KEY, PAC_GITHUB_APPLICATION_ID, PAC_WEBHOOK_SECRET

  create_second_github_app_controller_on_ghe
    Create the second controller on GHE
    Required env vars: TEST_GITHUB_SECOND_SMEE_URL, TEST_GITHUB_SECOND_PRIVATE_KEY, TEST_GITHUB_SECOND_WEBHOOK_SECRET

  run_e2e_tests
    Run the e2e tests
    Required env vars: TEST_PROVIDER plus many test-specific environment variables

  collect_logs
    Collect logs from the cluster
    Optional env vars: TEST_GITEA_SMEEURL, TEST_GITHUB_SECOND_SMEE_URL (for scrubbing URLs from logs)

  output_logs
    Will output logs using snazzy formatting when available or otherwise through a simple
    python formatter. This makes debugging easier from the GitHub Actions interface.

  generate_github_summary
    Parse gotestsum JSON output and write a markdown summary to GITHUB_STEP_SUMMARY.
    Required env vars: TEST_PROVIDER, GITHUB_STEP_SUMMARY

  print_tests
    Print the list of tests that would be run for each provider target.

EOF
}

case ${1-""} in
create_pac_github_app_secret)
  create_pac_github_app_secret
  ;;
create_second_github_app_controller_on_ghe)
  create_second_github_app_controller_on_ghe
  ;;
run_e2e_tests)
  run_e2e_tests
  ;;
collect_logs)
  collect_logs
  ;;
output_logs)
  output_logs
  ;;
generate_github_summary)
  generate_github_summary
  ;;
print_tests)
  set +x
  for target in github_public github_ghe_1 github_ghe_2 github_ghe_3 gitlab_bitbucket gitea_1 gitea_2 gitea_3 concurrency flaky; do
    mapfile -t tests < <(get_tests "${target}")
    echo "Tests for target: ${target} Total: ${#tests[@]}"
    printf '%s\n' "${tests[@]}"
    echo
  done
  ;;
help)
  help
  exit 0
  ;;
*)
  echo "Unknown command ${1-}"
  help
  exit 1
  ;;
esac
