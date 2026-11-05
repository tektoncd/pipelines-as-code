package pipelinerungroup

import (
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"gotest.tools/v3/assert"
)

func TestDecorate(t *testing.T) {
	tests := []struct {
		name     string
		groupID  string
		wantErr  string
		groupIDf string
	}{
		{
			name:     "a group member carries the group id",
			groupID:  "abc123",
			groupIDf: "abc123",
		},
		{
			name:    "an empty group id is rejected",
			groupID: "",
			wantErr: "group id must not be empty",
		},
		{
			name:    "a group id that is not label safe is rejected",
			groupID: "a/b c",
			wantErr: "is not a valid label value",
		},
		{
			name:     "dashes, dots and underscores are kept",
			groupID:  "a-b.c_d",
			groupIDf: "a-b.c_d",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := prTemplate("build", "", "")
			err := Decorate(pr, tt.groupID)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			assert.Equal(t, tt.groupIDf, GroupIDOf(pr))
			_, hasLabel := pr.GetLabels()[keys.State]
			assert.Assert(t, !hasLabel, "a plain group member must not carry a state label")
		})
	}
}

func TestSelectors(t *testing.T) {
	assert.Equal(t, "pipelinesascode.tekton.dev/group-id=abc-123", GroupSelector("abc-123"))
}

func TestNewGroupID(t *testing.T) {
	first := NewGroupID()
	second := NewGroupID()
	assert.Assert(t, len(first) > 0)
	assert.Assert(t, first != second, "group ids must be unique per event")
}

func TestFollowUpName(t *testing.T) {
	tests := []struct {
		name       string
		definition string
		groupID    string
		expected   string
		wantErr    string
	}{
		{
			name:       "definition and group id compose the run name, lowercased",
			definition: "Deploy",
			groupID:    "AbC123",
			expected:   "deploy-abc123",
		},
		{
			name:       "a run name of exactly 63 characters is accepted",
			definition: strings.Repeat("d", 52),
			groupID:    "abcdefghij",
			expected:   strings.Repeat("d", 52) + "-abcdefghij",
		},
		{
			name:       "a run name over the 63 characters of a PipelineRun name is rejected",
			definition: strings.Repeat("d", 53),
			groupID:    "abcdefghij",
			wantErr:    "the definition name must be at most 52 characters",
		},
		{
			name:       "a name with dots is a valid subdomain",
			definition: "deploy.v2",
			groupID:    "abc123",
			expected:   "deploy.v2-abc123",
		},
		{
			name:       "an underscore is rejected",
			definition: "deploy_prod",
			groupID:    "abc123",
			wantErr:    "is not a valid kubernetes name",
		},
		{
			name:       "an empty definition name is rejected",
			definition: "",
			groupID:    "abc123",
			wantErr:    "is not a valid kubernetes name",
		},
		{
			name:       "a definition ending with a dash is accepted",
			definition: "deploy-",
			groupID:    "abc123",
			expected:   "deploy--abc123",
		},
		{
			name:       "an invalid definition name is rejected",
			definition: "deploy/with/slashes",
			groupID:    "abc123",
			wantErr:    "is not a valid kubernetes name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FollowUpName(tt.definition, tt.groupID)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
