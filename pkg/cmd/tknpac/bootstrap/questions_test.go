package bootstrap

import (
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	"gotest.tools/v3/assert"
)

func TestAskQuestions(t *testing.T) {
	origAsk := prompt.SurveyAsk
	origAskOne := prompt.SurveyAskOne
	defer func() {
		prompt.SurveyAsk = origAsk
		prompt.SurveyAskOne = origAskOne
	}()

	tests := []struct {
		name          string
		opts          *bootstrapOpts
		stubAsk       func(qs []*survey.Question, response any) error
		stubAskOne    func(p survey.Prompt, response any) error
		wantErr       bool
		wantAPIURL    string
		wantAppURL    string
		wantRouteName string
	}{
		{
			name: "public github, route already set",
			opts: &bootstrapOpts{
				GithubApplicationName: "myapp",
				RouteName:             "https://route.example.com",
			},
			stubAsk: func(_ []*survey.Question, _ any) error {
				return nil
			},
			wantAPIURL:    "https://github.com",
			wantAppURL:    "https://route.example.com",
			wantRouteName: "https://route.example.com",
		},
		{
			name: "route needs to be asked",
			opts: &bootstrapOpts{
				GithubApplicationName: "myapp",
			},
			stubAsk: func(_ []*survey.Question, _ any) error {
				return nil
			},
			stubAskOne: func(_ survey.Prompt, response any) error {
				if s, ok := response.(*string); ok {
					*s = "myroute.example.com"
				}
				return nil
			},
			wantAPIURL:    "https://github.com",
			wantAppURL:    "myroute.example.com",
			wantRouteName: "myroute.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _ := newIOStream()
			tt.opts.ioStreams = io

			prompt.SurveyAsk = func(qs []*survey.Question, response any, _ ...survey.AskOpt) error {
				return tt.stubAsk(qs, response)
			}
			prompt.SurveyAskOne = func(p survey.Prompt, response any, _ ...survey.AskOpt) error {
				if tt.stubAskOne != nil {
					return tt.stubAskOne(p, response)
				}
				return nil
			}

			err := askQuestions(tt.opts)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.opts.GithubAPIURL, tt.wantAPIURL)
			assert.Equal(t, tt.opts.GithubApplicationURL, tt.wantAppURL)
			assert.Equal(t, tt.opts.RouteName, tt.wantRouteName)
		})
	}
}
