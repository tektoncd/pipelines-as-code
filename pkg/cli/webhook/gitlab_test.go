package webhook

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	thelp "github.com/tektoncd/pipelines-as-code/pkg/provider/gitlab/test"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestAskGLWebhookConfig(t *testing.T) {
	//nolint
	io, _, _, _ := cli.IOTest()
	tests := []struct {
		name                string
		wantErrStr          string
		askStubs            func(*prompt.AskStubber)
		providerURL         string
		controllerURL       string
		repoURL             string
		personalaccesstoken string
	}{
		{
			name: "ask all details no defaults",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("https://gitlab.com/pac/test")
				as.StubOne("id")
				as.StubOne("https://test")
				as.StubOne("webhook-secret")
				as.StubOne("token")
				as.StubOne("https://gl.pac.test")
			},
			wantErrStr: "",
		},
		{
			name: "with defaults",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("id")
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			repoURL:       "https://gitlab.com/pac/demo",
			controllerURL: "https://test",
			providerURL:   "https://gl.pac.test",
			wantErrStr:    "",
		},
		{
			name: "with defaults and given personalaccesstoken",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("id")
				as.StubOne(true)
				as.StubOne("webhook-secret")
				as.StubOne("token")
			},
			repoURL:             "https://gitlab.com/pac/demo",
			controllerURL:       "https://test",
			providerURL:         "https://gl.pac.test",
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
			gl := gitLabConfig{IOStream: io}
			err := gl.askGLWebhookConfig(tt.repoURL, tt.controllerURL, tt.providerURL, tt.personalaccesstoken)
			if tt.wantErrStr != "" {
				assert.Equal(t, err.Error(), tt.wantErrStr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestGLCreate(t *testing.T) {
	_, _ = rtesting.SetupFakeContext(t)
	fakeclient, mux, teardown := thelp.Setup(t)
	defer teardown()
	//nolint
	io, _, _, _ := cli.IOTest()

	// webhook created
	mux.HandleFunc("/projects/11/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"status": "ok"}`)
	})

	// webhook failed
	mux.HandleFunc("/projects/13/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"status": "forbidden"}`)
	})

	// webhook response is successful HTTP but not the created status the CLI expects
	mux.HandleFunc("/projects/14/hooks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status": "ok"}`)
	})

	tests := []struct {
		name      string
		projectID string
		wantErr   bool
	}{
		{
			name:      "webhook created",
			projectID: "11",
			wantErr:   false,
		},
		{
			name:      "webhook failed",
			projectID: "13",
			wantErr:   true,
		},
		{
			name:      "webhook returned non created status",
			projectID: "14",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gl := gitLabConfig{
				IOStream:  io,
				Client:    fakeclient,
				projectID: tt.projectID,
			}
			err := gl.create()
			if !tt.wantErr {
				assert.NilError(t, err)
			} else {
				assert.Assert(t, err != nil)
			}
		})
	}
}

func TestGLNewClient(t *testing.T) {
	tests := []struct {
		name           string
		apiURL         string
		wantErrContain string
	}{
		{
			name: "default gitlab client",
		},
		{
			name:   "custom api url",
			apiURL: "https://gitlab.example.com/api/v4",
		},
		{
			name:           "invalid api url",
			apiURL:         "://bad-url",
			wantErrContain: "missing protocol scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gl := gitLabConfig{
				personalAccessToken: "token",
				APIURL:              tt.apiURL,
			}
			client, err := gl.newClient()
			if tt.wantErrContain != "" {
				assert.ErrorContains(t, err, tt.wantErrContain)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, client != nil)
		})
	}
}
