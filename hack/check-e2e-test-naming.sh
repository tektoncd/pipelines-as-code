#!/usr/bin/env bash
# Validates that all E2E test functions follow the naming convention so they
# can be properly partitioned into CI jobs by hack/gh-workflow-ci.sh.
#
# Valid prefixes: TestGithub*, TestGitea*, TestGitlab*, TestBitbucket*, TestOthers*
# Concurrency tests (any name containing "Concurrency") are also allowed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Find all test files in the test directory (excluding subdirectories)
mapfile -t testfiles < <(find "${REPO_ROOT}/test/" -maxdepth 1 -name '*_test.go' 2>/dev/null)

if [[ ${#testfiles[@]} -eq 0 ]]; then
    echo "No test files found in ${REPO_ROOT}/test/"
    exit 0
fi

# Extract all Test* function names (excluding TestMain which is a Go test harness, not a test)
all_tests=$(grep -hE '^func[[:space:]]+Test[[:alnum:]_]+' "${testfiles[@]}" | sed -E 's/^func[[:space:]]+([[:alnum:]_]+).*/\1/' | grep -v '^TestMain$')

# Valid patterns: TestGithub*, TestGitea*, TestGitlab*, TestBitbucket*, TestOthers*, or *Concurrency*
valid_pattern='^Test(Github|Gitea|Gitlab|Bitbucket|Others)|Concurrency'

orphaned_tests=()
while IFS= read -r test; do
    [[ -z "${test}" ]] && continue
    if ! grep -qE "${valid_pattern}" <<< "${test}"; then
        orphaned_tests+=("${test}")
    fi
done <<< "${all_tests}"

if [[ ${#orphaned_tests[@]} -gt 0 ]]; then
    echo "ERROR: The following E2E tests do not follow the naming convention:"
    echo ""
    for test in "${orphaned_tests[@]}"; do
        # Find which file contains this test
        file=$(grep -l "func ${test}" "${testfiles[@]}" 2>/dev/null | head -1)
        echo "  - ${test} (in ${file:-unknown})"
    done
    echo ""
    echo "Tests must start with one of: TestGithub*, TestGitea*, TestGitlab*, TestBitbucket*, TestOthers*"
    echo "Or contain 'Concurrency' in the name for concurrency tests."
    echo ""
    echo "This ensures tests are properly assigned to CI jobs in hack/gh-workflow-ci.sh"
    exit 1
fi

echo "All E2E tests follow the naming convention."
