package pipelineascode

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v84/github"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const (
	testHubURL         = "https://mybelovedhub"
	testCatalogHubName = "tekton"
)

func replyString(mux *http.ServeMux, url, body string) {
	mux.HandleFunc(url, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, body)
	})
}

func testSetupCommonGhReplies(t *testing.T, mux *http.ServeMux, runevent info.Event, finalStatus, finalStatusText string, noReplyOrgPublicMembers bool) {
	t.Helper()
	// Take a directory and generate replies as Github for it
	replyString(mux,
		fmt.Sprintf("/repos/%s/%s/contents/internal/task", runevent.Organization, runevent.Repository),
		`{"sha": "internaltasksha"}`)

	replyString(mux,
		fmt.Sprintf("/repos/%s/%s/collaborators", runevent.Organization, runevent.Repository), `[]`)

	replyString(mux,
		fmt.Sprintf("/repos/%s/%s/statuses/%s", runevent.Organization, runevent.Repository, runevent.SHA),
		"{}")

	jj := fmt.Sprintf(`{"sha": "%s", "html_url": "https://git.commit.url/%s", "message": "commit message"}`,
		runevent.SHA, runevent.SHA)
	replyString(mux,
		fmt.Sprintf("/repos/%s/%s/git/commits/%s", runevent.Organization, runevent.Repository, runevent.SHA),
		jj)

	if !noReplyOrgPublicMembers {
		mux.HandleFunc("/orgs/"+runevent.Organization+"/members", func(rw http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(rw, `[{"login": "%s"}]`, runevent.Sender)
		})
	}

	replyString(mux,
		fmt.Sprintf("/repos/%s/%s/check-runs", runevent.Organization, runevent.Repository),
		`{"id": 26}`)

	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/check-runs/26", runevent.Organization, runevent.Repository),
		func(_ http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			created := github.CreateCheckRunOptions{}
			err := json.Unmarshal(body, &created)
			assert.NilError(t, err)
			// We created multiple status but the last one should be completed.
			// TODO: we could maybe refine this test
			if created.GetStatus() == "completed" {
				assert.Equal(t, created.GetConclusion(), finalStatus, "we got the status `%s` but we should have get the status `%s`", created.GetConclusion(), finalStatus)
				assert.Assert(t, strings.Contains(created.GetOutput().GetText(), finalStatusText),
					"GetStatus/CheckRun %s != %s", created.GetOutput().GetText(), finalStatusText)
			}
		})
}

func TestRun(t *testing.T) {
	var hubCatalogs sync.Map
	hubCatalogs.Store(
		"default", settings.HubCatalog{
			Index: "default",
			URL:   testHubURL,
			Name:  testCatalogHubName,
		})
	hubCatalogs.Store(
		"anotherHub", settings.HubCatalog{
			Index: "1",
			URL:   testHubURL,
			Name:  testCatalogHubName,
		})
	observer, log := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	tests := []struct {
		name                         string
		runevent                     info.Event
		tektondir                    string
		wantErr                      string
		finalStatus                  string
		finalStatusText              string
		repositories                 []*v1alpha1.Repository
		skipReplyingOrgPublicMembers bool
		expectedNumberofCleanups     int
		ProviderInfoFromRepo         bool
		WebHookSecretValue           string
		PayloadEncodedSecret         string
		concurrencyLimit             int
		expectedLogSnippet           string
		expectedPostedComment        string // TODO: multiple posted comments when we need it
		secretCreationError          error  // Error to inject for secret creation
	}{
		{
			name: "pull request/fail-to-start-apps",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				BaseBranch:    "main",
				Sender:        "fantasio",
				EventType:     "pull_request",
				TriggerTarget: "pull_request",
			},
			tektondir:       "testdata/pull_request",
			finalStatus:     "failure",
			finalStatusText: "we need at least one pipelinerun to start with",
		},
		{
			name: "pull request/bad-yaml",
			runevent: info.Event{
				SHA:               "principale",
				Organization:      "owner",
				Repository:        "lagaffe",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				Sender:            "owner",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
			},
			tektondir:             "testdata/bad_yaml",
			expectedPostedComment: ".*There are some errors in your PipelineRun template.*line 2: did not find expected key",
		},
		{
			name: "pull request/unknown-remotetask-but-fail-on-matching",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				BaseBranch:    "main",
				Sender:        "fantasio",
				EventType:     "push",
				TriggerTarget: "push",
			},
			tektondir:       "testdata/pull_request-nomatch-remotetask",
			finalStatus:     "failure",
			finalStatusText: "we need at least one pipelinerun to start with",
		},
		{
			name: "pull request/match-but-fail-to-start-on-unknown-remotetask",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "owner",
				Sender:            "owner",
				Repository:        "repo",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
				InstallationID:    1234,
			},
			tektondir:       "testdata/pull_request-nomatch-remotetask",
			finalStatus:     "failure",
			finalStatusText: "error getting remote task",
		},
		{
			name: "pull request/allowed",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "owner",
				Sender:            "owner",
				Repository:        "repo",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
				InstallationID:    1234,
			},
			tektondir:       "testdata/pull_request",
			finalStatus:     "neutral",
			finalStatusText: "<th>Status</th><th>Duration</th><th>Name</th>",
		},
		{
			name: "pull request/with webhook",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "organizationes",
				Repository:        "lagaffe",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				Sender:            "fantasio",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
			},
			tektondir:            "testdata/pull_request",
			finalStatus:          "neutral",
			finalStatusText:      "<th>Status</th><th>Duration</th><th>Name</th>",
			ProviderInfoFromRepo: true,
		},
		{
			name: "pull request/webhook secret new line",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "organizationes",
				Repository:        "lagaffe",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				Sender:            "fantasio",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
			},
			tektondir:            "testdata/pull_request",
			finalStatus:          "skipped",
			finalStatusText:      "<th>Status</th><th>Duration</th><th>Name</th>",
			ProviderInfoFromRepo: true,
			WebHookSecretValue:   "secret\n",
			PayloadEncodedSecret: "secret",
			expectedLogSnippet:   "it seems that we have detected a \\n or a space at the end",
		},
		{
			name: "pull request/webhook secret space at the end",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "organizationes",
				Repository:        "lagaffe",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				Sender:            "fantasio",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
			},
			tektondir:            "testdata/pull_request",
			finalStatus:          "skipped",
			finalStatusText:      "<th>Status</th><th>Duration</th><th>Name</th>",
			ProviderInfoFromRepo: true,
			WebHookSecretValue:   "secret ",
			PayloadEncodedSecret: "secret",
			expectedLogSnippet:   "it seems that we have detected a \\n or a space at the end",
		},
		{
			name: "Push/branch",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				Sender:        "fantasio",
				HeadBranch:    "refs/heads/main",
				BaseBranch:    "refs/heads/main",
				EventType:     "push",
				TriggerTarget: "push",
			},
			tektondir:   "testdata/push_branch",
			finalStatus: "neutral",
		},
		{
			name: "Push/tags",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				Sender:        "fantasio",
				HeadBranch:    "refs/tags/0.1",
				BaseBranch:    "refs/tags/0.1",
				EventType:     "push",
				TriggerTarget: "push",
			},
			tektondir:   "testdata/push_tags",
			finalStatus: "neutral",
		},

		// Skipped
		{
			name: "Skipped/Test no tekton dir",
			runevent: info.Event{
				SHA:          "principale",
				Organization: "organizationes",
				Repository:   "lagaffe",
				URL:          "https://service/documentation",
				HeadBranch:   "press",
				Sender:       "fantasio",
				BaseBranch:   "nomatch",
				EventType:    "pull_request",
			},
			tektondir:       "",
			finalStatus:     "skipped",
			finalStatusText: "directory for this repository",
		},
		// Skipped
		{
			name: "Skipped/Test on check_run",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				Sender:        "fantasio",
				BaseBranch:    "nomatch",
				TriggerTarget: "check_run",
				EventType:     "push",
			},
			tektondir:       "",
			finalStatus:     "skipped",
			finalStatusText: "directory for this repository",
		},
		{
			name: "Skipped/Test no repositories match",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				Sender:        "fantasio",
				BaseBranch:    "nomatch",
				EventType:     "pull_request",
				TriggerTarget: "pull_request",
			},
			tektondir:       "",
			finalStatus:     "skipped",
			finalStatusText: "not find a namespace match",
			repositories: []*v1alpha1.Repository{
				testnewrepo.NewRepo(
					testnewrepo.RepoTestcreationOpts{
						Name:             "test-run",
						URL:              "https://nowhere.com",
						InstallNamespace: "namespace",
					},
				),
			},
		},

		{
			name: "Skipped/User is not allowed",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				Sender:        "evilbro",
				BaseBranch:    "nomatch",
				EventType:     "pull_request",
				TriggerTarget: "pull_request",
			},
			tektondir:                    "testdata/pull_request",
			finalStatus:                  "skipped",
			finalStatusText:              "is not allowed to run CI on this repo",
			skipReplyingOrgPublicMembers: true,
		},
		{
			name: "allowed/push event even from non allowed user",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				Sender:        "evilbro",
				BaseBranch:    "main",
				EventType:     "push",
				TriggerTarget: "push",
			},
			tektondir:                    "testdata/push_branch",
			finalStatus:                  "skipped",
			skipReplyingOrgPublicMembers: true,
		},
		{
			name: "Keep max number of pipelineruns",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "press",
				BaseBranch:    "main",
				Sender:        "fantasio",
				EventType:     "pull_request",
				TriggerTarget: "pull_request",
			},
			tektondir:                "testdata/max-keep-runs",
			finalStatus:              "neutral",
			finalStatusText:          "PipelineRun has no taskruns",
			expectedNumberofCleanups: 10,
		},
		{
			name: "Do not allow unauthorized user to run CI on pushed commit",
			runevent: info.Event{
				SHA:           "principale",
				Organization:  "organizationes",
				Repository:    "lagaffe",
				URL:           "https://service/documentation",
				HeadBranch:    "main",
				BaseBranch:    "main",
				Sender:        "fantasio",
				EventType:     "test-all-comment",
				TriggerTarget: "push",
			},
			tektondir:                    "testdata/push_branch",
			finalStatus:                  "failure",
			finalStatusText:              "User fantasio is not allowed to trigger CI by GitOps comment on push commit in this repo.",
			skipReplyingOrgPublicMembers: true,
		},
		{
			name: "pull request/pipelinerun created in pending state (state changed by other controller)",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "owner",
				Sender:            "owner",
				Repository:        "repo",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
				InstallationID:    1234,
			},
			tektondir: "testdata/pending_pipelinerun",
		},
		{
			name: "pull request/pipelinerun created in pending state without installationID (state changed by other controller)",
			runevent: info.Event{
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Number: github.Ptr(666),
					},
				},
				SHA:               "fromwebhook",
				Organization:      "owner",
				Sender:            "owner",
				Repository:        "repo",
				URL:               "https://service/documentation",
				HeadBranch:        "press",
				BaseBranch:        "main",
				EventType:         "pull_request",
				TriggerTarget:     "pull_request",
				PullRequestNumber: 666,
			},
			tektondir: "testdata/pending_pipelinerun",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
			defer teardown()

			var providerURL string
			secrets := map[string]string{}
			webhookSecret := "don'tlookatmeplease"
			if tt.WebHookSecretValue != "" {
				webhookSecret = tt.WebHookSecretValue
			}
			payloadEncodedSecret := webhookSecret
			if tt.PayloadEncodedSecret != "" {
				payloadEncodedSecret = tt.PayloadEncodedSecret
			}

			repoToken := "repo-token"

			repo := testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				URL:              tt.runevent.URL,
				InstallNamespace: "namespace",
				ProviderURL:      providerURL,
				ConcurrencyLimit: tt.concurrencyLimit,
			}

			if tt.ProviderInfoFromRepo {
				secrets[repoToken] = "token"
				repo.SecretName = repoToken
				repo.WebhookSecretName = info.DefaultPipelinesAscodeSecretName
				secrets[info.DefaultPipelinesAscodeSecretName] = webhookSecret
			} else {
				secrets[info.DefaultPipelinesAscodeSecretName] = webhookSecret
			}

			if tt.repositories == nil {
				tt.repositories = []*v1alpha1.Repository{
					testnewrepo.NewRepo(repo),
				}
			}
			tdata := testclient.Data{
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "namespace",
						},
					},
				},
				Repositories: tt.repositories,
				PipelineRuns: []*pipelinev1.PipelineRun{
					tektontest.MakePRStatus("namespace", "force-me", []pipelinev1.ChildStatusReference{
						tektontest.MakeChildStatusReference("first"),
						tektontest.MakeChildStatusReference("last"),
						tektontest.MakeChildStatusReference("middle"),
					}, nil),
				},
			}

			testSetupCommonGhReplies(t, mux, tt.runevent, tt.finalStatus, tt.finalStatusText, tt.skipReplyingOrgPublicMembers)
			if tt.tektondir != "" {
				ghtesthelper.SetupGitTree(t, mux, tt.tektondir, &tt.runevent, false)
			} else {
				replyString(mux,
					fmt.Sprintf("/repos/%s/%s/git/trees/%s", tt.runevent.Organization, tt.runevent.Repository, tt.runevent.SHA),
					`{"sha": "`+tt.runevent.SHA+`", "tree": []}`)
			}

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", tt.runevent.Organization, tt.runevent.Repository, tt.runevent.PullRequestNumber),
				func(w http.ResponseWriter, req *http.Request) {
					if req.Method == http.MethodPost {
						_, _ = fmt.Fprintf(w, `{"id": %d}`, tt.runevent.PullRequestNumber)
						// read body and compare it
						body, _ := io.ReadAll(req.Body)
						expectedRegexp := regexp.MustCompile(tt.expectedPostedComment)
						assert.Assert(t, expectedRegexp.Match(body), "expected comment %s, got %s", tt.expectedPostedComment, string(body))
						return
					}
					_, _ = fmt.Fprint(w, `[]`)
				})

			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Log:            logger,
					Kube:           stdata.Kube,
					Tekton:         stdata.Pipeline,
				},
				Info: info.Info{
					Pac: &info.PacOpts{
						Settings: settings.Settings{
							HubCatalogs: &hubCatalogs,
						},
					},
					Controller: &info.ControllerInfo{
						Secret:           info.DefaultPipelinesAscodeSecretName,
						GlobalRepository: "global-repo",
					},
					Kube: &info.KubeOpts{
						Namespace: repo.InstallNamespace,
					},
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			mac := hmac.New(sha256.New, []byte(payloadEncodedSecret))
			payload := []byte(`{"iam": "batman"}`)
			mac.Write(payload)
			hexs := hex.EncodeToString(mac.Sum(nil))

			tt.runevent.Request = &info.Request{
				Header: map[string][]string{
					github.SHA256SignatureHeader: {"sha256=" + hexs},
				},
				Payload: payload,
			}
			tt.runevent.Provider = &info.Provider{
				URL:   ghTestServerURL,
				Token: "NONE",
			}
			ctx = info.StoreCurrentControllerName(ctx, "default")
			ctx = info.StoreNS(ctx, repo.InstallNamespace)

			kintTest := kitesthelper.KinterfaceTest{
				ConsoleURL:               "https://console.url",
				ExpectedNumberofCleanups: tt.expectedNumberofCleanups,
				GetSecretResult:          secrets,
			}

			var k8int kubeinteraction.Interface
			if tt.secretCreationError != nil {
				k8int = &KinterfaceTestWithError{
					KinterfaceTest:    kintTest,
					CreateSecretError: tt.secretCreationError,
				}
			} else {
				k8int = &kintTest
			}

			// InstallationID > 0 is used to detect if we are a GitHub APP
			tt.runevent.InstallationID = 12345
			if tt.ProviderInfoFromRepo {
				tt.runevent.InstallationID = 0
			}

			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					SecretAutoCreation: true,
					RemoteTasks:        true,
					HubCatalogs:        &hubCatalogs,
				},
			}
			vcx := &ghprovider.Provider{
				Run:    cs,
				Token:  github.Ptr("None"),
				Logger: logger,
			}
			vcx.SetGithubClient(fakeclient)
			vcx.SetPacInfo(pacInfo)
			p := NewPacs(&tt.runevent, vcx, cs, pacInfo, k8int, logger, nil)
			err := p.Run(ctx)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			assert.NilError(t, err)

			if tt.expectedLogSnippet != "" {
				logmsg := log.FilterMessageSnippet(tt.expectedLogSnippet).TakeAll()
				assert.Assert(t, len(logmsg) > 0, "log messages", logmsg, tt.expectedLogSnippet)
			}

			if tt.finalStatus != "skipped" {
				prs, err := cs.Clients.Tekton.TektonV1().PipelineRuns("").List(ctx, metav1.ListOptions{})
				assert.NilError(t, err)
				if len(prs.Items) == 0 {
					t.Error("failed to create pipelineRun for case: ", tt.name)
				}
				// validate logURL label
				for i := range prs.Items {
					pr := prs.Items[i]
					// skip existing seeded PipelineRuns
					if pr.GetName() == "force-me" {
						continue
					}
					logURL, ok := pr.Annotations[path.Join(apipac.GroupName, "log-url")]
					assert.Assert(t, ok, "failed to find log-url label on pipelinerun: %s/%s", pr.GetNamespace(), pr.GetGenerateName())
					assert.Equal(t, logURL, cs.Clients.ConsoleUI().DetailURL(&pr))

					if pacInfo.SecretAutoCreation {
						secretName, ok := pr.GetAnnotations()[keys.GitAuthSecret]
						assert.Assert(t, ok, "Cannot find secret %s on annotations", secretName)
					}
					if pr.Spec.Status == pipelinev1.PipelineRunSpecStatusPending {
						state, ok := pr.GetAnnotations()[keys.State]
						assert.Assert(t, ok, "State hasn't been set on PR", state)
						assert.Equal(t, state, kubeinteraction.StateQueued)

						// When PipelineRun is queued, SCMReportingPLRStarted should not be set
						_, scmStartedExists := pr.GetAnnotations()[keys.SCMReportingPLRStarted]
						assert.Assert(t, !scmStartedExists, "SCMReportingPLRStarted should not be set for queued PipelineRuns")
					} else {
						// When PipelineRun is not queued, SCMReportingPLRStarted should be set to "true"
						scmStarted, scmStartedExists := pr.GetAnnotations()[keys.SCMReportingPLRStarted]
						assert.Assert(t, scmStartedExists, "SCMReportingPLRStarted should be set for non-queued PipelineRuns")
						assert.Equal(t, scmStarted, "true", "SCMReportingPLRStarted should be 'true'")
					}
				}
			}
		})
	}
}

func TestGetLogURLMergePatch(t *testing.T) {
	con := consoleui.FallBackConsole{}
	clients := clients.Clients{}
	clients.SetConsoleUI(con)
	ann := map[string]string{keys.LogURL: con.URL()}
	result := getMergePatch(ann, map[string]string{})
	m, ok := result["metadata"].(map[string]any)
	assert.Assert(t, ok)
	a, ok := m["annotations"].(map[string]string)
	assert.Assert(t, ok)
	assert.Equal(t, a[path.Join(apipac.GroupName, "log-url")], con.URL())
}

func TestGetExecutionOrderPatch(t *testing.T) {
	tests := []struct {
		name  string
		order string
		want  string
	}{
		{
			name:  "single pr",
			order: "pull-pr-1",
			want:  "pull-pr-1",
		},
		{
			name:  "multiple prs",
			order: "pull-pr-1,pull-pr-2,pull-pr-3",
			want:  "pull-pr-1,pull-pr-2,pull-pr-3",
		},
		{
			name:  "empty",
			order: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getExecutionOrderPatch(tt.order)
			expected := map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						keys.ExecutionOrder: tt.want,
					},
				},
			}
			assert.DeepEqual(t, expected, result)
		})
	}
}

// KinterfaceTestWithError extends KinterfaceTest to allow injection of specific errors.
type KinterfaceTestWithError struct {
	kitesthelper.KinterfaceTest
	CreateSecretError error
}

func (k *KinterfaceTestWithError) CreateSecret(_ context.Context, _ string, _ *corev1.Secret) error {
	return k.CreateSecretError
}
