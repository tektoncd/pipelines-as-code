package webhook

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	bbcloudtest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/test"
	"gotest.tools/v3/assert"
)

func TestAskBBWebhookConfig(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	tests := []struct {
		name                string
		wantErrStr          string
		askStubs            func(*prompt.AskStubber)
		repoURL             string
		controllerURL       string
		personalaccesstoken string
		wantAccountEmail    string
		wantAPIToken        string
		wantControllerURL   string
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
				as.StubOne("https://bitbucket.org/pac/test")
				as.StubOne("user@example.com")
				as.StubOne("token")
				as.StubOne("https://controller.url")
			},
			wantErrStr:        "",
			wantAccountEmail:  "user@example.com",
			wantAPIToken:      "token",
			wantControllerURL: "https://controller.url",
		},
		{
			name: "with defaults",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("user@example.com")
				as.StubOne("token")
				as.StubOne(true)
			},
			repoURL:           "https://bitbucket.org/pac/demo",
			controllerURL:     "https://test",
			wantErrStr:        "",
			wantAccountEmail:  "user@example.com",
			wantAPIToken:      "token",
			wantControllerURL: "https://test",
		},
		{
			name: "with personalaccesstoken",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("user@example.com")
				as.StubOne(true)
			},
			repoURL:             "https://bitbucket.org/pac/demo",
			controllerURL:       "https://test",
			personalaccesstoken: "Yzg5NzhlYmNkNTQwNzYzN2E2ZGExYzhkMTc4NjU0MjY3ZmQ2NmMeZg==",
			wantErrStr:          "",
			wantAccountEmail:    "user@example.com",
			wantAPIToken:        "Yzg5NzhlYmNkNTQwNzYzN2E2ZGExYzhkMTc4NjU0MjY3ZmQ2NmMeZg==",
			wantControllerURL:   "https://test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			bb := bitbucketCloudConfig{IOStream: io}
			err := bb.askBBWebhookConfig(tt.repoURL, tt.controllerURL, "", tt.personalaccesstoken)
			if tt.wantErrStr != "" {
				assert.Equal(t, err.Error(), tt.wantErrStr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantAccountEmail, bb.accountEmail)
			assert.Equal(t, tt.wantAPIToken, bb.apiToken)
			assert.Equal(t, tt.wantControllerURL, bb.controllerURL)
		})
	}
}

func TestBBRunReturnsAPITokenAndAccountEmail(t *testing.T) {
	bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
	defer tearDown()
	//nolint
	io, _, _, _ := cli.IOTest()

	mux.HandleFunc("/repositories/pac/repo/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"type": "ok"}`)
	})

	as, teardown := prompt.InitAskStubber()
	defer teardown()
	as.StubOne("user@example.com")
	as.StubOne(true)

	bb := bitbucketCloudConfig{IOStream: io, Client: bbclient}
	response, err := bb.Run(context.Background(), &Options{
		RepositoryURL:       "https://bitbucket.org/pac/repo",
		ControllerURL:       "https://bb.pac.test",
		PersonalAccessToken: "api-token",
	})
	assert.NilError(t, err)
	assert.Equal(t, "user@example.com", response.UserName)
	assert.Equal(t, "api-token", response.PersonalAccessToken)
	assert.Equal(t, "https://bb.pac.test", response.ControllerURL)
}

func TestBBCreate(t *testing.T) {
	bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
	defer tearDown()
	//nolint
	io, _, _, _ := cli.IOTest()

	// webhook created for repo pac/repo
	mux.HandleFunc("/repositories/pac/repo/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"type": "ok"}`)
	})

	// webhook failed for repo pac/invalid
	mux.HandleFunc("/repositories/pac/invalid/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"type": "error"}`)
	})

	tests := []struct {
		name      string
		wantErr   bool
		repoName  string
		repoOwner string
		apiURL    string
	}{
		{
			name:      "webhook created",
			repoOwner: "pac",
			repoName:  "repo",
		},
		{
			name:      "webhook failed",
			repoOwner: "pac",
			repoName:  "invalid",
			wantErr:   true,
			apiURL:    "https://api.bitbucket.org/2.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bb := bitbucketCloudConfig{
				IOStream:      io,
				Client:        bbclient,
				repoOwner:     tt.repoOwner,
				repoName:      tt.repoName,
				APIURL:        tt.apiURL,
				controllerURL: "https://bb.pac.test",
			}
			err := bb.create()
			if !tt.wantErr {
				assert.NilError(t, err)
			}
		})
	}
}

func TestBBCreateUsesAccountEmailForAuthentication(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	mux := http.NewServeMux()
	apiHandler := http.NewServeMux()
	apiHandler.Handle("/2.0/", http.StripPrefix("/2.0", mux))
	server := httptest.NewServer(apiHandler)
	defer server.Close()

	mux.HandleFunc("/repositories/pac/repo/hooks", func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		assert.Assert(t, ok)
		assert.Equal(t, "user@example.com", username)
		assert.Equal(t, "api-token", password)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"type": "ok"}`)
	})

	bb := bitbucketCloudConfig{
		IOStream:      io,
		repoOwner:     "pac",
		repoName:      "repo",
		APIURL:        server.URL + "/2.0",
		controllerURL: "https://bb.pac.test",
		accountEmail:  "user@example.com",
		apiToken:      "api-token",
	}
	err := bb.create()
	assert.NilError(t, err)
}

func TestBBCreateRequiresAPIToken(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	bb := bitbucketCloudConfig{
		IOStream:     io,
		accountEmail: "user@example.com",
	}
	err := bb.create()
	assert.ErrorContains(t, err, "bitbucket cloud API token is required")
}

func TestBBCreateRequiresAccountEmail(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	bb := bitbucketCloudConfig{
		IOStream: io,
		apiToken: "api-token",
	}
	err := bb.create()
	assert.ErrorContains(t, err, "bitbucket cloud Atlassian account email is required")
}
