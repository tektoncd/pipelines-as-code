package webhook

import (
	"testing"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetProviderName(tt.url)
			assert.NilError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
