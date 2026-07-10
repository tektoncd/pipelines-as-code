package triggertype

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestIsPullRequestTypeWithReview(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "pull request review normalizes to pull request",
			input:  PullRequestReview.String(),
			expect: PullRequest.String(),
		},
		{
			name:   "pull request stays pull request",
			input:  PullRequest.String(),
			expect: PullRequest.String(),
		},
		{
			name:   "push stays push",
			input:  Push.String(),
			expect: Push.String(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPullRequestType(tt.input)
			assert.Equal(t, tt.expect, result.String())
		})
	}
}

func TestStringToTypeWithReview(t *testing.T) {
	result := StringToType(PullRequestReview.String())
	assert.Equal(t, PullRequestReview, result)
}

func TestTriggerIsPullRequest(t *testing.T) {
	tests := []struct {
		name    string
		trigger Trigger
		expect  bool
	}{
		{
			name:    "pull request is pull request",
			trigger: PullRequest,
			expect:  true,
		},
		{
			name:    "pull request review is pull request",
			trigger: PullRequestReview,
			expect:  true,
		},
		{
			name:    "push is not pull request",
			trigger: Push,
			expect:  false,
		},
		{
			name:    "ok to test is pull request",
			trigger: OkToTest,
			expect:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.trigger.IsPullRequest())
		})
	}
}
