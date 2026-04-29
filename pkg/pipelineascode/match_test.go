package pipelineascode

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v84/github"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestPacRunCheckNeedUpdate(t *testing.T) {
	tests := []struct {
		name                 string
		tmpl                 string
		upgradeMessageSubstr string
		needupdate           bool
	}{
		{
			name:       "no need",
			tmpl:       ` secretName: "foo-bar-foo"`,
			needupdate: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPacs(nil, nil, &params.Run{Clients: clients.Clients{}}, &info.PacOpts{}, nil, nil, nil)
			got, needupdate := p.checkNeedUpdate(tt.tmpl)
			if tt.upgradeMessageSubstr != "" {
				assert.Assert(t, strings.Contains(got, tt.upgradeMessageSubstr))
			}
			assert.Assert(t, needupdate == tt.needupdate)
		})
	}
}

func TestChangePipelineRun(t *testing.T) {
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testrepo",
			Namespace: "test",
		},
	}

	prs := []*tektonv1.PipelineRun{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "{{git_auth_secret}}",
				Namespace: "{{ repo_name }}",
			},
		},
	}

	jsonErrorPRs := []*tektonv1.PipelineRun{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "json-error-pr",
				Namespace: "test",
			},
			Spec: tektonv1.PipelineRunSpec{
				Params: []tektonv1.Param{
					{
						Name: "my-param",
						Value: tektonv1.ParamValue{
							// We are intentionally leaving this empty:
							// Type: tektonv1.ParamTypeString,
							StringVal: "some-value",
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name          string
		prs           []*tektonv1.PipelineRun
		expectedError string
	}{
		{
			name: "test with params",
			prs:  prs,
		},
		{
			name:          "test with json error",
			prs:           jsonErrorPRs,
			expectedError: "failed to marshal PipelineRun json-error-pr: json: error calling MarshalJSON for type v1.ParamValue: impossible ParamValues.Type: \"\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := info.NewEvent()
			event.Repository = "testrepo"
			p := &PacRun{event: event}
			ctx, _ := rtesting.SetupFakeContext(t)
			err := p.changePipelineRun(ctx, repo, tt.prs)
			if tt.expectedError != "" {
				assert.Error(t, err, tt.expectedError)
				return
			}
			assert.Assert(t, strings.HasPrefix(tt.prs[0].GetName(), "pac-gitauth"), tt.prs[0].GetName(), "has no pac-gitauth prefix")
			assert.Assert(t, tt.prs[0].GetAnnotations()[apipac.GitAuthSecret] != "")
			assert.Assert(t, tt.prs[0].GetNamespace() == "testrepo", "namespace should be testrepo: %v", tt.prs[0].GetNamespace())
		})
	}
}

func TestFilterRunningPipelineRunOnTargetTest(t *testing.T) {
	testPipeline := "test"
	prs := []*tektonv1.PipelineRun{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pipelinerun-" + testPipeline,
				Annotations: map[string]string{
					apipac.OriginalPRName: testPipeline,
				},
			},
		},
	}
	ret := filterRunningPipelineRunOnTargetTest("", prs)
	assert.Assert(t, ret == nil)
	ret = filterRunningPipelineRunOnTargetTest(testPipeline, prs)
	assert.Equal(t, prs[0].GetName(), ret.GetName())
	prs = []*tektonv1.PipelineRun{}
	ret = filterRunningPipelineRunOnTargetTest(testPipeline, prs)
	assert.Assert(t, ret == nil)
}

func TestGetPipelineRunsFromRepoExplicitTestUsesTargetNamespaceRepo(t *testing.T) {
	observerCore, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observerCore).Sugar()

	tests := []struct {
		name                string
		targetNamespaceLine string
		wantRepositoryNS    string
		wantRepositoryName  string
		wantNoMatch         bool
	}{
		{
			name:                "uses target namespace repo from annotation",
			targetNamespaceLine: "    pipelinesascode.tekton.dev/target-namespace: \"bar\"",
			wantRepositoryNS:    "bar",
			wantRepositoryName:  "bar",
		},
		{
			name:                "falls back to matched repo when annotation is absent",
			targetNamespaceLine: "",
			wantRepositoryNS:    "foo",
			wantRepositoryName:  "foo",
		},
		{
			name:                "skips pipelinerun when target namespace repo not found",
			targetNamespaceLine: "    pipelinesascode.tekton.dev/target-namespace: \"nonexistent\"",
			wantNoMatch:         true,
		},
	}

	template := `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: pr-gitops-comment
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
%s
spec:
  pipelineSpec:
    tasks:
    - name: task
      taskSpec:
        steps:
        - name: task
          image: quay.io/prometheus/busybox
          script: |
            echo "HELLOMOTO"
            exit 0
`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			repositories := []*v1alpha1.Repository{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "foo",
					},
					Spec: v1alpha1.RepositorySpec{
						URL: "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bar",
						Namespace: "bar",
					},
					Spec: v1alpha1.RepositorySpec{
						URL: "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
					},
				},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Repositories: repositories,
			})

			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
					Tekton:         stdata.Pipeline,
					Log:            logger,
				},
				Info: info.Info{},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			event := &info.Event{
				URL:           "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
				Organization:  "pipelines-as-code",
				Repository:    "e2e",
				Sender:        "foo",
				SHA:           "abc123",
				HeadBranch:    "main",
				BaseBranch:    "main",
				EventType:     opscomments.TestSingleCommentEventType.String(),
				TriggerTarget: triggertype.PullRequest,
				State: info.State{
					TargetTestPipelineRun: "pr-gitops-comment",
				},
			}

			p := NewPacs(
				event,
				&testprovider.TestProviderImp{TektonDirTemplate: fmt.Sprintf(template, tt.targetNamespaceLine)},
				cs,
				&info.PacOpts{},
				nil,
				logger,
				nil,
			)

			rawTemplates := fmt.Sprintf(template, tt.targetNamespaceLine)
			matchedPRs, err := p.getPipelineRunsFromRepo(ctx, repositories[0], rawTemplates)
			assert.NilError(t, err)
			if tt.wantNoMatch {
				assert.Equal(t, len(matchedPRs), 0)
				return
			}
			assert.Equal(t, len(matchedPRs), 1)
			assert.Assert(t, matchedPRs[0].Repo != nil)
			assert.Equal(t, matchedPRs[0].Repo.GetNamespace(), tt.wantRepositoryNS)
			assert.Equal(t, matchedPRs[0].Repo.GetName(), tt.wantRepositoryName)
		})
	}
}

func TestGetPipelineRunsFromRepo(t *testing.T) {
	pullRequestEvent := &info.Event{
		SHA:           "principale",
		Organization:  "organizationes",
		Repository:    "lagaffe",
		URL:           "https://service/documentation",
		HeadBranch:    "main",
		BaseBranch:    "main",
		Sender:        "fantasio",
		EventType:     "pull_request",
		TriggerTarget: "pull_request",
	}
	okToTestEvent := &info.Event{
		SHA:           "principale",
		Organization:  "organizationes",
		Repository:    "lagaffe",
		URL:           "https://service/documentation",
		HeadBranch:    "main",
		BaseBranch:    "main",
		Sender:        "fantasio",
		EventType:     "ok-to-test-comment",
		TriggerTarget: "pull_request",
	}
	retestAllEvent := &info.Event{
		SHA:               "principale",
		Organization:      "organizationes",
		Repository:        "lagaffe",
		URL:               "https://service/documentation",
		HeadBranch:        "main",
		BaseBranch:        "main",
		Sender:            "fantasio",
		EventType:         opscomments.RetestAllCommentEventType.String(),
		TriggerTarget:     "pull_request",
		PullRequestNumber: 10,
	}
	testExplicitNoMatchPREvent := &info.Event{
		SHA:           "principale",
		Organization:  "organizationes",
		Repository:    "lagaffe",
		URL:           "https://service/documentation",
		HeadBranch:    "main",
		BaseBranch:    "main",
		Sender:        "fantasio",
		TriggerTarget: "pull_request",
		State: info.State{
			TargetTestPipelineRun: "no-match",
		},
	}

	noOpsCommentEvent := &info.Event{
		SHA:           "principale",
		Organization:  "organizationes",
		Repository:    "lagaffe",
		URL:           "https://service/documentation",
		HeadBranch:    "main",
		BaseBranch:    "main",
		Sender:        "fantasio",
		EventType:     opscomments.NoOpsCommentEventType.String(),
		TriggerTarget: "pull_request",
	}

	tests := []struct {
		name                  string
		repositories          *v1alpha1.Repository
		tektondir             string
		expectedNumberOfPruns int
		event                 *info.Event
		logSnippet            string
		wantErr               bool
		seedData              *testclient.Data
	}{
		{
			name: "more than one pipelinerun in .tekton dir",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir:             "testdata/pull_request_multiplepipelineruns",
			expectedNumberOfPruns: 2,
			event:                 pullRequestEvent,
		},
		{
			name: "single pipelinerun in .tekton dir",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir:             "testdata/pull_request",
			expectedNumberOfPruns: 1,
			event:                 pullRequestEvent,
		},
		{
			name: "invalid tekton pipelineruns in directory",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir:  "testdata/invalid_tekton_yaml",
			event:      pullRequestEvent,
			logSnippet: `json: cannot unmarshal object into Go struct field PipelineSpec.spec.pipelineSpec.tasks of type []v1beta1.PipelineTask`,
		},
		{
			name: "no-match pipelineruns in .tekton dir, only matched should be returned",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			// we have 3 PR in there 2 that has a match on pull request and 1 that is a no-matching
			// matching those two that is matching here
			tektondir:             "testdata/no-match",
			expectedNumberOfPruns: 2,
			event:                 pullRequestEvent,
		},
		{
			name: "no-match pipelineruns in .tekton dir, only match the no-match",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			// we have 3 PR in there 2 that has a match on pull request and 1 that is a no-matching
			// matching that only one here
			tektondir:             "testdata/no-match",
			expectedNumberOfPruns: 1,
			event:                 testExplicitNoMatchPREvent,
		},
		{
			name: "no-match pipelineruns in .tekton dir, on ok-to-test command for an external user",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir:             "testdata/push_branch",
			expectedNumberOfPruns: 0,
			event:                 okToTestEvent,
		},
		{
			name: "same name pipelineruns error on regular event",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir: "testdata/same_name_pipelineruns",
			event:     pullRequestEvent,
			wantErr:   true,
		},
		{
			name: "same name pipelineruns skipped on no-ops comment event",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir:             "testdata/same_name_pipelineruns",
			expectedNumberOfPruns: 0,
			event:                 noOpsCommentEvent,
			logSnippet:            "skipping MetadataResolve error for no-ops comment event",
		},
		{
			name: "retest when all pipelines already succeeded returns no runs and posts comment",
			repositories: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "test",
				},
				Spec: v1alpha1.RepositorySpec{},
			},
			tektondir:             "testdata/pull_request",
			expectedNumberOfPruns: 0,
			event:                 retestAllEvent,
			logSnippet:            "All PipelineRuns for this commit have already succeeded",
			seedData: &testclient.Data{
				Repositories: []*v1alpha1.Repository{{
					ObjectMeta: metav1.ObjectMeta{Name: "testrepo", Namespace: "test"},
					Spec:       v1alpha1.RepositorySpec{},
				}},
				PipelineRuns: []*tektonv1.PipelineRun{{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull_request-xyz",
						Namespace: "test",
						Labels: map[string]string{
							apipac.SHA:            "principale",
							apipac.OriginalPRName: "pull_request",
						},
						Annotations: map[string]string{
							apipac.OriginalPRName: "pull_request",
						},
					},
					Status: tektonv1.PipelineRunStatus{
						Status: knativeduckv1.Status{
							Conditions: knativeduckv1.Conditions{{
								Type:   apis.ConditionSucceeded,
								Status: corev1.ConditionTrue,
							}},
						},
					},
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observerCore, logCatcher := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observerCore).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			// For retest-when-all-succeeded case, CreateComment is called; register handler so it succeeds.
			if tt.name == "retest when all pipelines already succeeded returns no runs and posts comment" {
				mux.HandleFunc("/repos/organizationes/lagaffe/issues/10/comments", func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						w.WriteHeader(http.StatusCreated)
					}
				})
			}
			if tt.tektondir != "" {
				ghtesthelper.SetupGitTree(t, mux, tt.tektondir, tt.event, false)
			}

			seedData := testclient.Data{}
			if tt.seedData != nil {
				seedData = *tt.seedData
			}
			stdata, _ := testclient.SeedTestData(t, ctx, seedData)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Log:            logger,
					Kube:           stdata.Kube,
					Tekton:         stdata.Pipeline,
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			k8int := &kitesthelper.KinterfaceTest{
				ConsoleURL: "https://console.url",
			}
			vcx := &ghprovider.Provider{
				Token:  github.Ptr("None"),
				Logger: logger,
			}
			vcx.SetGithubClient(fakeclient)
			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					ApplicationName:    "Pipelines as Code CI",
					SecretAutoCreation: true,
					RemoteTasks:        true,
				},
			}
			vcx.SetPacInfo(pacInfo)
			p := NewPacs(tt.event, vcx, cs, pacInfo, k8int, logger, nil)
			p.eventEmitter = events.NewEventEmitter(stdata.Kube, logger)
			rawTemplates, err := vcx.GetTektonDir(ctx, tt.event, ".tekton", "source")
			assert.NilError(t, err)
			matchedPRs, err := p.getPipelineRunsFromRepo(ctx, tt.repositories, rawTemplates)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected an error but got nil")
				return
			}
			assert.NilError(t, err)
			matchedPRNames := make([]string, len(matchedPRs))
			for i := range matchedPRs {
				matchedPRNames[i] = matchedPRs[i].PipelineRun.GetGenerateName()
			}
			if tt.logSnippet != "" {
				assert.Assert(t, logCatcher.FilterMessageSnippet(tt.logSnippet).Len() > 0, logCatcher.All())
			}
			assert.Equal(t, len(matchedPRNames), tt.expectedNumberOfPruns)
		})
	}
}

func TestVerifyRepoAndUser(t *testing.T) {
	observerCore, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observerCore).Sugar()

	payload := []byte(`{"key": "value"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(payload)
	sha256secret := hex.EncodeToString(mac.Sum(nil))

	header := make(http.Header)
	header.Set(github.SHA256SignatureHeader, fmt.Sprintf("sha256=%s", sha256secret))

	request := &info.Request{
		Header:  header,
		Payload: payload,
	}

	tests := []struct {
		name          string
		runevent      info.Event
		repositories  []*v1alpha1.Repository
		webhookSecret string
		wantRepoNil   bool
		wantErr       bool
		wantErrMsg    string
	}{
		{
			name: "no repository match",
			runevent: info.Event{
				Organization:  "owner",
				Repository:    "repo",
				URL:           "https://example.com/owner/repo",
				SHA:           "123abc",
				EventType:     triggertype.PullRequest.String(),
				TriggerTarget: triggertype.PullRequest,
			},
			wantRepoNil: true,
			wantErr:     false,
		},
		{
			name: "missing git_provider section",
			runevent: info.Event{
				Organization:  "owner",
				Repository:    "repo",
				URL:           "https://example.com/owner/repo",
				SHA:           "123abc",
				EventType:     triggertype.PullRequest.String(),
				TriggerTarget: triggertype.PullRequest,
			},
			repositories: []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{URL: "https://example.com/owner/repo"},
			}},
			wantRepoNil: false,
			wantErr:     true,
			wantErrMsg:  "cannot get secret from repository: failed to find git_provider details in repository spec: ns/repo",
		},
		{
			name: "webhook validation failure",
			runevent: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://example.com/owner/repo",
				SHA:            "123abc",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Request:        &info.Request{Header: http.Header{}},
			},
			repositories: []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{URL: "https://example.com/owner/repo"},
			}},
			wantRepoNil: false,
			wantErr:     true,
			wantErrMsg:  "could not validate payload, check your webhook secret?: no signature has been detected, for security reason we are not allowing webhooks that has no secret",
		},
		{
			name: "webhook secret is not set",
			runevent: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://example.com/owner/repo",
				SHA:            "123abc",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Request:        request,
			},
			repositories: []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{URL: "https://example.com/owner/repo"},
			}},
			wantRepoNil: false,
			wantErr:     true,
			wantErrMsg:  "could not validate payload, check your webhook secret?: no webhook secret has been set, in repository CR or secret",
		},
		{
			name: "permission denied push comment",
			runevent: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://example.com/owner/repo",
				SHA:            "123abc",
				EventType:      opscomments.TestAllCommentEventType.String(),
				TriggerTarget:  triggertype.Push,
				Sender:         "intruder",
				InstallationID: 1,
				Request:        request,
			},
			repositories: []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{URL: "https://example.com/owner/repo"},
			}},
			webhookSecret: "secret",
			wantRepoNil:   true,
			wantErr:       true,
			wantErrMsg:    "failed to run create status, user is not allowed to run the CI",
		},
		{
			name: "permission denied pull_request comment pending approval",
			runevent: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://example.com/owner/repo",
				SHA:            "123abc",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				Sender:         "outsider",
				InstallationID: 1,
				Request:        request,
			},
			repositories: []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{URL: "https://example.com/owner/repo"},
			}},
			webhookSecret: "secret",
			wantRepoNil:   true,
			wantErr:       true,
			wantErrMsg:    "failed to run create status, user is not allowed to run the CI",
		},
		{
			name: "happy path",
			runevent: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://example.com/owner/repo",
				SHA:            "123abc",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Sender:         "owner",
				Request:        request,
			},
			repositories: []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{URL: "https://example.com/owner/repo"},
			}},
			webhookSecret: "secret",
			wantRepoNil:   false,
			wantErr:       false,
		},
	}

	pacInfo := &info.PacOpts{Settings: settings.DefaultSettings()}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseCtx, _ := rtesting.SetupFakeContext(t)
			ctx := info.StoreNS(baseCtx, "pac")

			ghClient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			// commit endpoint
			commitPath := fmt.Sprintf("/repos/%s/%s/git/commits/%s", tt.runevent.Organization, tt.runevent.Repository, tt.runevent.SHA)
			mux.HandleFunc(commitPath, func(rw http.ResponseWriter, _ *http.Request) {
				if tt.runevent.SHA == "" {
					rw.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprint(rw, `{"sha":"123abc","html_url":"https://example.com/commit/123abc","message":"msg"}`)
			})

			// org members empty
			mux.HandleFunc(fmt.Sprintf("/orgs/%s/members", tt.runevent.Organization), func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, `[]`) })
			// collaborator check – return 404 for non‐collaborator sender when defined
			if tt.runevent.Sender != "" && tt.runevent.Sender != tt.runevent.Organization {
				mux.HandleFunc(
					fmt.Sprintf("/repos/%s/%s/collaborators/%s", tt.runevent.Organization, tt.runevent.Repository, tt.runevent.Sender),
					func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusNotFound) },
				)
			}
			// status endpoint stub (used when CreateStatus is called)
			mux.HandleFunc(
				fmt.Sprintf("/repos/%s/%s/statuses/%s", tt.runevent.Organization, tt.runevent.Repository, tt.runevent.SHA),
				func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, `{}`) },
			)

			vcx := &ghprovider.Provider{Token: github.Ptr("token"), Logger: logger}
			vcx.SetGithubClient(ghClient)
			vcx.SetPacInfo(pacInfo)

			k8int := &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"pipelines-as-code-secret": tt.webhookSecret}}

			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{Repositories: tt.repositories /*Secret: []*corev1.Secret{secret}*/})
			in := info.NewInfo()
			cs := &params.Run{
				Info: in,
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
					Tekton:         stdata.Pipeline,
					Log:            logger,
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			ev := tt.runevent
			ev.Provider = &info.Provider{Token: "token", WebhookSecret: tt.webhookSecret}

			p := NewPacs(&ev, vcx, cs, pacInfo, k8int, logger, nil)
			repo, err := p.verifyRepoAndUser(ctx)
			assert.Assert(t, (err != nil) == tt.wantErr)

			if tt.wantErr {
				assert.ErrorContains(t, err, tt.wantErrMsg)
			}

			if tt.wantRepoNil {
				assert.Assert(t, repo == nil)
			} else {
				assert.Assert(t, repo != nil)
			}
		})
	}
}

func TestMatchRepoPRSkipsACLWithoutTektonDir(t *testing.T) {
	validTemplate := `apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: test-pr
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
spec:
  pipelineSpec:
    tasks:
    - name: task
      taskSpec:
        steps:
        - name: step
          image: alpine
          script: echo hello
`

	tests := []struct {
		name              string
		event             *info.Event
		tektonDirTemplate string
		allowIT           bool
		wantMatches       int
		wantLogSnippet    string
		dontWantLog       string
	}{
		{
			name: "PR from external user without tekton dir skips ACL and status",
			event: &info.Event{
				SHA:            "abc123",
				Organization:   "org",
				Repository:     "repo",
				URL:            "https://example.com/org/repo",
				HeadBranch:     "feature",
				BaseBranch:     "main",
				Sender:         "external-user",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Provider:       &info.Provider{Token: "test"},
			},
			tektonDirTemplate: "",
			allowIT:           false,
			wantMatches:       0,
			wantLogSnippet:    "fetched templates length=0",
			dontWantLog:       "checkUserAccess:",
		},
		{
			name: "push gitops comment without tekton dir skips ACL and status",
			event: &info.Event{
				SHA:            "abc123",
				Organization:   "org",
				Repository:     "repo",
				URL:            "https://example.com/org/repo",
				HeadBranch:     "main",
				BaseBranch:     "main",
				Sender:         "external-user",
				EventType:      opscomments.TestAllCommentEventType.String(),
				TriggerTarget:  triggertype.Push,
				InstallationID: 1,
				Provider:       &info.Provider{Token: "test"},
			},
			tektonDirTemplate: "",
			allowIT:           false,
			wantMatches:       0,
			wantLogSnippet:    "fetched templates length=0",
			dontWantLog:       "checkUserAccess:",
		},
		{
			name: "ok-to-test without tekton dir creates neutral status but skips ACL",
			event: &info.Event{
				SHA:            "abc123",
				Organization:   "org",
				Repository:     "repo",
				URL:            "https://example.com/org/repo",
				HeadBranch:     "feature",
				BaseBranch:     "main",
				Sender:         "maintainer",
				EventType:      opscomments.OkToTestCommentEventType.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Provider:       &info.Provider{Token: "test"},
			},
			tektonDirTemplate: "",
			allowIT:           true,
			wantMatches:       0,
			wantLogSnippet:    "fetched templates length=0",
			dontWantLog:       "checkUserAccess:",
		},
		{
			name: "PR from external user with tekton dir runs ACL and creates status",
			event: &info.Event{
				SHA:            "abc123",
				Organization:   "org",
				Repository:     "repo",
				URL:            "https://example.com/org/repo",
				HeadBranch:     "main",
				BaseBranch:     "main",
				Sender:         "external-user",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Provider:       &info.Provider{Token: "test"},
			},
			tektonDirTemplate: validTemplate,
			allowIT:           false,
			wantMatches:       0,
			wantLogSnippet:    "is not allowed to trigger CI",
		},
		{
			name: "PR from allowed user with tekton dir succeeds",
			event: &info.Event{
				SHA:            "abc123",
				Organization:   "org",
				Repository:     "repo",
				URL:            "https://example.com/org/repo",
				HeadBranch:     "main",
				BaseBranch:     "main",
				Sender:         "collaborator",
				EventType:      triggertype.PullRequest.String(),
				TriggerTarget:  triggertype.PullRequest,
				InstallationID: 1,
				Provider:       &info.Provider{Token: "test"},
			},
			tektonDirTemplate: validTemplate,
			allowIT:           true,
			wantMatches:       1,
			wantLogSnippet:    "checkUserAccess:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observerCore, logCatcher := zapobserver.New(zap.DebugLevel)
			logger := zap.New(observerCore).Sugar()

			baseCtx, _ := rtesting.SetupFakeContext(t)
			ctx := info.StoreNS(baseCtx, "pac")

			repositories := []*v1alpha1.Repository{{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "repo",
					Namespace: "ns",
				},
				Spec: v1alpha1.RepositorySpec{
					URL: "https://example.com/org/repo",
				},
			}}

			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Repositories: repositories,
			})

			in := info.NewInfo()
			cs := &params.Run{
				Info: in,
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
					Tekton:         stdata.Pipeline,
					Log:            logger,
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			vcx := &testprovider.TestProviderImp{
				AllowIT:           tt.allowIT,
				TektonDirTemplate: tt.tektonDirTemplate,
			}

			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					ApplicationName:    "Pipelines as Code CI",
					SecretAutoCreation: true,
					RemoteTasks:        true,
				},
			}

			k8int := &kitesthelper.KinterfaceTest{
				GetSecretResult: map[string]string{"webhook.secret": "secret"},
			}

			p := NewPacs(tt.event, vcx, cs, pacInfo, k8int, logger, nil)
			p.eventEmitter = events.NewEventEmitter(stdata.Kube, logger)

			matchedPRs, _, err := p.matchRepoPR(ctx)
			assert.NilError(t, err)
			assert.Equal(t, len(matchedPRs), tt.wantMatches)
			assert.Assert(t, logCatcher.FilterMessageSnippet(tt.wantLogSnippet).Len() > 0,
				"expected log containing %q, got: %v", tt.wantLogSnippet, logCatcher.All())
			if tt.dontWantLog != "" {
				assert.Equal(t, logCatcher.FilterMessageSnippet(tt.dontWantLog).Len(), 0,
					"unexpected log containing %q", tt.dontWantLog)
			}
		})
	}
}
