package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	thelp "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab/test"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func setupPipelineIDStatusHandler(t *testing.T, mux *http.ServeMux, path string, responsePID int64) {
	t.Helper()
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		t.Helper()
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		_, hasPID := reqBody["pipeline_id"]
		assert.Assert(t, !hasPID, "request should not have pipeline_id for %s", path)
		rw.WriteHeader(http.StatusCreated)
		fmt.Fprintf(rw, `{"id": 1, "pipeline_id": %d}`, responsePID)
	})
}

func TestCreateStatus(t *testing.T) {
	type fields struct {
		targetProjectID int
	}
	type args struct {
		event      *info.Event
		statusOpts providerstatus.StatusOpts
		postStr    string
	}
	tests := []struct {
		name           string
		fields         fields
		args           args
		wantErr        bool
		wantClient     bool
		setup          func(t *testing.T, mux *http.ServeMux)
		wantCachedPID  int64
		checkCachedPID bool
	}{
		{
			name:    "no client has been set",
			wantErr: true,
		},
		{
			name:       "skip in progress",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Status: "in_progress",
				},
			},
		},
		{
			name:       "skipped conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "skipped",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "has skipped",
			},
		},
		{
			name:       "neutral conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "neutral",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "has stopped",
			},
		},
		{
			name:       "failure conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "failure",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "has failed",
			},
		},
		{
			name:       "success conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "has successfully",
			},
		},
		{
			name:       "pending conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "pending",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "",
			},
		},
		{
			name:       "completed conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "completed",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "has completed",
			},
		},
		{
			name:       "cancelled conclusion",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "cancelled",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "has cancelled",
			},
		},
		{
			name:       "gitops comments completed",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "completed",
				},
				event: &info.Event{
					TriggerTarget: "Note",
				},
				postStr: "has completed",
			},
		},
		{
			name:       "completed with a details url",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "skipped",
					DetailsURL: "https://url.com",
				},
				event: &info.Event{
					TriggerTarget: "pull_request",
				},
				postStr: "https://url.com",
			},
		},
		{
			name:       "pending conclusion for gitops command on pushed commit",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "pending",
				},
				event: &info.Event{
					TriggerTarget: "push",
					EventType:     opscomments.TestAllCommentEventType.String(),
				},
				postStr: "",
			},
		},
		{
			name:       "completed conclusion for gitops command on pushed commit",
			wantClient: true,
			wantErr:    false,
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "completed",
				},
				event: &info.Event{
					TriggerTarget: "push",
					EventType:     opscomments.RetestAllCommentEventType.String(),
				},
				postStr: "has completed",
			},
		},
		{
			name:       "commit status success on source project",
			wantClient: true,
			wantErr:    false,
			fields: fields{
				targetProjectID: 100,
			},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 200,
					TargetProjectID: 100,
					SHA:             "abc123",
				},
				postStr: "has successfully",
			},
		},
		{
			name:       "commit status falls back to target project",
			wantClient: true,
			wantErr:    false,
			fields: fields{
				targetProjectID: 100,
			},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 404, // Will fail to find this project
					TargetProjectID: 100,
					SHA:             "abc123",
				},
				postStr: "has successfully",
			},
		},
		{
			name:       "commit status fails with state transition error skips MR comment",
			wantClient: true,
			wantErr:    false,
			fields: fields{
				targetProjectID: 100,
			},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 404, // Will fail with 404 Not Found
					TargetProjectID: 422, // Will fail with "Cannot transition status" error
					SHA:             "abc123",
				},
				postStr: "", // No MR comment expected for state transition errors
			},
		},
		{
			name:       "generic error on both projects creates MR comment",
			wantClient: true,
			wantErr:    false,
			fields: fields{
				targetProjectID: 100,
			},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 400, // Will fail with generic 400 error
					TargetProjectID: 405, // Will fail with generic 400 error
					SHA:             "abc123def",
				},
				postStr: "has successfully", // MR comment expected for non-transition errors
			},
		},
		{
			name:       "permission error 403 on source creates MR comment",
			wantClient: true,
			wantErr:    false,
			fields: fields{
				targetProjectID: 100,
			},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 403, // Will fail with 403 Forbidden
					TargetProjectID: 400, // Will fail with generic 400 error
					SHA:             "abc123ghi",
				},
				postStr: "has successfully",
			},
		},
		{
			name:       "permission error 401 on source creates MR comment",
			wantClient: true,
			wantErr:    false,
			fields: fields{
				targetProjectID: 100,
			},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion: "success",
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 401, // Will fail with 401 Unauthorized
					TargetProjectID: 400, // Will fail with generic 400 error
					SHA:             "abc123jkl",
				},
				postStr: "has successfully",
			},
		},
		{
			name:           "pipeline ID first call discovers and caches",
			wantClient:     true,
			checkCachedPID: true,
			wantCachedPID:  9999,
			fields:         fields{targetProjectID: 500},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion:              "success",
					OriginalPipelineRunName: "pr-1",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-pr",
							Namespace:   "default",
							Annotations: map[string]string{},
						},
					},
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 500,
					TargetProjectID: 500,
					SHA:             "deadbeef",
				},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				setupPipelineIDStatusHandler(t, mux, "/projects/500/statuses/deadbeef", 9999)
			},
		},
		{
			name:       "pipeline ID read from annotation",
			wantClient: true,
			fields:     fields{targetProjectID: 500},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion:              "success",
					OriginalPipelineRunName: "pr-1",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-pr",
							Namespace:   "default",
							Annotations: map[string]string{keys.GitLabPipelineID: "9999"},
						},
					},
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 500,
					TargetProjectID: 500,
					SHA:             "deadbeef01",
				},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/projects/500/statuses/deadbeef01", func(rw http.ResponseWriter, r *http.Request) {
					var reqBody map[string]any
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Fatalf("failed to decode request body: %v", err)
					}
					pid, ok := reqBody["pipeline_id"]
					assert.Assert(t, ok, "request must include pipeline_id")
					pidFloat, ok := pid.(float64)
					assert.Assert(t, ok, "pipeline_id must be a number")
					assert.Equal(t, int64(pidFloat), int64(9999))
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{"id": 1, "pipeline_id": 9999}`)
				})
			},
		},
		{
			name:           "pipeline ID zero in response does not cache",
			wantClient:     true,
			checkCachedPID: true,
			wantCachedPID:  0,
			fields:         fields{targetProjectID: 501},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion:              "success",
					OriginalPipelineRunName: "pr-zero-pid",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-pr-zero",
							Namespace:   "default",
							Annotations: map[string]string{},
						},
					},
				},
				event: &info.Event{
					TriggerTarget:   "push",
					SourceProjectID: 501,
					TargetProjectID: 501,
					SHA:             "deadbeef02",
				},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				setupPipelineIDStatusHandler(t, mux, "/projects/501/statuses/deadbeef02", 0)
			},
		},
		{
			name:       "pipeline ID target fallback reads annotation",
			wantClient: true,
			fields:     fields{targetProjectID: 600},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion:              "success",
					OriginalPipelineRunName: "pr-1",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-pr",
							Namespace:   "default",
							Annotations: map[string]string{keys.GitLabPipelineID: "1234"},
						},
					},
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 404,
					TargetProjectID: 600,
					SHA:             "aabb1122",
				},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/projects/404/statuses/aabb1122", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusNotFound)
					fmt.Fprint(rw, `{"message": "404 Not Found"}`)
				})
				mux.HandleFunc("/projects/600/statuses/aabb1122", func(rw http.ResponseWriter, r *http.Request) {
					var reqBody map[string]any
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Fatalf("failed to decode request body: %v", err)
					}
					pid, ok := reqBody["pipeline_id"]
					assert.Assert(t, ok, "target request must include pipeline_id")
					pidFloat, ok := pid.(float64)
					assert.Assert(t, ok, "pipeline_id must be a number")
					assert.Equal(t, int64(pidFloat), int64(1234))
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{"id": 1, "pipeline_id": 1234}`)
				})
			},
		},
		{
			name:       "pipeline ID target fallback without annotation",
			wantClient: true,
			fields:     fields{targetProjectID: 600},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion:              "success",
					OriginalPipelineRunName: "pr-1",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-pr",
							Namespace:   "default",
							Annotations: map[string]string{},
						},
					},
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 404,
					TargetProjectID: 600,
					SHA:             "aabb1123",
				},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/projects/404/statuses/aabb1123", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusNotFound)
					fmt.Fprint(rw, `{"message": "404 Not Found"}`)
				})
				mux.HandleFunc("/projects/600/statuses/aabb1123", func(rw http.ResponseWriter, r *http.Request) {
					var reqBody map[string]any
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Fatalf("failed to decode request body: %v", err)
					}
					_, hasPID := reqBody["pipeline_id"]
					assert.Assert(t, !hasPID, "target request should not have pipeline_id")
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{"id": 1, "pipeline_id": 7777}`)
				})
			},
		},
		{
			name:       "pipeline ID same project fallback preserves annotation",
			wantClient: true,
			fields:     fields{targetProjectID: 600},
			args: args{
				statusOpts: providerstatus.StatusOpts{
					Conclusion:              "success",
					OriginalPipelineRunName: "pr-1",
					PipelineRun: &tektonv1.PipelineRun{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-pr",
							Namespace:   "default",
							Annotations: map[string]string{keys.GitLabPipelineID: "5555"},
						},
					},
				},
				event: &info.Event{
					TriggerTarget:   "pull_request",
					SourceProjectID: 600,
					TargetProjectID: 600,
					SHA:             "aabb1124",
				},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				var callCount int
				mux.HandleFunc("/projects/600/statuses/aabb1124", func(rw http.ResponseWriter, r *http.Request) {
					callCount++
					if callCount == 1 {
						rw.WriteHeader(http.StatusForbidden)
						fmt.Fprint(rw, `{"message": "403 Forbidden"}`)
						return
					}
					var reqBody map[string]any
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Fatalf("failed to decode request body: %v", err)
					}
					pid, ok := reqBody["pipeline_id"]
					assert.Assert(t, ok, "target request must include pipeline_id")
					pidFloat, ok := pid.(float64)
					assert.Assert(t, ok, "pipeline_id must be a number")
					assert.Equal(t, int64(pidFloat), int64(5555))
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{"id": 1, "pipeline_id": 5555}`)
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			logger, _ := logger.GetLogger()
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			run := &params.Run{
				Clients: clients.Clients{
					Kube:   stdata.Kube,
					Tekton: stdata.Pipeline,
					Log:    logger,
				},
			}
			v := &Provider{
				targetProjectID: int64(tt.fields.targetProjectID),
				run:             run,
				Logger:          logger,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: settings.PACApplicationNameDefaultValue,
					},
				},
				eventEmitter: events.NewEventEmitter(run.Clients.Kube, logger),
			}
			if tt.args.event == nil {
				tt.args.event = info.NewEvent()
			}
			tt.args.event.PullRequestNumber = 666

			if tt.wantClient {
				client, mux, tearDown := thelp.Setup(t)
				v.SetGitLabClient(client)
				defer tearDown()

				if tt.setup != nil {
					tt.setup(t, mux)
				} else {
					// Mock commit status endpoints for both source and target projects
					if tt.args.event.SourceProjectID != 0 {
						sourceStatusPath := fmt.Sprintf("/projects/%d/statuses/%s", tt.args.event.SourceProjectID, tt.args.event.SHA)
						mux.HandleFunc(sourceStatusPath, func(rw http.ResponseWriter, _ *http.Request) {
							switch tt.args.event.SourceProjectID {
							case 400:
								rw.WriteHeader(http.StatusBadRequest)
								fmt.Fprint(rw, `{"message": "400 Bad Request"}`)
							case 401:
								rw.WriteHeader(http.StatusUnauthorized)
								fmt.Fprint(rw, `{"message": "401 Unauthorized"}`)
							case 403:
								rw.WriteHeader(http.StatusForbidden)
								fmt.Fprint(rw, `{"message": "403 Forbidden"}`)
							case 404:
								rw.WriteHeader(http.StatusNotFound)
								fmt.Fprint(rw, `{"message": "404 Project Not Found"}`)
							case 422:
								rw.WriteHeader(http.StatusBadRequest)
								fmt.Fprint(rw, `{"message": "Cannot transition status via :run from :running"}`)
							default:
								rw.WriteHeader(http.StatusCreated)
								fmt.Fprint(rw, `{}`)
							}
						})
					}

					if tt.args.event.TargetProjectID != 0 {
						targetStatusPath := fmt.Sprintf("/projects/%d/statuses/%s", tt.args.event.TargetProjectID, tt.args.event.SHA)
						mux.HandleFunc(targetStatusPath, func(rw http.ResponseWriter, _ *http.Request) {
							switch tt.args.event.TargetProjectID {
							case 400, 405:
								rw.WriteHeader(http.StatusBadRequest)
								fmt.Fprint(rw, `{"message": "400 Bad Request"}`)
							case 401:
								rw.WriteHeader(http.StatusUnauthorized)
								fmt.Fprint(rw, `{"message": "401 Unauthorized"}`)
							case 403:
								rw.WriteHeader(http.StatusForbidden)
								fmt.Fprint(rw, `{"message": "403 Forbidden"}`)
							case 404:
								rw.WriteHeader(http.StatusNotFound)
								fmt.Fprint(rw, `{"message": "404 Project Not Found"}`)
							case 422:
								rw.WriteHeader(http.StatusBadRequest)
								fmt.Fprint(rw, `{"message": "Cannot transition status via :run from :running"}`)
							default:
								rw.WriteHeader(http.StatusCreated)
								fmt.Fprint(rw, `{}`)
							}
						})
					}
				}

				thelp.MuxNotePost(t, mux, int(v.targetProjectID), tt.args.event.PullRequestNumber, tt.args.postStr)
			}

			if err := v.CreateStatus(ctx, tt.args.event, tt.args.statusOpts); (err != nil) != tt.wantErr {
				t.Errorf("CreateStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.checkCachedPID {
				assert.Equal(t, v.pipelineID, tt.wantCachedPID)
			}
		})
	}
}

func TestCreateStatusPipelineIDSharedAcrossPipelineRuns(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	log, _ := logger.GetLogger()
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{
			Kube:   stdata.Kube,
			Tekton: stdata.Pipeline,
			Log:    log,
		},
	}

	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	v := &Provider{
		run:    run,
		Logger: log,
		pacInfo: &info.PacOpts{
			Settings: settings.Settings{
				ApplicationName: settings.PACApplicationNameDefaultValue,
			},
		},
		eventEmitter: events.NewEventEmitter(run.Clients.Kube, log),
	}
	v.SetGitLabClient(client)

	sha := "abc123"
	projectID := int64(500)

	mux.HandleFunc(fmt.Sprintf("/projects/%d/statuses/%s", projectID, sha),
		func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(http.StatusCreated)
			fmt.Fprint(rw, `{"id": 1, "pipeline_id": 9999}`)
		})

	event := &info.Event{
		TriggerTarget:     "pull_request",
		SourceProjectID:   projectID,
		TargetProjectID:   projectID,
		SHA:               sha,
		PullRequestNumber: 1,
	}

	// First PipelineRun — discovers the pipeline ID
	err := v.CreateStatus(ctx, event, providerstatus.StatusOpts{
		Conclusion:              "success",
		OriginalPipelineRunName: "pr-lint",
		PipelineRun: &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-pr-lint",
				Namespace:   "default",
				Annotations: map[string]string{},
			},
		},
	})
	assert.NilError(t, err)
	assert.Equal(t, v.pipelineID, int64(9999))

	// Second PipelineRun — should pick up the pipeline ID from in-memory field
	var secondCallPID int64
	secondClient, secondMuxPtr, secondTearDown := thelp.Setup(t)
	defer secondTearDown()
	v.SetGitLabClient(secondClient)

	secondMuxPtr.HandleFunc(fmt.Sprintf("/projects/%d/statuses/%s", projectID, sha),
		func(rw http.ResponseWriter, r *http.Request) {
			var reqBody map[string]any
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			pid, ok := reqBody["pipeline_id"]
			assert.Assert(t, ok, "second call must include pipeline_id from cached v.pipelineID")
			pidFloat, ok := pid.(float64)
			assert.Assert(t, ok, "pipeline_id must be a number")
			secondCallPID = int64(pidFloat)
			rw.WriteHeader(http.StatusCreated)
			fmt.Fprint(rw, `{"id": 2, "pipeline_id": 9999}`)
		})
	err = v.CreateStatus(ctx, event, providerstatus.StatusOpts{
		Conclusion:              "success",
		OriginalPipelineRunName: "pr-test",
		PipelineRun: &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-pr-test",
				Namespace:   "default",
				Annotations: map[string]string{},
			},
		},
	})
	assert.NilError(t, err)
	assert.Equal(t, secondCallPID, int64(9999))
}

func TestGetCommitInfo(t *testing.T) {
	tests := []struct {
		name                string
		event               *info.Event
		sourceProjectID     int
		mockCommitResponse  string
		wantErr             bool
		wantSHATitle        string
		wantSHAURL          string
		wantSHAMessage      string
		wantAuthorName      string
		wantAuthorEmail     string
		wantAuthorDate      string
		wantCommitterName   string
		wantCommitterEmail  string
		wantCommitterDate   string
		checkExtendedFields bool
		noClient            bool
	}{
		{
			name: "good with full commit info",
			event: &info.Event{
				HeadBranch: "feature-branch",
			},
			sourceProjectID: 123,
			mockCommitResponse: `{
				"id": "abc123",
				"title": "feat: add new feature",
				"message": "feat: add new feature\n\nThis is the full commit message with details.",
				"web_url": "https://gitlab.com/owner/repo/-/commit/abc123",
				"author_name": "John Doe",
				"author_email": "john@example.com",
				"authored_date": "2024-01-15T10:30:00Z",
				"committer_name": "GitLab",
				"committer_email": "noreply@gitlab.com",
				"committed_date": "2024-01-15T10:31:00Z"
			}`,
			wantSHATitle:        "feat: add new feature",
			wantSHAURL:          "https://gitlab.com/owner/repo/-/commit/abc123",
			wantSHAMessage:      "feat: add new feature\n\nThis is the full commit message with details.",
			wantAuthorName:      "John Doe",
			wantAuthorEmail:     "john@example.com",
			wantAuthorDate:      "2024-01-15T10:30:00Z",
			wantCommitterName:   "GitLab",
			wantCommitterEmail:  "noreply@gitlab.com",
			wantCommitterDate:   "2024-01-15T10:31:00Z",
			checkExtendedFields: true,
		},
		{
			name: "basic fields only",
			event: &info.Event{
				HeadBranch: "main",
			},
			sourceProjectID: 123,
			mockCommitResponse: `{
				"id": "def456",
				"title": "fix: simple fix",
				"message": "fix: simple fix",
				"web_url": "https://gitlab.com/owner/repo/-/commit/def456"
			}`,
			wantSHATitle:   "fix: simple fix",
			wantSHAURL:     "https://gitlab.com/owner/repo/-/commit/def456",
			wantSHAMessage: "fix: simple fix",
		},
		{
			name: "no client error",
			event: &info.Event{
				HeadBranch: "main",
			},
			noClient: true,
			wantErr:  true,
		},
		{
			name: "no SHA, no HeadBranch - no API call",
			event: &info.Event{
				SHA: "already-set",
			},
			sourceProjectID: 123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			var provider *Provider
			if !tt.noClient {
				client, mux, tearDown := thelp.Setup(t)
				defer tearDown()

				// Mock the GetCommit API endpoint if we expect it to be called
				if tt.event.SHA == "" && tt.event.HeadBranch != "" {
					mux.HandleFunc(fmt.Sprintf("/projects/%d/repository/commits/%s", tt.sourceProjectID, tt.event.HeadBranch),
						func(rw http.ResponseWriter, _ *http.Request) {
							fmt.Fprint(rw, tt.mockCommitResponse)
						})
				}

				provider = &Provider{
					gitlabClient:    client,
					sourceProjectID: int64(tt.sourceProjectID),
				}
			} else {
				provider = &Provider{}
			}

			err := provider.GetCommitInfo(ctx, tt.event)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				return
			}

			assert.NilError(t, err)

			// Only check fields if API was supposed to be called
			if tt.event.SHA == "" && tt.event.HeadBranch != "" {
				assert.Equal(t, tt.wantSHATitle, tt.event.SHATitle, "SHATitle should match")
				assert.Equal(t, tt.wantSHAURL, tt.event.SHAURL, "SHAURL should match")
				assert.Equal(t, tt.wantSHAMessage, tt.event.SHAMessage, "SHAMessage should match")

				if tt.checkExtendedFields {
					assert.Equal(t, tt.wantAuthorName, tt.event.SHAAuthorName, "SHAAuthorName should match")
					assert.Equal(t, tt.wantAuthorEmail, tt.event.SHAAuthorEmail, "SHAAuthorEmail should match")
					assert.Equal(t, tt.wantCommitterName, tt.event.SHACommitterName, "SHACommitterName should match")
					assert.Equal(t, tt.wantCommitterEmail, tt.event.SHACommitterEmail, "SHACommitterEmail should match")

					// Verify dates are parsed correctly
					if tt.wantAuthorDate != "" {
						expectedAuthorDate, _ := time.Parse(time.RFC3339, tt.wantAuthorDate)
						assert.DeepEqual(t, expectedAuthorDate, tt.event.SHAAuthorDate)
					}
					if tt.wantCommitterDate != "" {
						expectedCommitterDate, _ := time.Parse(time.RFC3339, tt.wantCommitterDate)
						assert.DeepEqual(t, expectedCommitterDate, tt.event.SHACommitterDate)
					}
				}
			}
		})
	}
}

func TestGetCommitStatuses(t *testing.T) {
	tests := []struct {
		name         string
		event        *info.Event
		provider     *Provider
		mockHandlers map[string]func(http.ResponseWriter, *http.Request)
		want         []provider.CommitStatusInfo
	}{
		{
			name: "uses event source project statuses",
			event: &info.Event{
				SHA:             "abc123",
				SourceProjectID: 101,
				TargetProjectID: 202,
			},
			provider: &Provider{},
			mockHandlers: map[string]func(http.ResponseWriter, *http.Request){
				"/projects/101/repository/commits/abc123/statuses": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"name":"Pipelines as Code CI / always-good-pipelinerun","status":"success"},{"name":"Pipelines as Code CI / pipelinerun-exit-1","status":"failed"}]`)
				},
			},
			want: []provider.CommitStatusInfo{
				{Name: "Pipelines as Code CI / always-good-pipelinerun", Status: "success"},
				{Name: "Pipelines as Code CI / pipelinerun-exit-1", Status: "failed"},
			},
		},
		{
			name: "falls back to provider source project id when event source project id is empty",
			event: &info.Event{
				SHA:             "def456",
				TargetProjectID: 202,
			},
			provider: &Provider{
				sourceProjectID: 303,
			},
			mockHandlers: map[string]func(http.ResponseWriter, *http.Request){
				"/projects/303/repository/commits/def456/statuses": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"name":"Pipelines as Code CI / from-provider-source","status":"success"}]`)
				},
			},
			want: []provider.CommitStatusInfo{
				{Name: "Pipelines as Code CI / from-provider-source", Status: "success"},
			},
		},
		{
			name: "falls back to target project when source project lookup fails",
			event: &info.Event{
				SHA:             "fedcba",
				SourceProjectID: 404,
				TargetProjectID: 505,
			},
			provider: &Provider{},
			mockHandlers: map[string]func(http.ResponseWriter, *http.Request){
				"/projects/404/repository/commits/fedcba/statuses": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					rw.WriteHeader(http.StatusNotFound)
					fmt.Fprint(rw, `{"message":"404 Project Not Found"}`)
				},
				"/projects/505/repository/commits/fedcba/statuses": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"name":"Pipelines as Code CI / from-target","status":"success"}]`)
				},
			},
			want: []provider.CommitStatusInfo{
				{Name: "Pipelines as Code CI / from-target", Status: "success"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, tearDown := thelp.Setup(t)
			defer tearDown()
			logger, _ := logger.GetLogger()

			for endpoint, handler := range tt.mockHandlers {
				mux.HandleFunc(endpoint, handler)
			}

			tt.provider.gitlabClient = fakeclient
			tt.provider.Logger = logger

			got, err := tt.provider.GetCommitStatuses(context.Background(), tt.event)
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestGetConfig(t *testing.T) {
	v := &Provider{}
	assert.Assert(t, v.GetConfig().APIURL != "")
	assert.Assert(t, v.GetConfig().TaskStatusTMPL != "")
}

func TestSetClient(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	core, observer := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(core).Sugar()

	run := &params.Run{
		Clients: clients.Clients{
			Log: fakelogger,
		},
	}

	v := &Provider{}
	assert.Assert(t, v.SetClient(ctx, run, info.NewEvent(), nil, nil) != nil)

	client, _, tearDown := thelp.Setup(t)
	defer tearDown()

	vv := &Provider{gitlabClient: client}
	err := vv.SetClient(ctx, run, &info.Event{
		Provider: &info.Provider{
			Token: "hello",
		},
		Organization:    "my-org",
		Repository:      "my-repo",
		SourceProjectID: 1234,
		TargetProjectID: 1234,
	}, nil, nil)

	assert.NilError(t, err)
	assert.Assert(t, *vv.Token != "")

	logs := observer.TakeAll()
	assert.Assert(t, len(logs) > 0, "expected a log entry, got none")

	expected := fmt.Sprintf(
		"gitlab: initialized for client with token for apiURL=%s, org=%s, repo=%s",
		vv.apiURL, "my-org", "my-repo")

	assert.Equal(t, expected, logs[0].Message)
}

func TestSetClientFieldsInitializedOnError(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{
			Kube: stdata.Kube,
			Log:  fakelogger,
		},
	}

	tests := []struct {
		name              string
		triggerTarget     triggertype.Trigger
		sourceProjectID   int
		setupMockResponse func(*http.ServeMux, int)
		expectedError     string
		providerToken     string
	}{
		{
			name:            "Fields initialized even when project access fails",
			triggerTarget:   triggertype.PullRequest,
			sourceProjectID: 456,
			providerToken:   "test-token",
			setupMockResponse: func(mux *http.ServeMux, projectID int) {
				path := fmt.Sprintf("/projects/%d", projectID)
				mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						rw.WriteHeader(http.StatusNotFound)
						fmt.Fprint(rw, `{"message": "404 Project Not Found"}`)
					}
				})
			},
			expectedError: "failed to access GitLab source repository ID 456",
		},
		{
			name:            "Fields initialized when invalid URL causes error",
			triggerTarget:   triggertype.Push,
			sourceProjectID: 123,
			providerToken:   "test-token",
			setupMockResponse: func(_ *http.ServeMux, _ int) {
				// No mock needed
			},
			expectedError: "", // This test will use an invalid URL which gets caught during parsing
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient, mux, tearDown := thelp.Setup(t)
			defer tearDown()

			if tt.setupMockResponse != nil {
				tt.setupMockResponse(mux, tt.sourceProjectID)
			}

			v := &Provider{gitlabClient: mockClient}
			repo := &v1alpha1.Repository{}
			repo.SetName("test-repo")
			eventsEmitter := events.NewEventEmitter(run.Clients.Kube, fakelogger)

			event := &info.Event{
				Provider: &info.Provider{
					Token: tt.providerToken,
				},
				Organization:    "test-org",
				Repository:      "test-repo",
				TriggerTarget:   tt.triggerTarget,
				SourceProjectID: int64(tt.sourceProjectID),
				TargetProjectID: 123,
				EventType:       "pull_request",
			}

			err := v.SetClient(ctx, run, event, repo, eventsEmitter)

			// The test should verify that even when an error occurs,
			// the critical fields are initialized to prevent nil pointer crashes
			if tt.expectedError != "" {
				assert.Assert(t, err != nil, "expected error but got none")
				assert.ErrorContains(t, err, tt.expectedError)
			}

			// This is the key assertion: verify fields are initialized even on error
			assert.Assert(t, v.run != nil, "v.run should be initialized even on error")
			assert.Assert(t, v.eventEmitter != nil, "v.eventEmitter should be initialized even on error")
			assert.Assert(t, v.repo != nil, "v.repo should be initialized even on error")
			assert.Assert(t, v.triggerEvent != "", "v.triggerEvent should be initialized even on error")
		})
	}
}

func TestSetClientRepositoryAccessCheck(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	run := &params.Run{
		Clients: clients.Clients{
			Log: fakelogger,
		},
	}

	tests := []struct {
		name              string
		triggerTarget     triggertype.Trigger
		sourceProjectID   int
		setupMockResponse func(*http.ServeMux, int)
		expectedError     string
	}{
		{
			name:            "Non-pull request trigger should skip access check",
			triggerTarget:   triggertype.Push,
			sourceProjectID: 123,
			setupMockResponse: func(_ *http.ServeMux, _ int) {
				// No mock needed - should not make the call
			},
			expectedError: "",
		},
		{
			name:            "Pull request with successful access",
			triggerTarget:   triggertype.PullRequest,
			sourceProjectID: 123,
			setupMockResponse: func(mux *http.ServeMux, projectID int) {
				path := fmt.Sprintf("/projects/%d", projectID)
				mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						rw.WriteHeader(http.StatusOK)
						fmt.Fprint(rw, `{"id": 123, "name": "test-repo"}`)
					}
				})
			},
			expectedError: "",
		},
		{
			name:            "Pull request with not found should return specific error",
			triggerTarget:   triggertype.PullRequest,
			sourceProjectID: 456,
			setupMockResponse: func(mux *http.ServeMux, projectID int) {
				path := fmt.Sprintf("/projects/%d", projectID)
				mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						// Return 404 without error to test the status code check
						rw.WriteHeader(http.StatusNotFound)
						fmt.Fprint(rw, `{"message": "404 Project Not Found"}`)
					}
				})
			},
			expectedError: "failed to access GitLab source repository ID 456: please ensure token has 'read_repository' scope on that repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient, mux, tearDown := thelp.Setup(t)
			defer tearDown()

			// Setup the mock for the repository access check API call
			if tt.setupMockResponse != nil {
				tt.setupMockResponse(mux, tt.sourceProjectID)
			}

			v := &Provider{gitlabClient: mockClient}
			event := &info.Event{
				Provider: &info.Provider{
					Token: "test-token",
				},
				Organization:    "test-org",
				Repository:      "test-repo",
				TriggerTarget:   tt.triggerTarget,
				SourceProjectID: int64(tt.sourceProjectID),
				TargetProjectID: 123,
			}

			err := v.SetClient(ctx, run, event, nil, nil)

			if tt.expectedError != "" {
				assert.Assert(t, err != nil, "expected error but got none")
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err, "unexpected error: %v", err)
			}
		})
	}
}

func TestSetClientDetectAPIURL(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	run := &params.Run{
		Clients: clients.Clients{
			Log: fakelogger,
		},
	}
	mockClient, _, tearDown := thelp.Setup(t)
	defer tearDown()

	// Define test cases
	tests := []struct {
		name              string
		providerToken     string
		providerURL       string // input: event.Provider.URL
		repoURL           string // input: v.repoURL
		pathWithNamespace string // input: v.pathWithNamespace (needed if repoURL is used)
		eventURL          string // input: event.URL
		// Define expected outcomes
		expectedAPIURL string
		expectedError  string // Substring expected in the error message, "" for no error
	}{
		{
			name:          "Error: No token provided",
			providerToken: "",
			expectedError: "no git_provider.secret has been set",
		},
		{
			name:              "Success: API URL from event.Provider.URL (highest precedence)",
			providerToken:     "token",
			providerURL:       "https://provider.example.com",
			repoURL:           "https://repo.example.com/foo/bar", // Should be ignored
			pathWithNamespace: "foo/bar",
			eventURL:          "https://event.example.com/foo/bar", // Should be ignored
			expectedAPIURL:    "https://provider.example.com",
			expectedError:     "",
		},
		{
			name:              "Success: API URL from v.repoURL (non-public)",
			providerToken:     "token",
			providerURL:       "", // This must be empty to test the next case
			repoURL:           "https://private-gitlab.com/my/repo",
			pathWithNamespace: "my/repo",
			eventURL:          "https://event.example.com/my/repo", // Should be ignored
			expectedAPIURL:    "https://private-gitlab.com/",
			expectedError:     "",
		},
		{
			name:           "Success: API URL from event.URL",
			providerToken:  "token",
			providerURL:    "", // This must be empty
			repoURL:        "", // This must be empty
			eventURL:       "https://event-url.com/org/project",
			expectedAPIURL: "https://event-url.com",
			expectedError:  "",
		},
		{
			name:           "Success: Fallback to default public API URL",
			providerToken:  "token",
			providerURL:    "",
			repoURL:        "",
			eventURL:       "",
			expectedAPIURL: apiPublicURL, // Default case
			expectedError:  "",
		},
		{
			name:              "Success: Default URL when repoURL is public GitLab",
			providerToken:     "token",
			providerURL:       "",
			repoURL:           apiPublicURL + "/public/repo", // Starts with public URL, so skipped
			pathWithNamespace: "public/repo",
			eventURL:          "", // Falls through to default
			expectedAPIURL:    apiPublicURL,
			expectedError:     "",
		},
		{
			name:          "Error: Invalid URL from event.URL",
			providerToken: "token",
			providerURL:   "",
			repoURL:       "",
			eventURL:      "://bad-schema",
			expectedError: "parse \"://bad-schema\": missing protocol scheme", // Specific error from url.Parse
		},
		{
			name:          "Error: Invalid URL from event.Provider.URL (final parse)",
			providerToken: "token",
			providerURL:   "ht tp://invalid host", // Invalid URL format
			repoURL:       "",
			eventURL:      "",
			expectedError: "failed to parse api url", // Wrapper error message
		},
		{
			name:              "Error: Invalid URL from v.repoURL (final parse)",
			providerToken:     "token",
			providerURL:       "",
			repoURL:           "ht tp://invalid.repo.url/foo/bar", // Invalid format
			pathWithNamespace: "foo/bar",
			eventURL:          "",
			// Note: The calculated apiURL would be "ht tp://invalid.repo.url" before parsing
			expectedError: "failed to parse api url",
		},
	}

	// Run test cases
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup specific to this test case
			v := &Provider{
				gitlabClient:      mockClient, // Use the shared mock client
				repoURL:           tc.repoURL,
				pathWithNamespace: tc.pathWithNamespace,
			}
			event := info.NewEvent()
			event.Provider.Token = tc.providerToken
			event.Provider.URL = tc.providerURL
			event.URL = tc.eventURL
			// Set some default IDs to avoid potential nil pointer issues or side effects
			// if the GetProject part of SetClient is reached unexpectedly.
			event.TargetProjectID = 1
			event.SourceProjectID = 1

			// Execute the function under test
			// Using placeholder nil values for arguments not directly related to URL detection
			err := v.SetClient(ctx, run, event, nil, nil)

			// Assertions
			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
				// If an error is expected, we usually don't check the apiURL state,
				// as it might be indeterminate or irrelevant.
			} else {
				assert.NilError(t, err)
				// Only check the resulting apiURL if no error was expected
				assert.Equal(t, tc.expectedAPIURL, v.apiURL)
				// Optionally, check if the client was actually set (if no error)
				assert.Assert(t, v.Client() != nil)
				assert.Assert(t, v.Token != nil && *v.Token == tc.providerToken)
			}
		})
	}
}

func TestGetTektonDir(t *testing.T) {
	samplePR, err := os.ReadFile("../../resolve/testdata/pipeline-finally.yaml")
	assert.NilError(t, err)
	type fields struct {
		targetProjectID int
		sourceProjectID int
		userID          int
	}
	type args struct {
		event      *info.Event
		path       string
		provenance string
	}
	tests := []struct {
		name                 string
		fields               fields
		args                 args
		wantStr              string
		wantErr              string
		wantTreeAPIErr       bool
		wantFilesAPIErr      bool
		wantClient           bool
		prcontent            string
		filterMessageSnippet string
	}{
		{
			name:    "no client set",
			wantErr: noClientErrStr,
		},
		{
			name:       "not found, no err",
			wantClient: true,
			args:       args{event: &info.Event{}},
		},
		{
			name:       "bad yaml",
			wantClient: true,
			args: args{
				event: &info.Event{SHA: "abcd", HeadBranch: "main"},
				path:  ".tekton",
			},
			fields: fields{
				sourceProjectID: 10,
			},
			prcontent: "bad:\n- yaml\nfoo",
			wantErr:   "error unmarshalling yaml file pr.yaml: yaml: line 4: could not find expected ':'",
		},
		{
			name:      "list tekton dir on pull request",
			prcontent: string(samplePR),
			args: args{
				path: ".tekton",
				event: &info.Event{
					HeadBranch:    "main",
					TriggerTarget: triggertype.PullRequest,
				},
			},
			fields: fields{
				sourceProjectID: 100,
			},
			wantClient:           true,
			wantStr:              "kind: PipelineRun",
			filterMessageSnippet: `Using PipelineRun definition from source merge request on commit SHA`,
		},
		{
			name:      "list tekton dir on push",
			prcontent: string(samplePR),
			args: args{
				path: ".tekton",
				event: &info.Event{
					HeadBranch:    "main",
					TriggerTarget: triggertype.Push,
				},
			},
			fields: fields{
				sourceProjectID: 100,
			},
			wantClient:           true,
			wantStr:              "kind: PipelineRun",
			filterMessageSnippet: `Using PipelineRun definition from source push on commit SHA`,
		},
		{
			name:      "list tekton dir on default_branch",
			prcontent: string(samplePR),
			args: args{
				provenance: "default_branch",
				path:       ".tekton",
				event: &info.Event{
					DefaultBranch: "main",
				},
			},
			fields: fields{
				sourceProjectID: 100,
			},
			wantClient: true,
			wantStr:    "kind: PipelineRun",
		},
		{
			name:      "list tekton dir no --- prefix",
			prcontent: strings.TrimPrefix(string(samplePR), "---"),
			args: args{
				path: ".tekton",
				event: &info.Event{
					HeadBranch: "main",
				},
			},
			fields: fields{
				sourceProjectID: 100,
			},
			wantClient: true,
			wantStr:    "kind: PipelineRun",
		},
		{
			name:      "list tekton dir tree api call error",
			prcontent: strings.TrimPrefix(string(samplePR), "---"),
			args: args{
				path: ".tekton",
				event: &info.Event{
					HeadBranch: "main",
				},
			},
			fields: fields{
				sourceProjectID: 100,
			},
			wantClient:     true,
			wantTreeAPIErr: true,
			wantErr:        "failed to list .tekton dir",
		},
		{
			name:      "get file raw api call error",
			prcontent: strings.TrimPrefix(string(samplePR), "---"),
			args: args{
				path: ".tekton",
				event: &info.Event{
					HeadBranch: "main",
				},
			},
			fields: fields{
				sourceProjectID: 100,
			},
			wantClient:      true,
			wantFilesAPIErr: true,
			wantErr:         "failed to get filename from api pr.yaml dir",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			observer, exporter := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			v := &Provider{
				targetProjectID: int64(tt.fields.targetProjectID),
				sourceProjectID: int64(tt.fields.sourceProjectID),
				userID:          int64(tt.fields.userID),
				Logger:          fakelogger,
			}
			if tt.wantClient {
				client, mux, tearDown := thelp.Setup(t)
				v.SetGitLabClient(client)
				muxbranch := tt.args.event.HeadBranch
				if tt.args.provenance == "default_branch" {
					muxbranch = tt.args.event.DefaultBranch
				}
				if tt.args.path != "" && tt.prcontent != "" {
					thelp.MuxListTektonDir(t, mux, tt.fields.sourceProjectID, muxbranch, tt.prcontent, tt.wantTreeAPIErr, tt.wantFilesAPIErr)
				}
				defer tearDown()
			}

			got, err := v.GetTektonDir(ctx, tt.args.event, tt.args.path, tt.args.provenance)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error %s, got %v", tt.wantErr, err)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			if tt.wantStr != "" {
				assert.Assert(t, strings.Contains(got, tt.wantStr), "%s is not in %s", tt.wantStr, got)
			}
			if tt.filterMessageSnippet != "" {
				gotcha := exporter.FilterMessageSnippet(tt.filterMessageSnippet)
				assert.Assert(t, gotcha.Len() > 0, "expected to find %s in logs, found %v", tt.filterMessageSnippet, exporter.All())
			}
		})
	}
}

func TestGetFileInsideRepo(t *testing.T) {
	content := "hello moto"
	ctx, _ := rtesting.SetupFakeContext(t)
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	event := &info.Event{
		HeadBranch: "branch",
	}
	v := Provider{
		sourceProjectID: 10,
		gitlabClient:    client,
	}
	thelp.MuxListTektonDir(t, mux, int(v.sourceProjectID), event.HeadBranch, content, false, false)
	got, err := v.GetFileInsideRepo(ctx, event, "pr.yaml", "")
	assert.NilError(t, err)
	assert.Equal(t, content, got)

	_, err = v.GetFileInsideRepo(ctx, event, "notfound", "")
	assert.Assert(t, err != nil)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		wantErr     string
		secretToken string
		eventToken  string
	}{
		{
			name:        "valid event with matching tokens",
			wantErr:     "",
			secretToken: "test",
			eventToken:  "test",
		},
		{
			name:        "invalid when webhook secret not configured",
			wantErr:     "no webhook secret configured",
			secretToken: "",
			eventToken:  "test",
		},
		{
			name:        "invalid when tokens do not match",
			wantErr:     "token does not match configured secret",
			secretToken: "secret",
			eventToken:  "test",
		},
		{
			name:        "invalid when X-Gitlab-Token header missing",
			wantErr:     "no X-Gitlab-Token header detected",
			secretToken: "secret",
			eventToken:  "",
		},
		{
			name:        "invalid when both token and secret are empty (security fix)",
			wantErr:     "no X-Gitlab-Token header detected",
			secretToken: "",
			eventToken:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Provider{}

			httpHeader := http.Header{}
			httpHeader.Set("X-Gitlab-Token", tt.eventToken)

			event := info.NewEvent()
			event.Request = &info.Request{
				Header: httpHeader,
			}
			event.Provider = &info.Provider{
				WebhookSecret: tt.secretToken,
			}

			err := v.Validate(context.TODO(), nil, event)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGetFiles(t *testing.T) {
	tests := []struct {
		name                             string
		event                            *info.Event
		mrchanges                        []*gitlab.MergeRequestDiff
		diffAPILimitExceeded             bool
		pushChanges                      []*gitlab.Diff
		wantAddedFilesCount              int
		wantDeletedFilesCount            int
		wantModifiedFilesCount           int
		wantRenamedFilesCount            int
		sourceProjectID, targetProjectID int
		wantError                        bool
		apiCallCount                     int64
	}{
		{
			name: "pull-request",
			event: &info.Event{
				TriggerTarget:     "pull_request",
				Organization:      "pullrequestowner",
				Repository:        "pullrequestrepository",
				PullRequestNumber: 10,
				BaseBranch:        "main",
				HeadBranch:        "feature",
				SHA:               "abc123",
			},
			mrchanges: []*gitlab.MergeRequestDiff{
				{
					NewPath: "modified.yaml",
				},
				{
					NewPath: "added.doc",
					NewFile: true,
				},
				{
					NewPath:     "removed.yaml",
					DeletedFile: true,
				},
				{
					NewPath:     "renamed.doc",
					RenamedFile: true,
				},
			},
			wantAddedFilesCount:    1,
			wantDeletedFilesCount:  1,
			wantModifiedFilesCount: 1,
			wantRenamedFilesCount:  1,
			targetProjectID:        10,
			apiCallCount:           2,
		},
		{
			name: "merge request exceeding gitlab Diff API",
			event: &info.Event{
				TriggerTarget:     "pull_request",
				Organization:      "pullrequestowner",
				Repository:        "pullrequestrepository",
				PullRequestNumber: 10,
				BaseBranch:        "main",
				HeadBranch:        "feature",
				SHA:               "abc123",
			},
			mrchanges: []*gitlab.MergeRequestDiff{
				{
					NewPath: "modified.yaml",
				},
				{
					NewPath: "added.doc",
					NewFile: true,
				},
				{
					NewPath:     "removed.yaml",
					DeletedFile: true,
				},
				{
					NewPath:     "renamed.doc",
					RenamedFile: true,
				},
			},
			wantAddedFilesCount:    1,
			wantDeletedFilesCount:  1,
			wantModifiedFilesCount: 1,
			wantRenamedFilesCount:  1,
			targetProjectID:        10,
			wantError:              false,
			apiCallCount:           2,
			diffAPILimitExceeded:   true,
		},
		{
			name: "pull-request with wrong project ID",
			event: &info.Event{
				TriggerTarget:     "pull_request",
				Organization:      "pullrequestowner",
				Repository:        "pullrequestrepository",
				PullRequestNumber: 10,
			},
			mrchanges: []*gitlab.MergeRequestDiff{
				{
					NewPath: "modified.yaml",
				},
				{
					NewPath: "added.doc",
					NewFile: true,
				},
				{
					NewPath:     "removed.yaml",
					DeletedFile: true,
				},
				{
					NewPath:     "renamed.doc",
					RenamedFile: true,
				},
			},
			wantAddedFilesCount:    0,
			wantDeletedFilesCount:  0,
			wantModifiedFilesCount: 0,
			wantRenamedFilesCount:  0,
			targetProjectID:        12,
			wantError:              true,
			apiCallCount:           1,
		},
		{
			name: "push",
			event: &info.Event{
				TriggerTarget: "push",
				Organization:  "pushrequestowner",
				Repository:    "pushrequestrepository",
				SHA:           "shacommitinfo",
			},
			pushChanges: []*gitlab.Diff{
				{
					NewPath: "modified.yaml",
				},
				{
					NewPath: "added.doc",
					NewFile: true,
				},
				{
					NewPath:     "removed.yaml",
					DeletedFile: true,
				},
				{
					NewPath:     "renamed.doc",
					RenamedFile: true,
				},
			},
			wantAddedFilesCount:    1,
			wantDeletedFilesCount:  1,
			wantModifiedFilesCount: 1,
			wantRenamedFilesCount:  1,
			sourceProjectID:        0,
			apiCallCount:           1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, teardown := thelp.Setup(t)
			defer teardown()

			if tt.event.TriggerTarget == "pull_request" {
				mux.HandleFunc(fmt.Sprintf("/projects/10/merge_requests/%d", tt.event.PullRequestNumber),
					func(rw http.ResponseWriter, _ *http.Request) {
						resp := gitlab.MergeRequest{
							ChangesCount: strconv.Itoa(len(tt.mrchanges)),
						}
						if tt.diffAPILimitExceeded {
							resp.ChangesCount = strconv.Itoa(len(tt.mrchanges)-1) + "+"
						}
						jeez, err := json.Marshal(resp)
						assert.NilError(t, err)
						_, _ = rw.Write(jeez)
					})
				mux.HandleFunc(fmt.Sprintf("/projects/10/merge_requests/%d/diffs",
					tt.event.PullRequestNumber), func(rw http.ResponseWriter, _ *http.Request) {
					diffAPIChanges := tt.mrchanges
					if tt.diffAPILimitExceeded {
						// The Diff API will not return the full list of files
						diffAPIChanges = diffAPIChanges[:len(tt.mrchanges)-1]
					}

					jeez, err := json.Marshal(diffAPIChanges)
					assert.NilError(t, err)
					_, _ = rw.Write(jeez)
				})
				mux.HandleFunc("/projects/10/repository/compare", func(rw http.ResponseWriter, req *http.Request) {
					if req.URL.Query().Get("from") != tt.event.BaseBranch || req.URL.Query().Get("to") != tt.event.SHA {
						t.Errorf("expecting compare API call with 'from=%s&to=%s', got %s", tt.event.BaseBranch, tt.event.SHA, req.URL.Query().Encode())
					}

					// always return the full list, not subject to the Diff API size limitations
					diffs := []*gitlab.Diff{}
					for _, d := range tt.mrchanges {
						diffs = append(diffs, &gitlab.Diff{NewPath: d.NewPath, NewFile: d.NewFile, RenamedFile: d.RenamedFile, DeletedFile: d.DeletedFile})
					}
					jeez, err := json.Marshal(gitlab.Compare{Diffs: diffs})
					assert.NilError(t, err)
					_, _ = rw.Write(jeez)
				})
			}
			if tt.event.TriggerTarget == "push" {
				mux.HandleFunc(fmt.Sprintf("/projects/0/repository/commits/%s/diff", tt.event.SHA),
					func(rw http.ResponseWriter, _ *http.Request) {
						jeez, err := json.Marshal(tt.pushChanges)
						assert.NilError(t, err)
						_, _ = rw.Write(jeez)
					})
			}

			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			metricProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(metricProvider)

			providerInfo := &Provider{gitlabClient: fakeclient, sourceProjectID: int64(tt.sourceProjectID), targetProjectID: int64(tt.targetProjectID), triggerEvent: string(tt.event.TriggerTarget), apiURL: "api.gitlab.com"}
			changedFiles, err := providerInfo.GetFiles(ctx, tt.event)
			if tt.wantError != true {
				assert.NilError(t, err, nil)
			}
			assert.Equal(t, tt.wantAddedFilesCount, len(changedFiles.Added))
			assert.Equal(t, tt.wantDeletedFilesCount, len(changedFiles.Deleted))
			assert.Equal(t, tt.wantModifiedFilesCount, len(changedFiles.Modified))
			assert.Equal(t, tt.wantRenamedFilesCount, len(changedFiles.Renamed))

			if tt.event.TriggerTarget == "pull_request" {
				for i := range changedFiles.All {
					assert.Equal(t, tt.mrchanges[i].NewPath, changedFiles.All[i])
				}
			}
			if tt.event.TriggerTarget == "push" {
				for i := range changedFiles.All {
					assert.Equal(t, tt.pushChanges[i].NewPath, changedFiles.All[i])
				}
			}

			var rm metricdata.ResourceMetrics
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			assert.Equal(t, len(rm.ScopeMetrics), 1)
			assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
			assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
			count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(tt.apiCallCount))

			_, _ = providerInfo.GetFiles(ctx, tt.event)

			// recollect the metrics after the second call
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")
			if tt.wantError {
				// No caching on error
				count, ok = rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(tt.apiCallCount*2))
			} else {
				// Cache API results on success
				count, ok = rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(tt.apiCallCount))
			}
		})
	}
}

func TestGetFilesPaging(t *testing.T) {
	tests := []struct {
		name  string
		event *info.Event
	}{
		{
			name: "pull-request",
			event: &info.Event{
				TriggerTarget: "pull_request",
				Organization:  "owner",
				Repository:    "repository",
			},
		},
		{
			name: "push",
			event: &info.Event{
				TriggerTarget: "push",
				Organization:  "owner",
				Repository:    "repository",
				SHA:           "shacommitinfo",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, teardown := thelp.Setup(t)
			defer teardown()

			changeCount := 5
			apiCallCount := changeCount
			if tt.event.TriggerTarget == "pull_request" {
				// Extra request made to check the diff API limit
				apiCallCount++
			}

			if tt.event.TriggerTarget == "pull_request" {
				mux.HandleFunc(fmt.Sprintf("/projects/0/merge_requests/%d",
					tt.event.PullRequestNumber), func(rw http.ResponseWriter, _ *http.Request) {
					jeez, err := json.Marshal(gitlab.MergeRequest{ChangesCount: strconv.Itoa(changeCount)})
					assert.NilError(t, err)
					_, _ = rw.Write(jeez)
				})
				mux.HandleFunc(fmt.Sprintf("/projects/0/merge_requests/%d/diffs",
					tt.event.PullRequestNumber), func(rw http.ResponseWriter, req *http.Request) {
					pageCount := thelp.SetPagingHeader(t, rw, req, changeCount)
					jeez, err := json.Marshal([]*gitlab.MergeRequestDiff{{NewPath: fmt.Sprintf("change-%d.txt", pageCount)}})
					assert.NilError(t, err)
					_, _ = rw.Write(jeez)
				})
			}
			if tt.event.TriggerTarget == "push" {
				mux.HandleFunc(fmt.Sprintf("/projects/0/repository/commits/%s/diff",
					tt.event.SHA), func(rw http.ResponseWriter, req *http.Request) {
					pageCount := thelp.SetPagingHeader(t, rw, req, changeCount)
					jeez, err := json.Marshal([]*gitlab.Diff{{NewPath: fmt.Sprintf("change-%d.txt", pageCount)}})
					assert.NilError(t, err)
					_, _ = rw.Write(jeez)
				})
			}

			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			metricProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(metricProvider)

			providerInfo := &Provider{gitlabClient: fakeclient, sourceProjectID: 0, targetProjectID: 0, triggerEvent: string(tt.event.TriggerTarget), apiURL: "api.gitlab.com"}
			changedFiles, err := providerInfo.GetFiles(ctx, tt.event)
			assert.NilError(t, err, nil)
			assert.DeepEqual(t, changedFiles.All, []string{"change-1.txt", "change-2.txt", "change-3.txt", "change-4.txt", "change-5.txt"})
			assert.Equal(t, len(changedFiles.Modified), changeCount)

			// Check caching
			var rm metricdata.ResourceMetrics
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			assert.Equal(t, len(rm.ScopeMetrics), 1)
			assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
			assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
			count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(apiCallCount))

			_, _ = providerInfo.GetFiles(ctx, tt.event)

			// recollect the metrics after the second call
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			count, ok = rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(apiCallCount))
		})
	}
}

func TestIsHeadCommitOfBranch(t *testing.T) {
	tests := []struct {
		name          string
		event         *info.Event
		branchName    string
		wantClient    bool
		errStatusCode int
		ErrMsg        string
	}{
		{
			name:       "bad/client is not initialized",
			wantClient: false,
			ErrMsg:     "no gitlab client has been initialized",
		},
		{
			name:          "bad/user is not authorized",
			wantClient:    true,
			branchName:    "cool-branch",
			errStatusCode: http.StatusUnauthorized,
			ErrMsg:        "401",
		},
		{
			name:          "bad/branch doesn't exist",
			wantClient:    true,
			branchName:    "wrong-branch",
			errStatusCode: http.StatusNotFound,
			ErrMsg:        "404",
		},
		{
			name:       "bad/SHA is not HEAD of the branch",
			wantClient: true,
			event:      &info.Event{SHA: "IAmNotHEAD"},
			branchName: "cool-branch",
			ErrMsg:     "provided SHA IAmNotHEAD is not the HEAD commit of the branch cool-branch",
		},
		{
			name:       "good/SHA is HEAD commit",
			wantClient: true,
			event:      &info.Event{SHA: "IAmHEAD321"},
			branchName: "cool-branch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _ = rtesting.SetupFakeContext(t)
			fakeclient, mux, teardown := thelp.Setup(t)
			defer teardown()
			glProvider := &Provider{sourceProjectID: 1}
			if tt.wantClient {
				glProvider.SetGitLabClient(fakeclient)
				mux.HandleFunc("/projects/1/repository/branches/cool-branch",
					func(rw http.ResponseWriter, _ *http.Request) {
						if tt.errStatusCode != 0 {
							rw.WriteHeader(tt.errStatusCode)
							return
						}
						branch := &gitlab.Branch{Name: "cool-branch", Commit: &gitlab.Commit{ID: "IAmHEAD321"}}
						bytes, _ := json.Marshal(branch)
						_, _ = rw.Write(bytes)
					})
			}
			err := glProvider.isHeadCommitOfBranch(tt.event, tt.branchName)
			if tt.ErrMsg != "" {
				assert.ErrorContains(t, err, tt.ErrMsg)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestGitLabCreateComment(t *testing.T) {
	tests := []struct {
		name          string
		event         *info.Event
		commit        string
		updateMarker  string
		mockResponses map[string]func(rw http.ResponseWriter, _ *http.Request)
		wantErr       string
		clientNil     bool
	}{
		{
			name:      "nil client error",
			clientNil: true,
			event:     &info.Event{PullRequestNumber: 123},
			wantErr:   "no gitlab client has been initialized",
		},
		{
			name:    "not a merge request error",
			event:   &info.Event{PullRequestNumber: 0},
			wantErr: "create comment only works on merge requests",
		},
		{
			name:         "create new comment",
			event:        &info.Event{PullRequestNumber: 123, TargetProjectID: 666},
			commit:       "New Comment",
			updateMarker: "",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/projects/666/merge_requests/123/notes": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodPost)
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "update existing comment",
			event:        &info.Event{PullRequestNumber: 123, TargetProjectID: 666},
			commit:       "Updated Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100}`)
				},
				"/projects/666/merge_requests/123/notes": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "MARKER", "author": {"id": 100}}]`)
						return
					}
				},
				"/projects/666/merge_requests/123/notes/555": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, "PUT")
					rw.WriteHeader(http.StatusOK)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "no matching comment creates new",
			event:        &info.Event{PullRequestNumber: 123, TargetProjectID: 666},
			commit:       "New Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100}`)
				},
				"/projects/666/merge_requests/123/notes": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "NO_MATCH", "author": {"id": 200}}]`)
						return
					}
					assert.Equal(t, r.Method, http.MethodPost)
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "skip comment from different user and create new",
			event:        &info.Event{PullRequestNumber: 123, TargetProjectID: 666},
			commit:       "Updated Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100}`)
				},
				"/projects/666/merge_requests/123/notes": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "Old MARKER", "author": {"id": 999}}]`)
						return
					}
					assert.Equal(t, r.Method, http.MethodPost)
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{}`)
				},
				"/projects/666/merge_requests/123/notes/555": func(rw http.ResponseWriter, _ *http.Request) {
					t.Error("edit endpoint should not be called for comment from different user")
					rw.WriteHeader(http.StatusOK)
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := thelp.Setup(t)
			defer teardown()
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			if tt.clientNil {
				p := &Provider{
					Logger:          logger,
					sourceProjectID: 666,
				}
				err := p.CreateComment(context.Background(), tt.event, tt.commit, tt.updateMarker)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			for endpoint, handler := range tt.mockResponses {
				mux.HandleFunc(endpoint, handler)
			}

			p := &Provider{
				sourceProjectID: 666,
				gitlabClient:    fakeclient,
				Logger:          logger,
			}
			err := p.CreateComment(context.Background(), tt.event, tt.commit, tt.updateMarker)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGitLabCreateCommentPaging(t *testing.T) {
	updated := false
	event := &info.Event{PullRequestNumber: 123, TargetProjectID: 666}
	commit := "Updated Comment"
	updateMarker := "MARKER"
	mockResponses := map[string]func(rw http.ResponseWriter, _ *http.Request){
		"/user": func(rw http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(rw, `{"id": 100}`)
		},
		"/projects/666/merge_requests/123/notes": func(rw http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				page := thelp.SetPagingHeader(t, rw, r, 100)
				note := "unrelated"
				if page == 10 {
					note = "MARKER"
				} else if page > 10 {
					t.Error("notes shouldn't be queries past the expected ID")
				}
				fmt.Fprintf(rw, `[{"id": %d, "body": "%s", "author": {"id": 100}}]`, page, note)
			}
		},
		"/projects/666/merge_requests/123/notes/{id}": func(rw http.ResponseWriter, r *http.Request) {
			assert.Equal(t, r.Method, "PUT")
			if r.PathValue("id") == "10" {
				rw.WriteHeader(http.StatusOK)
				updated = true
			} else {
				rw.WriteHeader(http.StatusNotFound)
				t.Errorf("note %s is not intended to be updated", r.PathValue("id"))
			}
			fmt.Fprint(rw, `{}`)
		},
	}

	fakeclient, mux, teardown := thelp.Setup(t)
	defer teardown()
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	for endpoint, handler := range mockResponses {
		mux.HandleFunc(endpoint, handler)
	}

	p := &Provider{
		sourceProjectID: 666,
		gitlabClient:    fakeclient,
		Logger:          logger,
	}
	err := p.CreateComment(context.Background(), event, commit, updateMarker)
	assert.NilError(t, err)
	assert.Assert(t, updated == true, "comment update handler has not been called")
}
