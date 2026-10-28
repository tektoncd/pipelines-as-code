package webhook

import (
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	"gotest.tools/v3/assert"
)

func TestGetProviderName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "github",
			url:  "https://github.com/pac/demo",
			want: "github",
		},
		{
			name: "gitlab",
			url:  "https://gitlab.com/pac/demo",
			want: "gitlab",
		},
		{
			name: "bitbucket cloud",
			url:  "https://bitbucket-cloud.example.com/pac/demo",
			want: "bitbucket-cloud",
		},
		{
			name: "forgejo",
			url:  "https://forgejo.example.com/pac/demo",
			want: "forgejo",
		},
		{
			name: "gitea",
			url:  "https://gitea.example.com/pac/demo",
			want: "gitea",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetProviderName(tt.url)
			assert.NilError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetProviderNameAsksForUnknownURL(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		answer string
		want   string
	}{
		{
			name:   "asks when provider cannot be detected",
			url:    "https://git.example.com/pac/demo",
			answer: "forgejo",
			want:   "forgejo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			as.StubOne(tt.answer)

			got, err := GetProviderName(tt.url)
			assert.NilError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
