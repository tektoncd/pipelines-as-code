#!/usr/bin/env bash
# Unit tests for the test_pattern handling of hack/gh-workflow-ci.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/gh-workflow-ci.sh
source "${SCRIPT_DIR}/gh-workflow-ci.sh"
set +x

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

# write_events <file> <test name>...
# Write the gotestsum JSON events of a run where each test name started. List
# only what go test would start for the pattern: matching tests, their
# matching subtests, and parents of a subtest pattern.
write_events() {
  local file="$1" name
  shift
  : >"${file}"
  for name in "$@"; do
    printf '{"Action":"run","Package":"test","Test":"%s"}\n' "${name}" >>"${file}"
  done
}

failures=0
output="${tmpdir}/output.json"
while IFS=$'\t' read -r name pattern events want; do
  [[ -z ${name} ]] && continue
  ran_tests=()
  [[ ${events} != "-" ]] && IFS=',' read -r -a ran_tests <<<"${events}"
  write_events "${output}" ${ran_tests[@]+"${ran_tests[@]}"}

  got=1
  test_pattern_ran "${output}" "${pattern}" && got=0
  if [[ ${got} != "${want}" ]]; then
    echo "FAIL: ${name}: pattern '${pattern}' expected ${want}, got ${got}"
    failures=$((failures + 1))
  fi
done <<'EOF'
exact test matches	^TestGiteaWithCLIGeneratePipeline$	TestGiteaWithCLIGeneratePipeline	0
parent with nonexistent subtest does not match	^TestGiteaWithCLIGeneratePipeline$/DoesNotExist$	TestGiteaWithCLIGeneratePipeline	1
parent only with empty subtest regexp	^TestGiteaWithCLIGeneratePipeline$/^$	TestGiteaWithCLIGeneratePipeline	1
matching subtest selects the parent	^TestGiteaWithCLIGeneratePipeline$/^CLI$/	TestGiteaWithCLIGeneratePipeline,TestGiteaWithCLIGeneratePipeline/CLI	0
unanchored subtest path	^TestGiteaWithCLIGeneratePipeline$/CLI	TestGiteaWithCLIGeneratePipeline,TestGiteaWithCLIGeneratePipeline/CLI/foo	0
parent only with subtest pattern	^TestGiteaWithCLIGeneratePipeline$/^CLI$/	TestGiteaWithCLIGeneratePipeline	1
alternation matches one branch	^(TestGithubPush|TestGiteaPush)$	TestGiteaPush	0
slash inside a group is not a level	^(TestA/x|TestB)$	TestB	0
slash inside brackets is not a level	^Test[/]A$	TestA	0
escaped slash is not a level	^TestA\/B$	TestA	0
go-only syntax non-capturing group	^(?:TestGiteaWithCLI)$	TestGiteaWithCLI	0
go-only syntax case-insensitive flag	(?i)^testgiteawithcli$	TestGiteaWithCLI	0
pattern matches no test	^TestDoesNotExist$	-	1
alternatives of different depths, shallow one ran	^TestGiteaWithCLI$|^TestGiteaWithCLIGeneratePipeline$/DoesNotExist$	TestGiteaWithCLI	0
alternatives of different depths, none ran	^TestA$|^TestB$/^x$	-	1
pipe inside a group is not an alternative	^(TestA|TestB)$/^x$	TestA	1
EOF

if [[ ${failures} -gt 0 ]]; then
  echo "${failures} test(s) failed"
  exit 1
fi
echo "All hack/gh-workflow-ci.sh tests passed."
