package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	knativeapi "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetOrUpdateCheckRunStatus(t *testing.T) {
	tests := []struct {
		name          string
		statusOpts    []providerstatus.StatusOpts
		expectPatched bool
	}{
		{
			name:          "create check run with pipeline run name",
			expectPatched: true,
			statusOpts: []providerstatus.StatusOpts{
				{
					PipelineRunName: "pr1",
					Status:          "hello moto",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pr1",
							Namespace: "default",
						},
					},
				},
			},
		},
		{
			name:          "multiple failed PipelineRuns only creates one check run",
			expectPatched: true,
			statusOpts: []providerstatus.StatusOpts{
				{
					PipelineRunName:          "",
					Title:                    "Failed",
					InstanceCountForCheckRun: 0,
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "failed-pr",
							Namespace: "default",
						},
					},
				},
				{
					PipelineRunName:          "",
					Title:                    "Failed",
					InstanceCountForCheckRun: 1,
				},
			},
		},
		{
			name:          "matched report patches PipelineRun",
			expectPatched: true,
			statusOpts: []providerstatus.StatusOpts{
				{
					PipelineRunName: "matched-pr",
					Status:          "completed",
					Conclusion:      providerstatus.ConclusionSuccess,
					Text:            "PipelineRun matched",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "matched-pr",
							Namespace: "default",
						},
					},
				},
			},
		},
		{
			name: "unmatched report skips PipelineRun patch",
			statusOpts: []providerstatus.StatusOpts{
				{
					PipelineRunName:   "unmatched-pr",
					Status:            "completed",
					Conclusion:        providerstatus.ConclusionSkipped,
					Text:              "PipelineRun not matched",
					IsUnmatchedReport: true,
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "unmatched-pr",
							Namespace: "default",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			l, _ := logger.GetLogger()
			run := params.New()
			testData := testclient.Data{}
			for _, so := range tt.statusOpts {
				if so.PipelineRun != nil {
					testData.PipelineRuns = append(testData.PipelineRuns, so.PipelineRun)
				}
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)
			run.Clients.Tekton = stdata.Pipeline

			var patched bool
			stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
				patched = true
				return false, nil, nil
			})

			cnx := Provider{
				ghClient: fakeclient,
				Run:      run,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: settings.PACApplicationNameDefaultValue,
					},
				},
				Logger: l,
			}

			mux.HandleFunc("/repos/check/info/check-runs", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"id": 555}`)
			})
			mux.HandleFunc("/repos/check/info/check-runs/555", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"id": 555}`)
			})

			event := &info.Event{
				Organization: "check",
				Repository:   "info",
				SHA:          "createCheckRunSHA",
			}

			for i := range tt.statusOpts {
				err := cnx.getOrUpdateCheckRunStatus(ctx, event, tt.statusOpts[i])
				assert.NilError(t, err)
			}
			assert.Equal(t, patched, tt.expectPatched)
		})
	}
}

func TestGetExistingCheckRunIDFromMultiple(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	client, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	l, _ := logger.GetLogger()
	cnx := &Provider{
		ghClient:      client,
		PaginedNumber: 1,
		Logger:        l,
	}
	event := &info.Event{
		Organization: "owner",
		Repository:   "repository",
		SHA:          "sha",
	}

	chosenOne := "chosenOne"
	chosenID := int64(55555)
	url := fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA)
	mux.HandleFunc(url, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			w.Header().Add("Link", `<https://api.github.com`+url+`?page=2&per_page=1>; rel="next"`)
			fmt.Fprint(w, `{}`)
		} else {
			_, _ = fmt.Fprintf(w, `{
			"total_count": 2,
			"check_runs": [
				{
					"id": %v,
					"external_id": "%s"
				},
				{
					"id": 123456,
					"external_id": "notworthy"
				}
			]
		}`, chosenID, chosenOne)
		}
	})

	id, err := cnx.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{
		PipelineRunName: chosenOne,
	})
	assert.NilError(t, err)
	assert.Assert(t, id != nil)
	assert.Equal(t, *id, chosenID)
}

func TestGetExistingPendingApprovalCheckRunID(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	client, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	l, _ := logger.GetLogger()
	cnx := New()
	cnx.SetGithubClient(client)
	cnx.SetLogger(l)

	event := &info.Event{
		Organization: "owner",
		Repository:   "repository",
		SHA:          "sha",
	}

	chosenOne := "chosenOne"
	chosenID := int64(55555)
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"total_count": 1,
			"check_runs": [
				{
					"id": %v,
					"external_id": "%s",
					"output": {
						"title": "%s",
						"summary": "My CI is waiting for approval"
					}
				}
			]
		}`, chosenID, chosenOne, pendingApproval)
	})

	id, err := cnx.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{
		PipelineRunName: chosenOne,
	})
	assert.NilError(t, err)
	assert.Equal(t, *id, chosenID)
}

func TestGetExistingFailedCheckRunID(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	client, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	l, _ := logger.GetLogger()
	cnx := New()
	cnx.SetGithubClient(client)
	cnx.SetLogger(l)

	event := &info.Event{
		Organization: "owner",
		Repository:   "repository",
		SHA:          "sha",
	}

	chosenOne := "chosenOne"
	chosenID := int64(55555)
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"total_count": 1,
			"check_runs": [
				{
					"id": %v,
					"external_id": "%s",
					"output": {
						"title": "Failed",
						"summary": "CI is failed to run"
					}
				}
			]
		}`, chosenID, chosenOne)
	})

	id, err := cnx.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{
		PipelineRunName: chosenOne,
	})
	assert.NilError(t, err)
	assert.Equal(t, *id, chosenID)
}

func TestGithubProviderCreateStatus(t *testing.T) {
	checkrunid := int64(2026)
	resultid := int64(666)
	runEvent := info.NewEvent()
	prname := "pr1"
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: prname,
			Annotations: map[string]string{
				keys.CheckRunID: strconv.Itoa(int(checkrunid)),
			},
		},
	}
	runEvent.Organization = "check"
	runEvent.Repository = "run"

	type args struct {
		runevent           *info.Event
		status             string
		conclusion         string
		text               string
		detailsURL         string
		titleSubstr        string
		nilCompletedAtDate bool
		githubApps         bool
		accessDenied       bool
		isBot              bool
	}
	tests := []struct {
		name                 string
		args                 args
		pr                   *tektonv1.PipelineRun
		want                 *github.CheckRun
		wantErr              bool
		notoken              bool
		addExistingCheckruns bool
	}{
		{
			name: "success",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "success",
				text:        "Yay",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Success",
				githubApps:  true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "success with using existing pending approval run checkrun",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "success",
				text:        "Yay",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Success",
				githubApps:  true,
			},
			pr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: prname,
				},
			},
			addExistingCheckruns: true,
			want:                 &github.CheckRun{ID: &resultid},
			wantErr:              false,
		},
		{
			name: "success coming from webhook",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "success",
				text:        "Yay",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Success",
				githubApps:  false,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "in_progress",
			args: args{
				runevent:           runEvent,
				status:             "in_progress",
				conclusion:         "",
				text:               "Yay",
				detailsURL:         "https://cireport.com",
				nilCompletedAtDate: true,
				githubApps:         true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "failure",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "failure",
				text:        "Nay",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Failed",
				githubApps:  true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "validation failure",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "failure",
				text:        "There was an error creating the PipelineRun: ```admission webhook \"validation.webhook.pipeline.tekton.dev\" denied the request: validation failed: invalid value: 0s```",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Failed",
				githubApps:  true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "failure from bot",
			args: args{
				runevent:     runEvent,
				status:       "completed",
				conclusion:   "failure",
				text:         "Nay",
				detailsURL:   "https://cireport.com",
				titleSubstr:  "Failed",
				githubApps:   true,
				accessDenied: true,
				isBot:        true,
			},
			wantErr: false,
		},
		{
			name: "success from bot",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "failure",
				text:        "Nay",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Failed",
				githubApps:  true,
				isBot:       true,
			},
			wantErr: false,
			want:    &github.CheckRun{ID: &resultid},
		},
		{
			name: "skipped",
			args: args{
				runevent:    runEvent,
				status:      "queued",
				conclusion:  "pending",
				text:        "Skipit",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Pending",
				githubApps:  true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "skipped via github apps",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "skipped",
				text:        "PipelineRun not matched",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Skipped",
				githubApps:  true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "skipped via webhook",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "skipped",
				text:        "PipelineRun not matched",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Skipped",
				githubApps:  false,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name: "unknown",
			args: args{
				runevent:    runEvent,
				status:      "completed",
				conclusion:  "neutral",
				text:        "Je says pas ce qui se passe wesh",
				detailsURL:  "https://cireport.com",
				titleSubstr: "Unknown",
				githubApps:  true,
			},
			want:    &github.CheckRun{ID: &resultid},
			wantErr: false,
		},
		{
			name:    "no token set",
			wantErr: true,
			notoken: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			ctx, _ := rtesting.SetupFakeContext(t)
			gcvs := New()
			gcvs.SetGithubClient(fakeclient)
			gcvs.Logger, _ = logger.GetLogger()
			gcvs.Run = params.New()
			if tt.args.isBot {
				gcvs.userType = "Bot"
			}

			checkRunCreated := false
			mux.HandleFunc("/repos/check/run/statuses/sha", func(_ http.ResponseWriter, _ *http.Request) {})
			mux.HandleFunc(fmt.Sprintf("/repos/check/run/check-runs/%d", checkrunid), func(rw http.ResponseWriter, r *http.Request) {
				bit, _ := io.ReadAll(r.Body)
				checkRun := &github.CheckRun{}
				err := json.Unmarshal(bit, checkRun)
				assert.NilError(t, err)
				checkRunCreated = true
				if tt.args.nilCompletedAtDate {
					// I guess that's the way you check for an undefined year,
					// or maybe i don't understand fully how go works😅
					assert.Assert(t, checkRun.GetCompletedAt().Year() == 0o001)
				}
				assert.Equal(t, checkRun.GetStatus(), tt.args.status)
				// pending status is not provided by GitHub its something added to handle skipped part from PAC side
				if tt.args.conclusion != "pending" {
					assert.Equal(t, checkRun.GetConclusion(), tt.args.conclusion)
				}
				assert.Equal(t, checkRun.Output.GetText(), tt.args.text)
				assert.Equal(t, checkRun.GetDetailsURL(), tt.args.detailsURL)
				assert.Assert(t, strings.Contains(checkRun.Output.GetTitle(), tt.args.titleSubstr))
				_, err = fmt.Fprintf(rw, `{"id": %d}`, resultid)
				assert.NilError(t, err)
			})

			if tt.addExistingCheckruns {
				tt.args.runevent.SHA = "sha"
				mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", tt.args.runevent.Organization, tt.args.runevent.Repository, tt.args.runevent.SHA), func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{
						"total_count": 1,
						"check_runs": [
							{
								"id": %v,
								"external_id": "%v",
                                "status": "queued",
                                "conclusion": "pending", 
								"output": {
									"title": "Pending approval, waiting for an /ok-to-test",
									"summary": "My CI is waiting for approval"
								}
							}
						]
					}`, checkrunid, resultid)
				})
			}

			status := providerstatus.StatusOpts{
				PipelineRunName: prname,
				PipelineRun:     pr,
				Status:          tt.args.status,
				Conclusion:      providerstatus.Conclusion(tt.args.conclusion),
				Text:            tt.args.text,
				DetailsURL:      tt.args.detailsURL,
				AccessDenied:    tt.args.accessDenied,
			}
			if tt.pr != nil {
				status.PipelineRun = tt.pr
			}
			if tt.notoken {
				tt.args.runevent = info.NewEvent()
			} else {
				tt.args.runevent.Provider = &info.Provider{
					Token: "hello",
					URL:   "moto",
				}
				if tt.args.githubApps {
					tt.args.runevent.InstallationID = 12345
				} else {
					tt.args.runevent.SHA = "sha"
				}
			}

			testData := testclient.Data{}
			if tt.pr != nil {
				testData = testclient.Data{
					PipelineRuns: []*tektonv1.PipelineRun{tt.pr},
				}
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)
			fakeClients := clients.Clients{
				Tekton: stdata.Pipeline,
			}
			gcvs.Run.Clients = fakeClients
			gcvs.SetPacInfo(&info.PacOpts{
				Settings: settings.Settings{
					ApplicationName: settings.PACApplicationNameDefaultValue,
				},
			})
			err := gcvs.CreateStatus(ctx, tt.args.runevent, status)
			if (err != nil) != tt.wantErr {
				t.Errorf("GithubProvider.CreateStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.want == nil && checkRunCreated {
				t.Errorf("Check run should have not be created for this test")
				return
			}
			if tt.want != nil && !checkRunCreated {
				t.Errorf("Check run should have been created for this test")
				return
			}
		})
	}
}

func TestGithubProvidercreateStatusCommit(t *testing.T) {
	commentCreationAPICalled := false
	issuenumber := 666
	anevent := &info.Event{
		Event:             &github.PullRequestEvent{PullRequest: &github.PullRequest{Number: new(issuenumber)}},
		Organization:      "owner",
		Repository:        "repository",
		SHA:               "createStatusCommitSHA",
		EventType:         "pull_request",
		PullRequestNumber: issuenumber,
	}
	tests := []struct {
		name                         string
		event                        *info.Event
		repo                         *v1alpha1.Repository
		wantErr                      bool
		wantCommentCreationAPICalled bool
		status                       providerstatus.StatusOpts
		expectedConclusion           string
	}{
		{
			name:  "completed",
			event: anevent,
			status: providerstatus.StatusOpts{
				Status:     "completed",
				Summary:    "I just wanna say",
				Text:       "Finito amigo",
				Conclusion: "completed",
			},
			expectedConclusion:           "completed",
			wantCommentCreationAPICalled: true,
		},
		{
			name:  "in_progress",
			event: anevent,
			status: providerstatus.StatusOpts{
				Status: "in_progress",
			},
			expectedConclusion: "pending",
		},
		{
			name:  "pull_request status pending",
			event: anevent,
			status: providerstatus.StatusOpts{
				Conclusion: "pending",
			},
			expectedConclusion: "pending",
		},
		{
			name:  "pull_request status neutral",
			event: anevent,
			status: providerstatus.StatusOpts{
				Conclusion: "neutral",
			},
			expectedConclusion: "success",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commentCreationAPICalled = false
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/statuses/%s",
				tt.event.Organization, tt.event.Repository, tt.event.SHA), func(_ http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				assert.Check(t, strings.Contains(string(body), fmt.Sprintf(`"state":"%s"`, tt.expectedConclusion)))
			})
			if tt.status.Status == "completed" {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
					tt.event.Organization, tt.event.Repository, issuenumber), func(_ http.ResponseWriter, r *http.Request) {
					commentCreationAPICalled = true
					body, _ := io.ReadAll(r.Body)
					assert.Equal(t, fmt.Sprintf(`{"body":"%s<br>%s"}`, tt.status.Summary, tt.status.Text)+"\n", string(body))
				})
			}

			ctx, _ := rtesting.SetupFakeContext(t)
			l, _ := logger.GetLogger()
			provider := &Provider{
				ghClient: fakeclient,
				Run:      params.New(),
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: settings.PACApplicationNameDefaultValue,
					},
				},
				Logger: l,
			}

			if tt.repo != nil {
				provider.repo = tt.repo
			}

			if err := provider.createStatusCommit(ctx, tt.event, tt.status); (err != nil) != tt.wantErr {
				t.Errorf("GetCommitInfo() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.wantCommentCreationAPICalled, commentCreationAPICalled)
		})
	}
}

func TestProviderGetExistingCheckRunID(t *testing.T) {
	idd := int64(55555)
	tests := []struct {
		name       string
		jsonret    string
		expectedID *int64
		wantErr    bool
		prname     string
	}{
		{
			name: "has check runs",
			jsonret: `{
			"total_count": 1,
			"check_runs": [
				{
					"id": 55555,
					"external_id": "blahpr"
				}
			]
		}`,
			expectedID: &idd,
			prname:     "blahpr",
		},
		{
			name:       "no check runs",
			jsonret:    `{"total_count": 0,"check_runs": []}`,
			expectedID: nil,
		},
		{
			name:       "error it",
			jsonret:    `BLAHALALACLCALWA`,
			expectedID: nil,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			client, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			event := &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "sha",
			}
			l, _ := logger.GetLogger()
			v := &Provider{
				ghClient: client,
				Logger:   l,
			}
			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA), func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, "%s", tt.jsonret)
			})

			got, err := v.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{
				PipelineRunName: tt.prname,
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("getExistingCheckRunID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expectedID) {
				t.Errorf("getExistingCheckRunID() got = %v, want %v", got, tt.expectedID)
			}
		})
	}
}

func TestGetExistingCheckRunIDCache(t *testing.T) {
	tests := []struct {
		name            string
		jsonret         string
		failFirstN      int
		goroutines      int
		secondLookup    string
		expectedID      int64
		expectedAPIHits int64
	}{
		{
			name:            "second call serves from cache",
			jsonret:         `{"total_count": 2, "check_runs": [{"id": 55555, "external_id": "mypr"}, {"id": 55556, "external_id": "mypr2"}]}`,
			secondLookup:    "mypr2",
			expectedID:      55555,
			expectedAPIHits: 1,
		},
		{
			name:            "concurrent calls share single fetch",
			jsonret:         `{"total_count": 2, "check_runs": [{"id": 55555, "external_id": "mypr"}, {"id": 55556, "external_id": "mypr2"}]}`,
			goroutines:      10,
			expectedAPIHits: 1,
		},
		{
			name:            "retries on transient error",
			jsonret:         `{"total_count": 1, "check_runs": [{"id": 77777, "external_id": "mypr"}]}`,
			failFirstN:      1,
			expectedID:      77777,
			expectedAPIHits: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			client, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			event := &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "sha",
			}

			var apiHits atomic.Int64
			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA), func(w http.ResponseWriter, _ *http.Request) {
				hit := apiHits.Add(1)
				if int(hit) <= tt.failFirstN {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				fmt.Fprint(w, tt.jsonret)
			})

			l, _ := logger.GetLogger()
			cnx := New()
			cnx.SetGithubClient(client)
			cnx.SetLogger(l)

			if tt.goroutines > 1 {
				var wg sync.WaitGroup
				wg.Add(tt.goroutines)
				for range tt.goroutines {
					go func() {
						defer wg.Done()
						_, _ = cnx.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{PipelineRunName: "mypr"})
					}()
				}
				wg.Wait()
			} else {
				id, err := cnx.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{PipelineRunName: "mypr"})
				assert.NilError(t, err)
				if tt.expectedID != 0 {
					assert.Assert(t, id != nil)
					assert.Equal(t, *id, tt.expectedID)
				}
				if tt.secondLookup != "" {
					id2, err := cnx.getExistingCheckRunID(ctx, event, providerstatus.StatusOpts{PipelineRunName: tt.secondLookup})
					assert.NilError(t, err)
					assert.Assert(t, id2 != nil)
				}
			}

			assert.Equal(t, apiHits.Load(), tt.expectedAPIHits)
		})
	}
}

func TestUpdateCheckRunRetryNotFound(t *testing.T) {
	checkRunID := int64(2026)
	tests := []struct {
		name            string
		retryNotFound   bool
		notFoundFirstN  int
		serverErr       bool
		cancelContext   bool
		wantErr         bool
		expectedAPIHits int64
	}{
		{
			name:            "transient 404 after creation recovers",
			retryNotFound:   true,
			notFoundFirstN:  2,
			expectedAPIHits: 3,
		},
		{
			name:            "404 exhausts the retry budget",
			retryNotFound:   true,
			notFoundFirstN:  checkRunUpdateMaxRetries + 1,
			wantErr:         true,
			expectedAPIHits: checkRunUpdateMaxRetries + 1,
		},
		{
			name:            "404 on a pre-existing check run is not retried",
			notFoundFirstN:  1,
			wantErr:         true,
			expectedAPIHits: 1,
		},
		{
			name:            "non 404 error is not retried",
			retryNotFound:   true,
			serverErr:       true,
			wantErr:         true,
			expectedAPIHits: 1,
		},
		{
			name:            "cancelled context stops the retry",
			retryNotFound:   true,
			notFoundFirstN:  checkRunUpdateMaxRetries + 1,
			cancelContext:   true,
			wantErr:         true,
			expectedAPIHits: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			event := &info.Event{Organization: "check", Repository: "run"}

			var apiHits atomic.Int64
			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs/%d", event.Organization, event.Repository, checkRunID),
				func(w http.ResponseWriter, _ *http.Request) {
					hit := apiHits.Add(1)
					switch {
					case tt.serverErr:
						w.WriteHeader(http.StatusInternalServerError)
					case int(hit) <= tt.notFoundFirstN:
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"message": "Not Found"}`)
					default:
						fmt.Fprintf(w, `{"id": %d}`, checkRunID)
					}
				})

			l, _ := logger.GetLogger()
			cnx := New()
			cnx.SetGithubClient(fakeclient)
			cnx.SetLogger(l)

			fc := clockwork.NewFakeClock()
			cnx.clock = fc
			go func() {
				for range checkRunUpdateMaxRetries {
					if err := fc.BlockUntilContext(ctx, 1); err != nil {
						return
					}
					if tt.cancelContext {
						cancel()
						return
					}
					fc.Advance(time.Minute)
				}
			}()

			err := cnx.updateCheckRun(ctx, event, checkRunID, github.UpdateCheckRunOptions{Name: "test"}, tt.retryNotFound)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, tt.expectedAPIHits, apiHits.Load())
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
		},
		{
			name: "plain error",
			err:  fmt.Errorf("boom"),
		},
		{
			name: "error response without a response",
			err:  &github.ErrorResponse{Message: "nope"},
		},
		{
			name: "error response with another status",
			err:  &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
		},
		{
			name: "not found error response",
			err:  &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}},
			want: true,
		},
		{
			name: "wrapped not found error response",
			err:  fmt.Errorf("update failed: %w", &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNotFoundError(tt.err))
		})
	}
}

func TestGetOrUpdateCheckRunStatusNotFound(t *testing.T) {
	tests := []struct {
		name              string
		annotatedCheckRun bool
		notFoundFirstN    int
		wantErr           bool
		expectedAPIHits   int64
	}{
		{
			name:            "created check run is not updated again",
			notFoundFirstN:  1,
			expectedAPIHits: 0,
		},
		{
			name:              "annotated check run retries a transient 404",
			annotatedCheckRun: true,
			notFoundFirstN:    1,
			expectedAPIHits:   2,
		},
		{
			name:              "annotated check run gives up after the last retry",
			annotatedCheckRun: true,
			notFoundFirstN:    checkRunUpdateMaxRetries + 1,
			wantErr:           true,
			expectedAPIHits:   checkRunUpdateMaxRetries + 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel, _ := rtesting.SetupFakeContextWithCancel(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			checkRunID := int64(555)
			event := &info.Event{Organization: "check", Repository: "info", SHA: "createCheckRunSHA"}

			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA),
				func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, `{"total_count": 0, "check_runs": []}`)
				})
			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs", event.Organization, event.Repository),
				func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(w, `{"id": %d}`, checkRunID)
				})

			var apiHits atomic.Int64
			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs/%d", event.Organization, event.Repository, checkRunID),
				func(w http.ResponseWriter, _ *http.Request) {
					hit := apiHits.Add(1)
					if int(hit) <= tt.notFoundFirstN {
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"message": "Not Found"}`)
						return
					}
					fmt.Fprintf(w, `{"id": %d}`, checkRunID)
				})

			l, _ := logger.GetLogger()
			cnx := New()
			cnx.SetGithubClient(fakeclient)
			cnx.SetLogger(l)
			cnx.Run = params.New()
			cnx.SetPacInfo(&info.PacOpts{
				Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue},
			})

			fc := clockwork.NewFakeClock()
			cnx.clock = fc
			clockDone := make(chan struct{})
			go func() {
				defer close(clockDone)
				for range checkRunUpdateMaxRetries {
					if err := fc.BlockUntilContext(ctx, 1); err != nil {
						return
					}
					fc.Advance(time.Minute)
				}
			}()
			defer func() {
				cancel()
				<-clockDone
			}()

			statusOpts := providerstatus.StatusOpts{PipelineRunName: "pr1", Status: "in_progress"}
			if tt.annotatedCheckRun {
				statusOpts.PipelineRun = &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{keys.CheckRunID: strconv.FormatInt(checkRunID, 10)},
					},
				}
			}

			err := cnx.getOrUpdateCheckRunStatus(ctx, event, statusOpts)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, tt.expectedAPIHits, apiHits.Load())
		})
	}
}

func TestGetOrUpdateCheckRunStatusCreatesWithFullState(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	checkRunID := int64(777)
	event := &info.Event{Organization: "check", Repository: "info", SHA: "createCheckRunSHA"}

	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA),
		func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"total_count": 0, "check_runs": []}`)
		})

	var created github.CreateCheckRunOptions
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs", event.Organization, event.Repository),
		func(w http.ResponseWriter, r *http.Request) {
			assert.NilError(t, json.NewDecoder(r.Body).Decode(&created))
			fmt.Fprintf(w, `{"id": %d}`, checkRunID)
		})

	var updates atomic.Int64
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs/%d", event.Organization, event.Repository, checkRunID),
		func(w http.ResponseWriter, _ *http.Request) {
			updates.Add(1)
			fmt.Fprintf(w, `{"id": %d}`, checkRunID)
		})

	l, _ := logger.GetLogger()
	cnx := New()
	cnx.SetGithubClient(fakeclient)
	cnx.SetLogger(l)
	cnx.Run = params.New()
	cnx.SetPacInfo(&info.PacOpts{
		Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue},
	})

	err := cnx.getOrUpdateCheckRunStatus(ctx, event, providerstatus.StatusOpts{
		PipelineRunName: "pr1",
		Status:          "completed",
		Conclusion:      providerstatus.ConclusionFailure,
		Title:           "Failed",
		Summary:         "it failed",
		Text:            "the details",
		DetailsURL:      "https://console/logs",
	})
	assert.NilError(t, err)

	assert.Equal(t, int64(0), updates.Load(), "the freshly created check run should not be updated again")
	assert.Equal(t, "failure", created.GetConclusion())
	assert.Assert(t, !created.GetCompletedAt().IsZero())
	assert.Equal(t, "it failed", created.GetOutput().GetSummary())
	assert.Equal(t, "the details", created.GetOutput().GetText())
}

func TestCreateCheckRunStatusCarriesAnnotations(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	event := &info.Event{Organization: "check", Repository: "info", SHA: "createCheckRunSHA"}

	var created github.CreateCheckRunOptions
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs", event.Organization, event.Repository),
		func(w http.ResponseWriter, r *http.Request) {
			assert.NilError(t, json.NewDecoder(r.Body).Decode(&created))
			fmt.Fprint(w, `{"id": 779}`)
		})

	l, _ := logger.GetLogger()
	cnx := New()
	cnx.SetGithubClient(fakeclient)
	cnx.SetLogger(l)
	cnx.Run = params.New()
	cnx.SetPacInfo(&info.PacOpts{
		Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue},
	})

	output := &github.CheckRunOutput{
		Title:   new("Failed"),
		Summary: new("it failed"),
		Text:    new("the details"),
		Annotations: []*github.CheckRunAnnotation{
			{
				Path:            new("main.go"),
				StartLine:       new(12),
				EndLine:         new(12),
				AnnotationLevel: new("failure"),
				Message:         new("undefined: foo"),
			},
		},
	}

	id, err := cnx.createCheckRunStatus(ctx, event, providerstatus.StatusOpts{
		PipelineRunName: "pr1",
		Status:          "completed",
		Conclusion:      providerstatus.ConclusionFailure,
	}, output, "failure")
	assert.NilError(t, err)
	assert.Equal(t, int64(779), *id)

	annotations := created.GetOutput().Annotations
	assert.Equal(t, 1, len(annotations))
	assert.Equal(t, "main.go", annotations[0].GetPath())
	assert.Equal(t, "undefined: foo", annotations[0].GetMessage())
	assert.Equal(t, "failure", created.GetConclusion())
	assert.Assert(t, !created.GetCompletedAt().IsZero())
}

func TestGetOrUpdateCheckRunStatusCancelledOnCreate(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	event := &info.Event{Organization: "check", Repository: "info", SHA: "createCheckRunSHA"}
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/commits/%v/check-runs", event.Organization, event.Repository, event.SHA),
		func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"total_count": 0, "check_runs": []}`)
		})

	var created github.CreateCheckRunOptions
	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/check-runs", event.Organization, event.Repository),
		func(w http.ResponseWriter, r *http.Request) {
			assert.NilError(t, json.NewDecoder(r.Body).Decode(&created))
			fmt.Fprint(w, `{"id": 778}`)
		})

	l, _ := logger.GetLogger()
	cnx := New()
	cnx.SetGithubClient(fakeclient)
	cnx.SetLogger(l)
	cnx.Run = params.New()
	cnx.SetPacInfo(&info.PacOpts{
		Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue},
	})

	err := cnx.getOrUpdateCheckRunStatus(ctx, event, providerstatus.StatusOpts{
		Status:     "completed",
		Conclusion: providerstatus.ConclusionFailure,
		PipelineRun: &tektonv1.PipelineRun{
			Spec: tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusCancelled},
		},
	})
	assert.NilError(t, err)
	assert.Equal(t, "cancelled", created.GetConclusion())
}

func TestFormatPipelineComment(t *testing.T) {
	v := &Provider{}
	tests := []struct {
		name         string
		status       providerstatus.StatusOpts
		wantTitle    string
		wantEmoji    string
		wantSummary  string
		wantContains string
	}{
		{
			name:      "queued",
			status:    providerstatus.StatusOpts{Status: "queued", Summary: "queued summary", Text: "queued text", OriginalPipelineRunName: "unit"},
			wantTitle: "Queued",
			wantEmoji: "⏳",
		},
		{
			name:      "in progress",
			status:    providerstatus.StatusOpts{Status: "in_progress", Summary: "running summary", Text: "running text", OriginalPipelineRunName: "unit"},
			wantTitle: "Running",
			wantEmoji: "🚀",
		},
		{
			name:      "completed success",
			status:    providerstatus.StatusOpts{Status: "completed", Conclusion: providerstatus.ConclusionSuccess, Summary: "success summary", Text: "success text", OriginalPipelineRunName: "unit"},
			wantTitle: "Success",
			wantEmoji: "✅",
		},
		{
			name:      "completed failure",
			status:    providerstatus.StatusOpts{Status: "completed", Conclusion: providerstatus.ConclusionFailure, Summary: "failure summary", Text: "failure text", OriginalPipelineRunName: "unit"},
			wantTitle: "Failed",
			wantEmoji: "❌",
		},
		{
			name:      "completed cancelled",
			status:    providerstatus.StatusOpts{Status: "completed", Conclusion: providerstatus.ConclusionCancelled, Summary: "cancelled summary", Text: "cancelled text", OriginalPipelineRunName: "unit"},
			wantTitle: "Cancelled",
			wantEmoji: "⚠️",
		},
		{
			name:      "completed neutral",
			status:    providerstatus.StatusOpts{Status: "completed", Conclusion: providerstatus.ConclusionNeutral, Summary: "neutral summary", Text: "neutral text", OriginalPipelineRunName: "unit"},
			wantTitle: "Completed",
			wantEmoji: "ℹ️",
		},
		{
			name:      "default status",
			status:    providerstatus.StatusOpts{Status: "waiting", Summary: "default summary", Text: "default text", OriginalPipelineRunName: "unit"},
			wantTitle: "Status Update",
			wantEmoji: "ℹ️",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.formatPipelineComment("abc123", tt.status)
			assert.Assert(t, strings.HasPrefix(got, tt.wantEmoji+" "), "expected emoji %q in %q", tt.wantEmoji, got)
			assert.Assert(t, strings.Contains(got, "**"+tt.wantTitle+": unit for abc123**"), "expected title %q in %q", tt.wantTitle, got)
			assert.Assert(t, strings.Contains(got, tt.status.Summary+"<br>"+tt.status.Text), "expected summary/text in %q", got)
		})
	}
}

func TestCreateStatusCommitCommentStrategies(t *testing.T) {
	tests := []struct {
		name                  string
		commentStrategy       string
		eventType             string
		status                providerstatus.StatusOpts
		setup                 func(t *testing.T, mux *http.ServeMux, event *info.Event, got *statusCommitCommentCalls)
		wantErr               string
		wantState             string
		wantCreated           bool
		wantPatched           bool
		wantNoCommentRequests bool
		wantEventReason       string
	}{
		{
			name:            "update strategy creates comment for queued pending approval",
			commentStrategy: provider.UpdateCommentStrategy,
			eventType:       triggertype.PullRequest.String(),
			status: providerstatus.StatusOpts{
				Status:                  "queued",
				Conclusion:              providerstatus.ConclusionPending,
				Title:                   pendingApproval,
				Summary:                 "Pipelines as Code CI/demo is waiting for approval.",
				Text:                    "please approve",
				OriginalPipelineRunName: "demo",
			},
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event, got *statusCommitCommentCalls) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", event.Organization, event.Repository, event.PullRequestNumber), func(rw http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						fmt.Fprint(rw, `[]`)
					case http.MethodPost:
						got.created = true
						body, err := io.ReadAll(r.Body)
						assert.NilError(t, err)
						assert.Assert(t, strings.Contains(string(body), "<!-- pac-status-demo -->"))
						assert.Assert(t, strings.Contains(string(body), "⏳ **Queued: demo for sha**"))
						fmt.Fprint(rw, `{"id": 222}`)
					default:
						t.Fatalf("unexpected method %s", r.Method)
					}
				})
			},
			wantState:   "pending",
			wantCreated: true,
		},
		{
			name:            "update strategy edits existing own comment",
			commentStrategy: provider.UpdateCommentStrategy,
			eventType:       triggertype.PullRequest.String(),
			status: providerstatus.StatusOpts{
				Status:                  "completed",
				Conclusion:              providerstatus.ConclusionFailure,
				Summary:                 "Pipelines as Code CI/demo has <b>failed</b>.",
				Text:                    "failed details",
				OriginalPipelineRunName: "demo",
			},
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event, got *statusCommitCommentCalls) {
				t.Helper()
				marker := "<!-- pac-status-demo -->"
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", event.Organization, event.Repository, event.PullRequestNumber), func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodGet, r.Method)
					fmt.Fprintf(rw, `[{"id":111,"body":"%s old body","user":{"login":"pac-user"},"created_at":"2024-01-01T00:00:00Z"}]`, marker)
				})
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/comments/111", event.Organization, event.Repository), func(rw http.ResponseWriter, r *http.Request) {
					got.patched = true
					assert.Equal(t, http.MethodPatch, r.Method)
					body, err := io.ReadAll(r.Body)
					assert.NilError(t, err)
					assert.Assert(t, strings.Contains(string(body), "❌ **Failed: demo for sha**"))
					fmt.Fprint(rw, `{"id": 111}`)
				})
			},
			wantState:   "failure",
			wantPatched: true,
		},
		{
			name:            "update strategy emits event when comment update fails",
			commentStrategy: provider.UpdateCommentStrategy,
			eventType:       triggertype.PullRequest.String(),
			status: providerstatus.StatusOpts{
				Status:                  "completed",
				Conclusion:              providerstatus.ConclusionFailure,
				Summary:                 "Pipelines as Code CI/demo has <b>failed</b>.",
				Text:                    "failed details",
				OriginalPipelineRunName: "demo",
			},
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event, _ *statusCommitCommentCalls) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", event.Organization, event.Repository, event.PullRequestNumber), func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodGet, r.Method)
					rw.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(rw, `{"message":"boom"}`)
				})
			},
			wantErr:         "boom",
			wantState:       "failure",
			wantEventReason: "PipelineRunCommentCreationError",
		},
		{
			name:            "disable all strategy skips comments",
			commentStrategy: provider.DisableAllCommentStrategy,
			eventType:       triggertype.PullRequest.String(),
			status: providerstatus.StatusOpts{
				Status:                  "completed",
				Conclusion:              providerstatus.ConclusionSuccess,
				Summary:                 "Pipelines as Code CI/demo has <b>successfully</b> validated your commit.",
				Text:                    "success details",
				OriginalPipelineRunName: "demo",
			},
			wantState:             "success",
			wantNoCommentRequests: true,
		},
		{
			name:      "default strategy maps ops comment event to pull request comment",
			eventType: opscomments.RetestAllCommentEventType.String(),
			status: providerstatus.StatusOpts{
				Status:                  "completed",
				Conclusion:              providerstatus.ConclusionNeutral,
				Summary:                 "Pipelines as Code CI/demo <b>Completed</b>",
				Text:                    "neutral details",
				OriginalPipelineRunName: "demo",
			},
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event, got *statusCommitCommentCalls) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", event.Organization, event.Repository, event.PullRequestNumber), func(_ http.ResponseWriter, r *http.Request) {
					got.created = true
					assert.Equal(t, http.MethodPost, r.Method)
				})
			},
			wantState:   "success",
			wantCreated: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			event := &info.Event{
				Organization:      "owner",
				Repository:        "repository",
				SHA:               "sha",
				EventType:         tt.eventType,
				PullRequestNumber: 666,
			}
			got := &statusCommitCommentCalls{}
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/statuses/%s", event.Organization, event.Repository, event.SHA), func(_ http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NilError(t, err)
				assert.Assert(t, strings.Contains(string(body), fmt.Sprintf(`"state":"%s"`, tt.wantState)), string(body))
			})
			if tt.setup != nil {
				tt.setup(t, mux, event, got)
			}

			log, _ := logger.GetLogger()
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo-cr", Namespace: "ns"},
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						Github: &v1alpha1.GithubSettings{CommentStrategy: tt.commentStrategy},
					},
				},
			}
			v := &Provider{
				ghClient: fakeclient,
				Logger:   log,
				Run:      params.New(),
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue},
				},
				repo:         repo,
				eventEmitter: events.NewEventEmitter(stdata.Kube, log),
				pacUserLogin: "pac-user",
			}

			err := v.createStatusCommit(ctx, event, tt.status)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, tt.wantCreated, got.created)
			assert.Equal(t, tt.wantPatched, got.patched)

			if tt.wantEventReason != "" {
				eventsList, err := stdata.Kube.CoreV1().Events("ns").List(ctx, metav1.ListOptions{})
				assert.NilError(t, err)
				assert.Equal(t, 1, len(eventsList.Items))
				assert.Equal(t, tt.wantEventReason, eventsList.Items[0].Reason)
			}
		})
	}
}

type statusCommitCommentCalls struct {
	created bool
	patched bool
}

func TestCreateStatusCommitMapsConclusion(t *testing.T) {
	tests := []struct {
		name      string
		status    providerstatus.StatusOpts
		wantState string
	}{
		{
			name:      "neutral maps to success",
			status:    providerstatus.StatusOpts{Conclusion: providerstatus.ConclusionNeutral},
			wantState: "success",
		},
		{
			name:      "pending with title remains pending",
			status:    providerstatus.StatusOpts{Conclusion: providerstatus.ConclusionPending, Title: pendingApproval},
			wantState: "pending",
		},
		{
			name:      "in progress maps to pending",
			status:    providerstatus.StatusOpts{Status: "in_progress", Conclusion: providerstatus.ConclusionFailure},
			wantState: "pending",
		},
		{
			name:      "failure remains failure",
			status:    providerstatus.StatusOpts{Conclusion: providerstatus.ConclusionFailure},
			wantState: "failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			event := &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "sha",
				EventType:    triggertype.PullRequest.String(),
			}
			mux.HandleFunc("/repos/owner/repository/statuses/sha", func(_ http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NilError(t, err)
				assert.Assert(t, strings.Contains(string(body), fmt.Sprintf(`"state":"%s"`, tt.wantState)), string(body))
			})
			log, _ := logger.GetLogger()
			v := &Provider{
				ghClient: fakeclient,
				Logger:   log,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue},
				},
			}
			assert.NilError(t, v.createStatusCommit(ctx, event, tt.status))
		})
	}
}

func TestGetFailuresMessageAsAnnotations(t *testing.T) {
	tests := []struct {
		name           string
		regexp         string
		logSnippet     string
		wantPath       string
		wantLine       int
		wantMessage    string
		wantLogSnippet string
	}{
		{
			name:           "invalid regexp",
			regexp:         "[",
			logSnippet:     "./cmd/main.go:42: broken",
			wantLogSnippet: "invalid regexp for filtering failure messages",
		},
		{
			name:           "missing filename group",
			regexp:         `(?P<line>[0-9]+): (?P<error>.*)`,
			logSnippet:     "42: broken",
			wantLogSnippet: "does not contain a filename regexp group",
		},
		{
			name:           "missing line group",
			regexp:         `(?P<filename>[^:]+): (?P<error>.*)`,
			logSnippet:     "cmd/main.go: broken",
			wantLogSnippet: "does not contain a line regexp group",
		},
		{
			name:           "missing error group",
			regexp:         `(?P<filename>[^:]+):(?P<line>[0-9]+)`,
			logSnippet:     "cmd/main.go:42",
			wantLogSnippet: "does not contain a error regexp group",
		},
		{
			name:           "line is not integer",
			regexp:         `(?P<filename>[^:]+):(?P<line>[^:]+): (?P<error>.*)`,
			logSnippet:     "cmd/main.go:not-a-number: broken",
			wantLogSnippet: "cannot convert not-a-number as integer",
		},
		{
			name:        "annotation trims leading dot slash",
			regexp:      `(?P<filename>[^:]+):(?P<line>[0-9]+): (?P<error>.*)`,
			logSnippet:  "./cmd/main.go:42: broken",
			wantPath:    "cmd/main.go",
			wantLine:    42,
			wantMessage: "broken",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			clock := clockwork.NewFakeClock()
			pr := tektontest.MakePRCompletion(clock, "pipeline", "ns", tektonv1.PipelineRunReasonFailed.String(), nil, map[string]string{}, 10)
			pr.Status.ChildReferences = []tektonv1.ChildStatusReference{{
				TypeMeta:         runtime.TypeMeta{Kind: "TaskRun"},
				Name:             "task",
				PipelineTaskName: "task",
			}}
			taskStatus := tektonv1.TaskRunStatusFields{PodName: "task-pod"}
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				TaskRuns: []*tektonv1.TaskRun{
					tektontest.MakeTaskRunCompletion(clock, "task", "ns", "pipeline", map[string]string{}, taskStatus, knativeduckv1.Conditions{{
						Type:    knativeapi.ConditionSucceeded,
						Status:  corev1.ConditionFalse,
						Reason:  "TaskRunValidationFailed",
						Message: tt.logSnippet,
					}}, 10),
				},
			})
			log, observer := logger.GetLogger()
			v := &Provider{
				Logger: log,
				Run: &params.Run{
					Clients: clients.Clients{
						Kube:   stdata.Kube,
						Tekton: stdata.Pipeline,
						Log:    log,
					},
				},
			}

			got := v.getFailuresMessageAsAnnotations(ctx, pr, &info.PacOpts{
				Settings: settings.Settings{
					ErrorDetectionSimpleRegexp:  tt.regexp,
					ErrorDetectionNumberOfLines: 50,
				},
			})

			if tt.wantPath == "" {
				assert.Equal(t, 0, len(got))
				assert.Assert(t, observer.FilterMessageSnippet(tt.wantLogSnippet).Len() > 0, "expected log containing %q", tt.wantLogSnippet)
				return
			}

			assert.Equal(t, 1, len(got))
			assert.Equal(t, tt.wantPath, got[0].GetPath())
			assert.Equal(t, tt.wantLine, got[0].GetStartLine())
			assert.Equal(t, tt.wantLine, got[0].GetEndLine())
			assert.Equal(t, "failure", got[0].GetAnnotationLevel())
			assert.Equal(t, tt.wantMessage, got[0].GetMessage())
		})
	}
}

func TestCanIUseCheckrunIDAllowsOnlyFirstID(t *testing.T) {
	tests := []struct {
		name string
		ids  []int64
		want []bool
	}{
		{
			name: "first check run id wins",
			ids:  []int64{101, 202},
			want: []bool{true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New()
			for i, id := range tt.ids {
				got := v.canIUseCheckrunID(&id)
				assert.Equal(t, tt.want[i], got)
			}
		})
	}
}
