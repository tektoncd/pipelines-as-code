#!/usr/bin/env bash
# Collect Go binary coverage from PAC components running on a kind/k8s
# cluster reachable via the current kubeconfig, while running e2e tests
# against it. No assumption about how the cluster was set up (startpaac,
# manual kind, ...): the registry is auto-detected from the running
# deployment and node access goes through a privileged helper pod.
#
# Usage:
#   ./hack/dev/e2e-coverage.sh deploy     # redeploy instrumented PAC via ko (+GOCOVERDIR)
#   ./hack/dev/e2e-coverage.sh collect    # flush + gather coverage from the cluster
#   ./hack/dev/e2e-coverage.sh report     # textfmt + html report (merges unit if present)
#   ./hack/dev/e2e-coverage.sh clean      # remove coverage dirs (node + local)
#
# Typical flow:
#   ./hack/dev/e2e-coverage.sh deploy
#   make test-e2e GO_TEST_FLAGS="-run TestGiteaPush"   # or any e2e run
#   ./hack/dev/e2e-coverage.sh collect
#   ./hack/dev/e2e-coverage.sh report
#
# Environment:
#   KO_DOCKER_REPO  registry for ko builds (default: auto-detected from the
#                   running controller deployment image)
#   KO_EXTRA_FLAGS  extra flags for ko apply (e.g. --insecure-registry)
#   COMPONENTS      space-separated list (default: "controller watcher webhook")
#   OUTPUT_DIR      local output dir (default: ./tmp/e2e-coverage)
set -euo pipefail

COMPONENTS=${COMPONENTS:-"controller watcher webhook"}
OUTPUT_DIR=${OUTPUT_DIR:-./tmp/e2e-coverage}
KO_EXTRA_FLAGS=${KO_EXTRA_FLAGS:-}
NAMESPACE=pipelines-as-code
NODE_COVER_DIR=/tmp/gocover
HELPER_POD=gocover-helper

info() { echo -e "\033[1;32m>>>\033[0m $*"; }
die() {
	echo -e "\033[1;31mERROR:\033[0m $*" >&2
	exit 1
}

# Privileged helper pod mounting the node's coverage dir; all node-side
# operations go through it so this works wherever the kind node runs
# (local docker, remote host, ...).
helper_up() {
	kubectl -n ${NAMESPACE} get pod ${HELPER_POD} >/dev/null 2>&1 && return
	kubectl -n ${NAMESPACE} apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${HELPER_POD}
spec:
  terminationGracePeriodSeconds: 0
  containers:
    - name: helper
      image: busybox
      command: ["sleep", "infinity"]
      securityContext:
        privileged: true
      volumeMounts:
        - name: host-tmp
          mountPath: /host-tmp
  volumes:
    - name: host-tmp
      hostPath:
        path: /tmp
        type: Directory
EOF
	kubectl -n ${NAMESPACE} wait --for=condition=Ready pod/${HELPER_POD} --timeout=120s
}

helper_exec() {
	kubectl -n ${NAMESPACE} exec ${HELPER_POD} -- sh -c "$*"
}

helper_down() {
	kubectl -n ${NAMESPACE} delete pod ${HELPER_POD} --ignore-not-found --wait=false
}

component_config() {
	case $1 in
	controller) echo config/400-controller.yaml ;;
	watcher) echo config/500-watcher.yaml ;;
	webhook) echo config/600-webhook.yaml ;;
	*) die "unknown component: $1" ;;
	esac
}

detect_registry() {
	local image
	image=$(kubectl -n ${NAMESPACE} get deployment pipelines-as-code-controller \
		-o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null) ||
		die "cannot read controller deployment; is PAC installed in ${NAMESPACE}?"
	echo "${image%%/*}"
}

deploy() {
	command -v ko >/dev/null || die "ko is required (https://ko.build)"
	if [[ -z ${KO_DOCKER_REPO:-} ]]; then
		KO_DOCKER_REPO=$(detect_registry)
		info "Auto-detected registry: ${KO_DOCKER_REPO}"
	fi
	export KO_DOCKER_REPO

	local node_arch platform
	node_arch=$(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')
	platform="linux/${node_arch}"
	info "Building for node platform: ${platform}"

	info "Redeploying PAC with coverage instrumentation (GOFLAGS=-cover)"
	for component in ${COMPONENTS}; do
		# shellcheck disable=SC2086
		env GOFLAGS=-cover ko apply -f "$(component_config "${component}")" \
			--platform="${platform}" -B --sbom=none ${KO_EXTRA_FLAGS}
	done

	info "Creating coverage dirs on the node"
	helper_up
	for component in ${COMPONENTS}; do
		# distroless nonroot UID; a root-owned dir would be unwritable
		helper_exec "mkdir -p /host-tmp/gocover/${component} && chown 65532:65532 /host-tmp/gocover/${component} && chmod 0770 /host-tmp/gocover/${component}"
	done

	info "Patching deployments with GOCOVERDIR + hostPath volume"
	for component in ${COMPONENTS}; do
		kubectl -n ${NAMESPACE} patch deployment "pipelines-as-code-${component}" --type=strategic -p "{
      \"spec\": {\"template\": {\"spec\": {
        \"terminationGracePeriodSeconds\": 90,
        \"containers\": [{\"name\": \"pac-${component}\",
          \"env\": [{\"name\": \"GOCOVERDIR\", \"value\": \"/coverage\"}],
          \"volumeMounts\": [{\"name\": \"gocover\", \"mountPath\": \"/coverage\"}]}],
        \"volumes\": [{\"name\": \"gocover\", \"hostPath\": {\"path\": \"${NODE_COVER_DIR}/${component}\", \"type\": \"Directory\"}}]
      }}}}"
	done

	for component in ${COMPONENTS}; do
		kubectl -n ${NAMESPACE} rollout status "deployment/pipelines-as-code-${component}" --timeout=120s
	done
	info "Done. Run your e2e tests, then: $0 collect"
}

collect() {
	info "Scaling down instrumented deployments (SIGTERM flushes coverage)"
	for component in ${COMPONENTS}; do
		kubectl -n ${NAMESPACE} scale deployment "pipelines-as-code-${component}" --replicas=0
	done
	for component in ${COMPONENTS}; do
		kubectl -n ${NAMESPACE} wait --for=delete pod \
			-l "app.kubernetes.io/name=${component}" --timeout=120s 2>/dev/null || true
	done

	info "Copying coverage data from the node"
	helper_up
	rm -rf "${OUTPUT_DIR}/data"
	for component in ${COMPONENTS}; do
		mkdir -p "${OUTPUT_DIR}/data/${component}"
		kubectl -n ${NAMESPACE} exec ${HELPER_POD} -- \
			tar cf - -C "/host-tmp/gocover/${component}" . 2>/dev/null |
			tar xf - -C "${OUTPUT_DIR}/data/${component}" || true
		count=$(find "${OUTPUT_DIR}/data/${component}" -name 'covcounters.*' | wc -l)
		info "${component}: ${count} covcounters file(s)"
	done
	helper_down

	find "${OUTPUT_DIR}/data" -name 'covmeta.*' | grep -q . ||
		die "no coverage data found; was PAC deployed with '$0 deploy'?"
	find "${OUTPUT_DIR}/data" -name 'covcounters.*' | grep -q . ||
		die "covmeta found but no covcounters: processes did not exit gracefully (coverage not flushed)"

	info "Scaling deployments back up"
	for component in ${COMPONENTS}; do
		kubectl -n ${NAMESPACE} scale deployment "pipelines-as-code-${component}" --replicas=1
	done
	info "Done. Generate a report with: $0 report"
}

report() {
	local dirs
	dirs=$(find "${OUTPUT_DIR}/data" -mindepth 1 -maxdepth 1 -type d 2>/dev/null |
		while read -r d; do ls "${d}"/covmeta.* >/dev/null 2>&1 && echo "${d}"; done | paste -sd,)
	[[ -n ${dirs} ]] || die "no coverage data in ${OUTPUT_DIR}/data; run '$0 collect' first"

	info "Converting to text profile"
	go tool covdata textfmt -i="${dirs}" -o "${OUTPUT_DIR}/e2e-coverage.txt"

	info "Coverage by package (e2e only):"
	go tool covdata percent -i="${dirs}" | grep -v '0.0%' || true

	go tool cover -html="${OUTPUT_DIR}/e2e-coverage.txt" -o "${OUTPUT_DIR}/e2e-coverage.html"
	info "E2E report: ${OUTPUT_DIR}/e2e-coverage.html"

	# Merge with unit coverage when unit tests also wrote binary coverage:
	#   mkdir -p tmp/e2e-coverage/unit && \
	#     go test -cover ./pkg/... -args -test.gocoverdir=$PWD/tmp/e2e-coverage/unit
	if ls "${OUTPUT_DIR}/unit"/covmeta.* >/dev/null 2>&1; then
		info "Merging with unit-test coverage"
		go tool covdata textfmt -i="${dirs},${OUTPUT_DIR}/unit" -o "${OUTPUT_DIR}/combined-coverage.txt"
		go tool cover -html="${OUTPUT_DIR}/combined-coverage.txt" -o "${OUTPUT_DIR}/combined-coverage.html"
		go tool cover -func="${OUTPUT_DIR}/combined-coverage.txt" | tail -1
		info "Combined report: ${OUTPUT_DIR}/combined-coverage.html"
	else
		info "No unit binary coverage found in ${OUTPUT_DIR}/unit — see comment in this script to generate it"
	fi
}

clean() {
	helper_up
	helper_exec "rm -rf /host-tmp/gocover"
	helper_down
	rm -rf "${OUTPUT_DIR}"
	info "Cleaned"
}

case ${1:-""} in
deploy | collect | report | clean) "$1" ;;
*)
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
	exit 1
	;;
esac
