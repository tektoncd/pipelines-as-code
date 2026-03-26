package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestHandleEvent(t *testing.T) {
	t.Parallel()
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		ConfigMap: []*corev1.ConfigMap{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      info.DefaultPipelinesAscodeConfigmapName,
					Namespace: "default",
				},
				Data: map[string]string{},
			},
		},
	})
	logger, logCatcher := logger.GetLogger()

	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, "default")

	emptys := &unstructured.Unstructured{}
	emptys.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "not",
			"namespace": "console",
		},
	})
	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), emptys)
	repositories := []*v1alpha1.Repository{
		testnewrepo.NewRepo(
			testnewrepo.RepoTestcreationOpts{
				Name:             "pipelines-as-code",
				URL:              "https://nowhere.com",
				InstallNamespace: "pipelines-as-code",
			},
		),
	}

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "namespace",
				},
			},
		},
		Repositories: repositories,
		PipelineRuns: []*pipelinev1.PipelineRun{
			tektontest.MakePRStatus("namespace", "force-me", []pipelinev1.ChildStatusReference{
				tektontest.MakeChildStatusReference("first"),
				tektontest.MakeChildStatusReference("last"),
				tektontest.MakeChildStatusReference("middle"),
			}, nil),
		},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	l := listener{
		run: &params.Run{
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Log:            logger,
				Kube:           cs.Kube,
				Dynamic:        dynClient,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{
						AutoConfigureNewGitHubRepo: false,
					},
				},
				Controller: &info.ControllerInfo{
					Configmap:        info.DefaultPipelinesAscodeConfigmapName,
					Secret:           info.DefaultPipelinesAscodeSecretName,
					GlobalRepository: info.DefaultGlobalRepoName,
				},
				Kube: &info.KubeOpts{
					// TODO: we should use a global for that
					Namespace: "pipelines-as-code",
				},
			},
		},
		logger: logger,
	}
	l.run.Clients.InitClients()
	l.run.Info.InitInfo()

	// valid push event
	testEvent := github.PushEvent{Pusher: &github.CommitAuthor{Name: github.Ptr("user")}}
	event, err := json.Marshal(testEvent)
	assert.NilError(t, err)

	// invalid push event which will be skipped
	skippedEvent, err := json.Marshal(github.PushEvent{})
	assert.NilError(t, err)

	tests := []struct {
		name           string
		event          []byte
		eventType      string
		requestType    string
		statusCode     int
		wantLogSnippet string
	}{
		{
			name:        "get http call",
			requestType: "GET",
			event:       []byte("event"),
			statusCode:  200,
		},
		{
			name:        "invalid json body",
			requestType: "POST",
			event:       []byte("some random string for invalid json body"),
			statusCode:  400,
		},
		{
			name:        "invalid json body only when payload has been set",
			requestType: "POST",
			event:       []byte(""),
			statusCode:  200,
		},
		{
			name:        "valid event",
			requestType: "POST",
			eventType:   "push",
			event:       event,
			statusCode:  202,
		},
		{
			name:           "detected global repository",
			requestType:    "POST",
			eventType:      "push",
			event:          event,
			statusCode:     202,
			wantLogSnippet: "detected global repository settings named pipelines-as-code in namespace pipelines-as-code",
		},
		{
			name:        "skip event",
			requestType: "POST",
			eventType:   "push",
			event:       skippedEvent,
			statusCode:  200,
		},
		{
			name:        "git provider not detected",
			requestType: "POST",
			eventType:   "",
			event:       event,
			statusCode:  200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tn := tt
			t.Parallel()

			ts := httptest.NewServer(l.handleEvent(ctx))
			defer ts.Close()

			req, err := http.NewRequestWithContext(context.Background(), tn.requestType, ts.URL, bytes.NewReader(tn.event))
			if err != nil {
				t.Fatalf("error creating request: %s", err)
			}
			req.Header.Set("X-Github-Event", tn.eventType)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("error sending request: %s", err)
			}
			defer resp.Body.Close()

			if tn.wantLogSnippet != "" {
				assert.Assert(t, logCatcher.FilterMessageSnippet(tn.wantLogSnippet).Len() > 0, logCatcher.All())
			}
			if resp.StatusCode != tn.statusCode {
				t.Fatalf("expected status code : %v but got %v ", tn.statusCode, resp.StatusCode)
			}
		})
	}
}

func TestWhichProvider(t *testing.T) {
	logger, _ := logger.GetLogger()
	l := listener{
		logger: logger,
	}
	tests := []struct {
		name          string
		event         any
		header        http.Header
		wantErrString string
	}{
		{
			name: "github event",
			header: map[string][]string{
				"X-Github-Event":    {"push"},
				"X-GitHub-Delivery": {"abcd"},
			},
			event: github.PushEvent{
				Pusher: &github.CommitAuthor{Name: github.Ptr("user")},
			},
		},
		{
			name: "some random event",
			header: map[string][]string{
				"foo": {"bar"},
			},
			event:         map[string]string{"foo": "bar"},
			wantErrString: "no supported Git provider has been detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jeez, err := json.Marshal(tt.event)
			if err != nil {
				assert.NilError(t, err)
			}
			req := &http.Request{
				Header: tt.header,
			}

			_, _, err = l.detectProvider(req, string(jeez))
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)
		})
	}
}
