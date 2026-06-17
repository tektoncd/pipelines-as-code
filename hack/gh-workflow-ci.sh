#!/usr/bin/env bash
# Helper script for GitHub Actions CI, used from e2e tests.
set -eufo pipefail

create_pac_github_app_secret() {
	local app_private_key="${1}"
	local application_id="${2}"
	local webhook_secret="${3}"
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
	local test_github_second_smee_url="${1}"
	local test_github_second_private_key="${2}"
	local test_github_second_webhook_secret="${3}"

	# this is to handle compatibilty until the PR #1518 is merged
	[[ -e ./hack/second-controller.py ]] || exit 0
	python3 -m pip install PyYAML
	./hack/second-controller.py \
		--controller-image="ko" \
		--smee-url="${test_github_second_smee_url}" \
		--ingress-domain="paac-127-0-0-1.nip.io" \
		--namespace="pipelines-as-code" \
		ghe | tee /tmp/generated.yaml

	ko apply -f /tmp/generated.yaml
	kubectl delete secret -n pipelines-as-code ghe-secret || true
	kubectl -n pipelines-as-code create secret generic ghe-secret \
		--from-literal github-private-key="${test_github_second_private_key}" \
		--from-literal github-application-id="2" \
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
		mapfile -t gitea_tests < <(echo "${all_tests}" | grep -iP '^TestGitea' 2>/dev/null | grep -ivP 'Concurrency' 2>/dev/null | sort 2>/dev/null)
		local -a filtered_tests
		for test in "${gitea_tests[@]}"; do
			if [[ "${test}" =~ ^TestGitea ]] && [[ ! "${test}" =~ Concurrency ]]; then
				filtered_tests+=("${test}")
			fi
		done
		gitea_tests=("${filtered_tests[@]}")
	fi

	local chunk_size remainder
	if [[ ${#gitea_tests[@]} -gt 0 ]]; then
		chunk_size=$((${#gitea_tests[@]} / 3))
		remainder=$((${#gitea_tests[@]} % 3))
	fi

	case "${target}" in
	flaky)
		# no-op: kept for backward compat since pull_request_target uses main's YAML
		;;
	concurrency)
		printf '%s\n' "${all_tests}" | grep -iP 'Concurrency|Others'
		;;
	github_public | github_1 | github_2)
		printf '%s\n' "${all_tests}" | grep -iP 'Github' | grep -ivP 'Concurrency|GHE|Second' | grep -ivP 'Gitea'
		;;
	github_ghe_1 | github_ghe_2 | github_ghe_3 | github_second_controller | github_ghe)
		printf '%s\n' "${all_tests}" | grep -iP 'GHE|Second' | grep -ivP 'Concurrency'
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
	# backward compat for v0.27.x workflow YAML
	providers)
		printf '%s\n' "${all_tests}" | grep -iP 'Github|Gitlab|Bitbucket' | grep -ivP 'Concurrency'
		;;
	gitea_others)
		printf '%s\n' "${all_tests}" | grep -ivP 'Github|Gitlab|Bitbucket'
		;;
	*)
		echo "Invalid target: ${target}"
		echo "supported targets: github_public, github_ghe_1, github_ghe_2, github_ghe_3, gitlab_bitbucket, gitea_1, gitea_2, gitea_3, concurrency, flaky"
		echo "backward compat aliases: github_1, github_2, github_second_controller, github_ghe, providers, gitea_others"
		;;
	esac
}

run_e2e_tests() {
	# Accept secrets as positional args (v0.27.x workflow) or env vars (v0.37.x+ workflow)
	local bitbucket_cloud_token="${1:-${TEST_BITBUCKET_CLOUD_TOKEN:-}}"
	local webhook_secret="${2:-${TEST_EL_WEBHOOK_SECRET:-}}"
	local test_gitea_smeeurl="${3:-${TEST_GITEA_SMEEURL:-}}"
	local installation_id="${4:-${TEST_GITHUB_REPO_INSTALLATION_ID:-}}"
	local gh_apps_token="${5:-${TEST_GITHUB_TOKEN:-}}"
	local test_github_second_token="${6:-${TEST_GITHUB_SECOND_TOKEN:-}}"
	local gitlab_token="${7:-${TEST_GITLAB_TOKEN:-}}"

	# Nothing specific to webhook here it  just that repo is private in that org and that's what we want to test
	export TEST_GITHUB_PRIVATE_TASK_URL="https://github.com/openshift-pipelines/pipelines-as-code-e2e-tests-private/blob/main/remote_task.yaml"
	export TEST_GITHUB_PRIVATE_TASK_NAME="task-remote"

	export GO_TEST_FLAGS="-v -race -failfast"

	export TEST_BITBUCKET_CLOUD_API_URL=https://api.bitbucket.org/2.0
	export TEST_BITBUCKET_CLOUD_E2E_REPOSITORY=cboudjna/pac-e2e-tests
	export TEST_BITBUCKET_CLOUD_TOKEN="${bitbucket_cloud_token}"
	export TEST_BITBUCKET_CLOUD_USER=cboudjna

	export TEST_EL_URL="http://${CONTROLLER_DOMAIN_URL:-localhost}"
	export TEST_EL_WEBHOOK_SECRET="${webhook_secret}"

	export TEST_GITEA_API_URL="http://localhost:3000"
	## This is the URL used to forward requests from the webhook to the paac controller
	## badly named!
	export TEST_GITEA_SMEEURL="${test_gitea_smeeurl}"
	export TEST_GITEA_USERNAME=pac
	export TEST_GITEA_PASSWORD=pac
	export TEST_GITEA_REPO_OWNER=pac/pac

	export TEST_GITHUB_API_URL=api.github.com
	export TEST_GITHUB_REPO_INSTALLATION_ID="${installation_id}"
	export TEST_GITHUB_REPO_OWNER_GITHUBAPP=openshift-pipelines/pipelines-as-code-e2e-tests
	export TEST_GITHUB_REPO_OWNER_WEBHOOK=openshift-pipelines/pipelines-as-code-e2e-tests-webhook
	export TEST_GITHUB_TOKEN="${gh_apps_token}"

	export TEST_GITHUB_SECOND_API_URL=ghe.pipelinesascode.com
	export TEST_GITHUB_SECOND_EL_URL=http://ghe.paac-127-0-0-1.nip.io
	export TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP=pipelines-as-code/e2e
	# TODO: webhook repo for second github
	# export TEST_GITHUB_SECOND_REPO_OWNER_WEBHOOK=openshift-pipelines/pipelines-as-code-e2e-tests-webhook
	export TEST_GITHUB_SECOND_REPO_INSTALLATION_ID=1
	export TEST_GITHUB_SECOND_TOKEN="${test_github_second_token}"

	export TEST_GITLAB_API_URL="https://gitlab.com"
	export TEST_GITLAB_PROJECT_ID="34405323"
	export TEST_GITLAB_TOKEN="${gitlab_token}"
	# https://gitlab.com/gitlab-com/alliances/ibm-red-hat/sandbox/openshift-pipelines/pac-e2e-tests

	# Use TEST_PROVIDER if set (matrix-based workflow), otherwise run all tests
	local target="${TEST_PROVIDER:-all}"

	if [[ "${target}" == "all" ]]; then
		make test-e2e
		return $?
	fi

	set +x
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
	if command -v gotestsum >/dev/null 2>&1; then
		gotestsum --format standard-verbose --jsonfile "${raw_output}" -- \
			-race -failfast -timeout 45m -count=1 -tags=e2e -run "${test_pattern}" ./test || test_status=$?
	else
		# shellcheck disable=SC2001
		make test-e2e GO_TEST_FLAGS="-v -run \"${test_pattern}\"" || test_status=$?
	fi
	return "${test_status}"
}

collect_logs() {
	mkdir -p /tmp/logs
	kind export logs /tmp/logs

	mkdir -p /tmp/logs/gosmee
	[[ -d /tmp/gosmee-replay ]] && cp -a /tmp/gosmee-replay /tmp/logs/gosmee/replay

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
}

generate_github_summary() {
	echo "generate_github_summary: skipped on backport branch"
}

help() {
	cat <<EOF
  Usage: $0 <command> [args]

  Shell script to run e2e tests from GitHub Actions CI

  create_pac_github_app_secret <application_id> <app_private_key> <webhook_secret>
    Create the secret for the github app

  create_second_github_app_controller_on_ghe <test_github_second_smee_url> <test_github_second_private_key> <test_github_second_webhook_secret>
    Create the second controller on GHE

  run_e2e_tests <bitbucket_cloud_token> <webhook_secret> <test_gitea_smeeurl> <installation_id> <gh_apps_token> <test_github_second_token> <gitlab_token>
    Run the e2e tests

  collect_logs
    Collect logs from the cluster

  generate_github_summary
    No-op on backport branch.

  print_tests
    Print the list of tests that would be run for each provider target.
EOF
}

case ${1-""} in
create_pac_github_app_secret)
	create_pac_github_app_secret "${2}" "${3}" "${4}"
	;;
create_second_github_app_controller_on_ghe)
	create_second_github_app_controller_on_ghe "${2}" "${3}" "${4}"
	;;
run_e2e_tests)
	run_e2e_tests "${2-}" "${3-}" "${4-}" "${5-}" "${6-}" "${7-}" "${8-}"
	;;
collect_logs)
	collect_logs
	;;
generate_github_summary)
	generate_github_summary
	;;
print_tests)
	set +x
	for target in github_public github_ghe_1 github_ghe_2 github_ghe_3 gitlab_bitbucket gitea_1 gitea_2 gitea_3 concurrency flaky providers gitea_others; do
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
