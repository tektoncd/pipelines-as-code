package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	giteatest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"gotest.tools/v3/assert"
)

func TestAskForgejoWebhookConfig(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	tests := []struct {
		name                string
		wantErrStr          string
		askStubs            func(*prompt.AskStubber)
		repoURL             string
		controllerURL       string
		providerURL         string
		personalaccesstoken string
		wantRepoName        string
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
				as.StubOne("https://forgejo.example.com/pac/test")
				as.StubOne("https://controller.url")
				as.StubOne("webhook-secret")
				as.StubOne("token")
				as.StubOne("https://forgejo.example.com")
			},
		},
		{
			name: "with defaults",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
				as.StubOne("https://forgejo.example.com")
			},
			repoURL:       "https://forgejo.example.com/pac/demo",
			controllerURL: "https://test",
		},
		{
			name: "with personalaccesstoken",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("https://forgejo.example.com")
			},
			repoURL:             "https://forgejo.example.com/pac/demo",
			controllerURL:       "https://test",
			personalaccesstoken: "Yzg5NzhlYmNkNTQwNzYzN2E2ZGExYzhkMTc4NjU0MjY3ZmQ2NmMeZg==",
		},
		{
			name: "with provider url",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			repoURL:       "https://git.example.com/pac/demo",
			controllerURL: "https://test",
			providerURL:   "https://git.example.com",
		},
		{
			name: "with git suffix",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
				as.StubOne("https://forgejo.example.com")
			},
			repoURL:       "https://forgejo.example.com/pac/demo.git",
			controllerURL: "https://test",
			wantRepoName:  "demo",
		},
		{
			name: "with trailing slash",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
				as.StubOne("https://forgejo.example.com")
			},
			repoURL:       "https://forgejo.example.com/pac/demo/",
			controllerURL: "https://test",
			wantRepoName:  "demo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			fg := forgejoConfig{IOStream: io}
			err := fg.askForgejoWebhookConfig(tt.repoURL, tt.controllerURL, tt.providerURL, tt.personalaccesstoken)
			if tt.wantErrStr != "" {
				assert.Equal(t, err.Error(), tt.wantErrStr)
				return
			}
			assert.NilError(t, err)
			if tt.wantRepoName != "" {
				assert.Equal(t, tt.wantRepoName, fg.repoName)
			}
		})
	}
}

func TestForgejoCreate(t *testing.T) {
	fgClient, mux, tearDown := giteatest.Setup(t)
	defer tearDown()
	//nolint
	io, _, _, _ := cli.IOTest()

	mux.HandleFunc("/repos/pac/valid/hooks", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, r.Method, http.MethodPost)
		var hook forgejo.CreateHookOption
		assert.NilError(t, json.NewDecoder(r.Body).Decode(&hook))
		assert.Equal(t, forgejo.HookTypeForgejo, hook.Type)
		assert.Equal(t, "https://controller.url", hook.Config["url"])
		assert.Equal(t, "json", hook.Config["content_type"])
		assert.Equal(t, "webhook-secret", hook.Config["secret"])
		assert.DeepEqual(t, []string{
			"push",
			"pull_request",
			"pull_request_sync",
			"pull_request_label",
			"issue_comment",
		}, hook.Events)
		assert.Assert(t, hook.Active)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	mux.HandleFunc("/repos/pac/invalid/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "forbidden"}`))
	})

	tests := []struct {
		name     string
		repoName string
		wantErr  bool
	}{
		{
			name:     "webhook created",
			repoName: "valid",
		},
		{
			name:     "webhook failed",
			repoName: "invalid",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fg := forgejoConfig{
				IOStream:      io,
				Client:        fgClient,
				repoOwner:     "pac",
				repoName:      tt.repoName,
				controllerURL: "https://controller.url",
				webhookSecret: "webhook-secret",
			}
			err := fg.create()
			if !tt.wantErr {
				assert.NilError(t, err)
			} else {
				assert.Assert(t, err != nil)
			}
		})
	}
}

func TestForgejoRunUsesPersonalAccessTokenForWebhookCreation(t *testing.T) {
	serverURL, mux, tearDown := setupForgejoServer()
	defer tearDown()

	//nolint
	io, _, _, _ := cli.IOTest()
	mux.HandleFunc("/repos/pac/valid/hooks", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "token runtime-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	as, teardown := prompt.InitAskStubber()
	defer teardown()
	as.StubOne(true)
	as.StubOne("webhook-secret")
	as.StubOne("runtime-token")

	fg := forgejoConfig{IOStream: io}
	res, err := fg.Run(context.Background(), &Options{
		RepositoryURL:       serverURL + "/pac/valid",
		ControllerURL:       "https://controller.url",
		ProviderAPIURL:      serverURL,
		PersonalAccessToken: "",
	})
	assert.NilError(t, err)
	assert.Equal(t, "runtime-token", res.PersonalAccessToken)
}

func setupForgejoServer() (string, *http.ServeMux, func()) {
	mux := http.NewServeMux()
	apiHandler := http.NewServeMux()
	apiHandler.Handle("/api/v1/", http.StripPrefix("/api/v1", mux))
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version": "1.17.0"}`))
	})
	server := httptest.NewServer(apiHandler)
	return server.URL, mux, server.Close
}
