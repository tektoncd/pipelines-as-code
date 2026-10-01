package status

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// BreakdownStart is how the step breakdown of a task begins in the status
// reported to the providers able to fold content, the name of the task is the
// summary of the folded block holding its steps.
const BreakdownStart = "<details><summary>"

// StepLink is how a step shows in the breakdown, as the text of the link to its
// logs.
func StepLink(title string) string {
	return "[" + title + "]("
}

// AssertStepBreakdown checks that the step breakdown of a status report shows
// the steps we expect and none of the ones we do not want, like the steps which
// have been skipped after a failure. The steps are given by the title shown to
// the user.
func AssertStepBreakdown(t *testing.T, body string, wantSteps, dontWantSteps []string) {
	t.Helper()
	_, breakdown, found := strings.Cut(body, BreakdownStart)
	assert.Assert(t, found, "no step breakdown in the status report: %s", body)
	breakdown, _, _ = strings.Cut(breakdown, "</details>")
	for _, step := range wantSteps {
		assert.Assert(t, strings.Contains(breakdown, StepLink(step)), "step %s is not in the breakdown: %s", step, breakdown)
	}
	for _, step := range dontWantSteps {
		assert.Assert(t, !strings.Contains(breakdown, StepLink(step)), "step %s should not be in the breakdown: %s", step, breakdown)
	}
}
