package webhook

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	ghtesthelper "github.com/tektoncd/pipelines-as-code/pkg/test/github"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestAskGHWebhookConfig(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	tests := []struct {
		name                string
		wantErrStr          string
		askStubs            func(*prompt.AskStubber)
		repoURL             string
		controllerURL       string
		personalaccesstoken string
	}{
		{
			name: "invalid repo format",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("invalid-repo")
			},
			wantErrStr: "invalid repo url at least a organization/project and a repo needs to be specified: invalid-repo",
		},
		{
			name: "ask all details no defaults",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("https://github.com/pac/test")
				as.StubOne("https://controller.url")
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			wantErrStr: "",
		},
		{
			name: "with defaults",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			repoURL:       "https://github.com/pac/demo",
			controllerURL: "https://test",
			wantErrStr:    "",
		},
		{
			name: "with defaults and a slash",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			repoURL:       "https://github.com/pac/demo/",
			controllerURL: "https://test",
			wantErrStr:    "",
		},
		{
			name: "with personalaccesstoken",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			repoURL:             "https://github.com/pac/demo/",
			controllerURL:       "https://test",
			personalaccesstoken: "Yzg5NzhlYmNkNTQwNzYzN2E2ZGExYzhkMTc4NjU0MjY3ZmQ2NmMeZg==",
			wantErrStr:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			gh := gitHubConfig{IOStream: io}
			err := gh.askGHWebhookConfig(tt.repoURL, tt.controllerURL, "", tt.personalaccesstoken)
			if tt.wantErrStr != "" {
				assert.Equal(t, err.Error(), tt.wantErrStr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestCreate(t *testing.T) {
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()
	//nolint
	io, _, _, _ := cli.IOTest()

	// webhook created for repo pac/valid
	mux.HandleFunc("/repos/pac/valid/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	// webhook failed for repo pac/invalid
	mux.HandleFunc("/repos/pac/invalid/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"status": "forbidden"}`)
	})

	// webhook response is successful HTTP but not the created status the CLI expects
	mux.HandleFunc("/repos/pac/notcreated/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status": "ok"}`)
	})

	tests := []struct {
		name      string
		wantErr   bool
		repoName  string
		repoOwner string
	}{
		{
			name:      "webhook created",
			repoOwner: "pac",
			repoName:  "valid",
		},
		{
			name:      "webhook failed",
			repoOwner: "pac",
			repoName:  "invalid",
			wantErr:   true,
		},
		{
			name:      "webhook returned non created status",
			repoOwner: "pac",
			repoName:  "notcreated",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			gh := gitHubConfig{
				IOStream:  io,
				Client:    fakeclient,
				repoOwner: tt.repoOwner,
				repoName:  tt.repoName,
			}
			err := gh.create(ctx)
			if !tt.wantErr {
				assert.NilError(t, err)
			} else {
				assert.Assert(t, err != nil)
			}
		})
	}
}

func TestNewGHClientByToken(t *testing.T) {
	tests := []struct {
		name           string
		apiURL         string
		wantBaseURL    string
		wantUploadURL  string
		wantErrContain string
	}{
		{
			name:          "default api url when empty",
			wantBaseURL:   "https://api.github.com/",
			wantUploadURL: "https://uploads.github.com/",
		},
		{
			name:          "default api url when public api configured",
			apiURL:        keys.PublicGithubAPIURL,
			wantBaseURL:   "https://api.github.com/",
			wantUploadURL: "https://uploads.github.com/",
		},
		{
			name:          "enterprise api url",
			apiURL:        "https://ghe.example.com/api/v3/",
			wantBaseURL:   "https://ghe.example.com/api/v3/",
			wantUploadURL: "https://ghe.example.com/api/v3/api/uploads/",
		},
		{
			name:           "invalid enterprise api url",
			apiURL:         "://bad-url",
			wantErrContain: "missing protocol scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := gitHubConfig{
				personalAccessToken: "token",
				APIURL:              tt.apiURL,
			}

			client, err := gh.newGHClientByToken(context.Background())
			if tt.wantErrContain != "" {
				assert.ErrorContains(t, err, tt.wantErrContain)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantBaseURL, client.BaseURL())
			assert.Equal(t, tt.wantUploadURL, client.UploadURL())
		})
	}
}
