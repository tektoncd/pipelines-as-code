package bootstrap

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestGenerateManifest(t *testing.T) {
	type args struct {
		opts *bootstrapOpts
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "test generate manifest",
			args: args{
				opts: &bootstrapOpts{
					GithubApplicationName: "test",
					GithubApplicationURL:  "http://localhost:8080",
					RouteName:             "http://test",
					webserverPort:         8080,
				},
			},
			want: `{"name":"test","url":"http://localhost:8080"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generateManifest(tt.args.opts)
			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(string(got), tt.want))
		})
	}
}

func TestGetGHClient(t *testing.T) {
	tests := []struct {
		name    string
		URL     string
		want    string
		wantErr bool
	}{
		{
			name: "test get github client",
			URL:  defaultPublicGithub,
			want: "https://api.github.com/",
		},
		{
			name: "test get github client",
			URL:  "http://localhost:8080",
			want: "http://localhost:8080/api/v3/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &bootstrapOpts{
				GithubAPIURL: tt.URL,
			}
			got, err := getGHClient(opts)
			assert.NilError(t, err)
			assert.Equal(t, got.BaseURL(), tt.want)
		})
	}
}
