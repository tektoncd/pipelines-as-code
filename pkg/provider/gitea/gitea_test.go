package gitea

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestProviderCreateStatus(t *testing.T) {
	type args struct {
		event      *info.Event
		statusOpts status.StatusOpts
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Test with success conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionSuccess,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with failure conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionFailure,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with pending conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionPending,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with neutral conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionNeutral,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with in_progress status",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Status: "in_progress",
				},
			},
			wantErr: false,
		},
		{
			name: "Test with onpr",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
				},
			},
			wantErr: false,
		},
		{
			name: "Test with ok-to-test event",
			args: args{
				event: &info.Event{EventType: triggertype.OkToTest.String()},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
				},
			},
			wantErr: false,
		},
		{
			name: "Test with oncomment event",
			args: args{
				event: &info.Event{EventType: opscomments.OkToTestCommentEventType.String()},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
				},
			},
			wantErr: false,
		},
		{
			name: "Test status_text",
			args: args{
				event: &info.Event{EventType: triggertype.PullRequest.String()},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
					Text:            "mytext",
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			run := params.New()
			p := &Provider{
				giteaClient: fakeclient, // Set this to a valid client for the tests where wantErr is false
				run:         run,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: settings.PACApplicationNameDefaultValue,
					},
				},
			}
			tt.args.event.Organization = "myorg"
			tt.args.event.Repository = "myrepo"

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/0/comments", tt.args.event.Organization, tt.args.event.Repository), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(rw, `{"state":"success"}`)
			})
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/statuses/", tt.args.event.Organization, tt.args.event.Repository), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(rw, `{"state":"success"}`)
			})
			if err := p.CreateStatus(context.Background(), tt.args.event, tt.args.statusOpts); (err != nil) != tt.wantErr {
				t.Errorf("Provider.CreateStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func computeHMACSHA256(payload, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestProviderValidate(t *testing.T) {
	testPayload := []byte(`{"ref":"refs/heads/main"}`)
	testSecret := "mysecret"
	validSignature := computeHMACSHA256(testPayload, []byte(testSecret))

	tests := []struct {
		name            string
		signatureHeader string
		signature       string
		secret          string
		payload         []byte
		wantErr         string
	}{
		{
			name:            "valid forgejo signature",
			signatureHeader: ForgejoSignatureHeader,
			signature:       validSignature,
			secret:          testSecret,
			payload:         testPayload,
		},
		{
			name:            "valid gitea signature",
			signatureHeader: GiteaSignatureHeader,
			signature:       validSignature,
			secret:          testSecret,
			payload:         testPayload,
		},
		{
			name:            "invalid signature mismatch",
			signatureHeader: ForgejoSignatureHeader,
			signature:       computeHMACSHA256([]byte("wrong payload"), []byte(testSecret)),
			secret:          testSecret,
			payload:         testPayload,
			wantErr:         "gitea/forgejo webhook signature validation failed",
		},
		{
			name:            "invalid hex in signature",
			signatureHeader: ForgejoSignatureHeader,
			signature:       "not-valid-hex!@#$",
			secret:          testSecret,
			payload:         testPayload,
			wantErr:         "gitea/forgejo webhook signature is not valid hex",
		},
		{
			name:            "signature present but no secret configured",
			signatureHeader: ForgejoSignatureHeader,
			signature:       validSignature,
			secret:          "",
			payload:         testPayload,
			wantErr:         "no webhook secret has been set, in repository CR or secret",
		},
		{
			name:            "secret configured but no signature",
			signatureHeader: "",
			signature:       "",
			secret:          testSecret,
			payload:         testPayload,
			wantErr:         "no signature has been detected, for security reason we are not allowing webhooks without a secret",
		},
		{
			name:            "no secret and no signature",
			signatureHeader: "",
			signature:       "",
			secret:          "",
			payload:         testPayload,
			wantErr:         "no signature has been detected, for security reason we are not allowing webhooks without a secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			header := http.Header{}
			if tt.signatureHeader != "" && tt.signature != "" {
				header.Set(tt.signatureHeader, tt.signature)
			}

			event := &info.Event{
				Provider: &info.Provider{
					WebhookSecret: tt.secret,
				},
				Request: &info.Request{
					Header:  header,
					Payload: tt.payload,
				},
			}

			p := &Provider{Logger: logger}
			err := p.Validate(context.Background(), nil, event)

			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error but got nil")
				assert.Assert(t, strings.Contains(err.Error(), tt.wantErr),
					"expected error to contain %q, got %q", tt.wantErr, err.Error())
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestProviderGetFiles(t *testing.T) {
	type args struct {
		runevent *info.Event
	}
	tests := []struct {
		name                string
		args                args
		changedFiles        string
		want                changedfiles.ChangedFiles
		wantErr             bool
		wantAPIRequestCount int64
	}{
		{
			name: "pull_request",
			args: args{
				runevent: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
				},
			},
			want: changedfiles.ChangedFiles{
				All: []string{
					"added.txt",
					"deleted.txt",
					"modified.txt",
					"renamed.txt",
				},
				Added: []string{
					"added.txt",
				},
				Deleted:  []string{"deleted.txt"},
				Modified: []string{"modified.txt"},
				Renamed:  []string{"renamed.txt"},
			},
			changedFiles:        `[{"filename":"added.txt","status":"added"},{"filename":"deleted.txt","status":"deleted"},{"filename":"modified.txt","status":"changed"},{"filename":"renamed.txt","status":"renamed"}]`,
			wantAPIRequestCount: 1,
		},
		{
			name: "push",
			args: args{
				runevent: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: -1,
					TriggerTarget:     "push",
					Request: &info.Request{
						Payload: []byte(`{"ref":"refs/heads/main","commits":[{"added":["added.txt"],"removed":["deleted.txt"],"modified":["modified.txt"]},{"added":[".tekton/pullrequest.yaml",".tekton/push.yaml"],"removed":[],"modified":[]}]}`),
					},
				},
			},
			want: changedfiles.ChangedFiles{
				All: []string{
					".tekton/pullrequest.yaml",
					".tekton/push.yaml",
					"added.txt",
					"deleted.txt",
					"modified.txt",
				},
				Added: []string{
					".tekton/pullrequest.yaml",
					".tekton/push.yaml",
					"added.txt",
				},
				Deleted:  []string{"deleted.txt"},
				Modified: []string{"modified.txt"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls/%d/files", tt.args.runevent.Organization, tt.args.runevent.Repository, tt.args.runevent.PullRequestNumber), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.changedFiles)
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{},
			}}
			giteaInstanceURL := "https://gitea.example.com"
			gprovider := Provider{
				giteaClient:      fakeclient,
				repo:             repo,
				Logger:           logger,
				giteaInstanceURL: giteaInstanceURL,
				triggerEvent:     string(tt.args.runevent.TriggerTarget),
			}

			got, err := gprovider.GetFiles(ctx, tt.args.runevent)

			if (err != nil) != tt.wantErr {
				t.Errorf("Provider.GetFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			sort.Strings(got.All)
			sort.Strings(tt.want.All)

			sort.Strings(got.Added)
			sort.Strings(tt.want.Added)

			sort.Strings(got.Deleted)
			sort.Strings(tt.want.Deleted)

			sort.Strings(got.Modified)
			sort.Strings(tt.want.Modified)

			sort.Strings(got.Renamed)
			sort.Strings(tt.want.Renamed)
			if !reflect.DeepEqual(got.All, tt.want.All) {
				t.Errorf("Provider.GetFiles() All = %v, want %v", got.All, tt.want.All)
			}
			if !reflect.DeepEqual(got.Added, tt.want.Added) {
				t.Errorf("Provider.GetFiles() Added = %v, want %v", got.Added, tt.want.Added)
			}
			if !reflect.DeepEqual(got.Deleted, tt.want.Deleted) {
				t.Errorf("Provider.GetFiles() Deleted = %v, want %v", got.Deleted, tt.want.Deleted)
			}
			if !reflect.DeepEqual(got.Modified, tt.want.Modified) {
				t.Errorf("Provider.GetFiles() Modified = %v, want %v", got.Modified, tt.want.Modified)
			}
			if !reflect.DeepEqual(got.Renamed, tt.want.Renamed) {
				t.Errorf("Provider.GetFiles() Renamed = %v, want %v", got.Renamed, tt.want.Renamed)
			}

			// Verify metrics from first call
			if tt.wantAPIRequestCount > 0 {
				var rm metricdata.ResourceMetrics
				err = reader.Collect(ctx, &rm)
				assert.NilError(t, err, "error collecting metrics")

				assert.Equal(t, len(rm.ScopeMetrics), 1)
				assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
				assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
				count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(tt.wantAPIRequestCount))
			}

			// Verify caching: second call should return cached result without additional API calls
			got2, err2 := gprovider.GetFiles(ctx, tt.args.runevent)
			assert.NilError(t, err2)
			assert.DeepEqual(t, got, got2)

			if tt.wantAPIRequestCount > 0 {
				var rm metricdata.ResourceMetrics
				err = reader.Collect(ctx, &rm)
				assert.NilError(t, err, "error collecting metrics")
				count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(tt.wantAPIRequestCount))
			}
		})
	}
}

func TestProviderCreateStatusCommit(t *testing.T) {
	type args struct {
		event   *info.Event
		pacopts *info.PacOpts
		status  status.StatusOpts
	}
	tests := []struct {
		name                            string
		args                            args
		wantErr                         bool
		wantCommentJSON, wantStatusJSON string
	}{
		{
			name: "success",
			args: args{
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
					SHA:               "123456",
				},
				status: status.StatusOpts{
					Conclusion: status.ConclusionNeutral,
				},
			},
			wantStatusJSON: `{"state":"success","target_url":"","description":"","context":"myapp"}`,
		},
		{
			name: "pending",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
					SHA:               "123456",
				},
			},
			wantStatusJSON: `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
		},
		{
			name: "pending from status",
			args: args{
				status: status.StatusOpts{
					Status: "in_progress",
					Title:  "Pipeline run for myapp has been triggered",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
					SHA:               "123456",
				},
			},
			wantStatusJSON: `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
		},
		{
			name: "ok-to-test",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
					Text:       "time to get started",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					EventType:         triggertype.OkToTest.String(),
					SHA:               "123456",
				},
			},
			wantStatusJSON:  `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
			wantCommentJSON: `{"body":"\ntime to get started"}`,
		},
		{
			name: "cancel",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
					Text:       "time to get started",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					EventType:         triggertype.Cancel.String(),
					SHA:               "123456",
				},
			},
			wantStatusJSON:  `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
			wantCommentJSON: `{"body":"\ntime to get started"}`,
		},
		{
			name: "retest",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
					Text:       "time to get started",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					EventType:         triggertype.Retest.String(),
					SHA:               "123456",
				},
			},
			wantStatusJSON:  `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
			wantCommentJSON: `{"body":"\ntime to get started"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			// Mock the CreateStatus API
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/statuses/%s", tt.args.event.Organization, tt.args.event.Repository, tt.args.event.SHA), func(rw http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(rw, "Failed to read request body", http.StatusInternalServerError)
					return
				}

				if res := cmp.Diff(string(tt.wantStatusJSON), string(body)); res != "" {
					t.Errorf("Received: %s Diff %s:", string(body), res)
				}

				_, _ = rw.Write([]byte(`{"state":"success"}`))
			})

			// Mock the CreateIssueComment API
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", tt.args.event.Organization, tt.args.event.Repository, tt.args.event.PullRequestNumber), func(rw http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(rw, "Failed to read request body", http.StatusInternalServerError)
					return
				}

				if res := cmp.Diff(string(tt.wantCommentJSON), string(body)); res != "" {
					t.Errorf("Received: %s Diff %s:", string(body), res)
				}
				_, _ = rw.Write([]byte(`{"body":"Pipeline run for myapp has been triggered"}`))
			})

			v := &Provider{
				giteaClient: fakeclient,
			}

			if err := v.createStatusCommit(context.Background(), tt.args.event, tt.args.pacopts, tt.args.status); (err != nil) != tt.wantErr {
				t.Errorf("Provider.createStatusCommit() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProviderCreateStatusCommitRetryOnTransientError(t *testing.T) {
	tests := []struct {
		name           string
		failCount      int
		errorMessage   string
		wantErr        bool
		wantRetryCount int
	}{
		{
			name:           "retry on user does not exist error",
			failCount:      2,
			errorMessage:   "user does not exist [uid: 0, name: ]",
			wantErr:        false,
			wantRetryCount: 2,
		},
		{
			name:           "fail after max retries",
			failCount:      5, // More than maxRetries (3)
			errorMessage:   "user does not exist [uid: 0, name: ]",
			wantErr:        true,
			wantRetryCount: 3,
		},
		{
			name:           "no retry on other errors",
			failCount:      1,
			errorMessage:   "some other error",
			wantErr:        true,
			wantRetryCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			observer, logs := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			callCount := 0
			mux.HandleFunc("/repos/myorg/myrepo/statuses/abc123", func(rw http.ResponseWriter, _ *http.Request) {
				callCount++
				if callCount <= tt.failCount {
					rw.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprintf(rw, `{"message": "%s"}`, tt.errorMessage)
					return
				}
				_, _ = rw.Write([]byte(`{"state":"success"}`))
			})

			fc := clockwork.NewFakeClock()
			v := &Provider{
				giteaClient: fakeclient,
				Logger:      logger,
				clock:       fc,
			}

			event := &info.Event{
				Organization: "myorg",
				Repository:   "myrepo",
				SHA:          "abc123",
			}
			pacopts := &info.PacOpts{Settings: settings.Settings{
				ApplicationName: "myapp",
			}}
			status := status.StatusOpts{
				Conclusion: "success",
			}

			ctx := context.Background()
			if strings.Contains(tt.errorMessage, "user does not exist") && tt.failCount > 0 {
				// Drive the fake clock only for retryable cases that actually sleep.
				go func() {
					for i := range 3 {
						fc.BlockUntilContext(ctx, 1) //nolint:errcheck
						fc.Advance(time.Duration(i+1) * 500 * time.Millisecond)
					}
				}()
			}

			err := v.createStatusCommit(ctx, event, pacopts, status)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected an error but got none")
			} else {
				assert.NilError(t, err)
			}

			// Verify the number of API calls made
			isTransientError := strings.Contains(tt.errorMessage, "user does not exist")
			switch {
			case tt.wantErr && isTransientError:
				// For transient errors, we should have tried maxRetries (3) times
				assert.Equal(t, 3, callCount, "expected 3 retries for transient error")
			case tt.wantErr:
				// For non-retryable errors, we should have tried only once
				assert.Equal(t, 1, callCount, "expected only 1 attempt for non-retryable error")
			default:
				// For success after retries, we should have failCount+1 calls
				assert.Equal(t, tt.failCount+1, callCount, "expected failCount+1 calls for eventual success")
			}

			// Verify warning logs were emitted for retries on "user does not exist" errors
			if strings.Contains(tt.errorMessage, "user does not exist") && tt.failCount > 0 {
				retryLogs := 0
				for _, log := range logs.All() {
					if strings.Contains(log.Message, "CreateStatus failed with transient error") {
						retryLogs++
					}
				}
				expectedLogs := min(tt.failCount, 3)
				assert.Equal(t, expectedLogs, retryLogs, "unexpected number of retry warning logs")
			}
		})
	}
}

func TestGetTektonDir(t *testing.T) {
	testGetTektonDir := []struct {
		treepath             string
		event                *info.Event
		name                 string
		expectedString       string
		provenance           string
		filterMessageSnippet string
		wantErr              string
	}{
		{
			name: "test with badly formatted yaml",
			event: &info.Event{
				Organization: "tekton",
				Repository:   "cat",
				SHA:          "123",
			},
			treepath: "testdata/tree/badyaml",
			wantErr:  "error unmarshalling yaml file badyaml.yaml: yaml: line 2: did not find expected key",
		},
	}
	for _, tt := range testGetTektonDir {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			gvcs := Provider{
				giteaClient: fakeclient,
				Logger:      fakelogger,
			}
			if tt.provenance == "default_branch" {
				tt.event.SHA = tt.event.DefaultBranch
			} else {
				shaDir := fmt.Sprintf("%x", sha256.Sum256([]byte(tt.treepath)))
				tt.event.SHA = shaDir
			}

			tgitea.SetupGitTree(t, mux, tt.treepath, tt.event, false)
			got, err := gvcs.GetTektonDir(ctx, tt.event, ".tekton", tt.provenance)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "we should have get an error here")
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(got, tt.expectedString), "expected %s, got %s", tt.expectedString, got)
		})
	}
}

func TestCreateComment(t *testing.T) {
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
			wantErr:   "no gitea client has been initialized",
		},
		{
			name:    "not a pull request error",
			event:   &info.Event{PullRequestNumber: 0},
			wantErr: "create comment only works on pull requests",
		},
		{
			name:         "create new comment",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "New Comment",
			updateMarker: "",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodPost)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "update existing comment",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "Updated Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "MARKER", "user": {"id": 100}}]`)
						return
					}
				},
				"/repos/org/repo/issues/comments/555": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, "PATCH")
					rw.WriteHeader(http.StatusOK)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "no matching comment creates new",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "New Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "NO_MATCH", "user": {"id": 200}}]`)
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
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "Updated Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "Old MARKER", "user": {"id": 999}}]`)
						return
					}
					assert.Equal(t, r.Method, http.MethodPost)
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{}`)
				},
				"/repos/org/repo/issues/comments/555": func(rw http.ResponseWriter, _ *http.Request) {
					t.Error("edit endpoint should not be called for comment from different user")
					rw.WriteHeader(http.StatusOK)
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			if tt.clientNil {
				p := &Provider{}
				err := p.CreateComment(context.Background(), tt.event, tt.commit, tt.updateMarker)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			for endpoint, handler := range tt.mockResponses {
				mux.HandleFunc(endpoint, handler)
			}

			p := &Provider{giteaClient: fakeclient, Logger: fakelogger}
			err := p.CreateComment(context.Background(), tt.event, tt.commit, tt.updateMarker)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestCreateStatusUpdateCommentNormalizesBreaks(t *testing.T) {
	fakeclient, mux, teardown := tgitea.Setup(t)
	defer teardown()

	var createCommentBody string
	mux.HandleFunc("/repos/org/repo/statuses/", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"state":"pending"}`)
	})
	mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(rw, `[]`)
		case http.MethodPost:
			b, err := io.ReadAll(r.Body)
			assert.NilError(t, err)
			createCommentBody = string(b)
			fmt.Fprint(rw, `{}`)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})

	p := &Provider{
		giteaClient: fakeclient,
		pacInfo: &info.PacOpts{
			Settings: settings.Settings{
				ApplicationName: settings.PACApplicationNameDefaultValue,
			},
		},
		repo: &v1alpha1.Repository{
			Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{
					Forgejo: &v1alpha1.ForgejoSettings{CommentStrategy: provider.UpdateCommentStrategy},
				},
			},
		},
	}

	err := p.CreateStatus(context.Background(), &info.Event{
		Organization:      "org",
		Repository:        "repo",
		SHA:               "abc123",
		PullRequestNumber: 123,
		EventType:         triggertype.PullRequest.String(),
		TriggerTarget:     triggertype.PullRequest,
	}, status.StatusOpts{
		Status:                  "in_progress",
		Text:                    "line1<br>line2",
		OriginalPipelineRunName: "demo",
		DetailsURL:              "https://example.test/log",
	})
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(createCommentBody, "<br>"), "comment body should not contain raw <br>: %s", createCommentBody)
	assert.Assert(t, strings.Contains(createCommentBody, "line1\\nline2"), "comment body should contain normalized newline: %s", createCommentBody)

	golden.Assert(t, createCommentBody, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
}

func TestFormatPipelineCommentEmoji(t *testing.T) {
	p := &Provider{
		pacInfo: &info.PacOpts{
			Settings: settings.Settings{
				ApplicationName: settings.PACApplicationNameDefaultValue,
			},
		},
	}

	tests := []struct {
		name   string
		status status.StatusOpts
		emoji  string
	}{
		{
			name: "failure conclusion uses failure emoji",
			status: status.StatusOpts{
				Conclusion:              status.ConclusionFailure,
				Title:                   "Failed",
				Text:                    "details",
				OriginalPipelineRunName: "demo",
				DetailsURL:              "https://example.test/log",
			},
			emoji: "❌",
		},
		{
			name: "in progress status uses rocket emoji",
			status: status.StatusOpts{
				Status:                  "in_progress",
				Title:                   "CI has Started",
				Text:                    "details",
				OriginalPipelineRunName: "demo",
				DetailsURL:              "https://example.test/log",
			},
			emoji: "🚀",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.formatPipelineComment("abc123", tt.status)
			assert.Assert(t, strings.HasPrefix(got, tt.emoji+" "), "expected prefix %q in comment %q", tt.emoji+" ", got)
		})
	}
}

func TestGetCommitInfo(t *testing.T) {
	tests := []struct {
		name                string
		event               *info.Event
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
				Organization: "owner",
				Repository:   "repo",
				SHA:          "abc123",
			},
			mockCommitResponse: `{
				"sha": "abc123",
				"html_url": "https://gitea.com/owner/repo/commit/abc123",
				"commit": {
					"message": "feat: add new feature\n\nThis is the full commit message with details.",
					"author": {
						"name": "John Doe",
						"email": "john@example.com",
						"date": "2024-01-15T10:30:00Z"
					},
					"committer": {
						"name": "Gitea",
						"email": "noreply@gitea.com",
						"date": "2024-01-15T10:31:00Z"
					}
				}
			}`,
			wantSHATitle:        "feat: add new feature",
			wantSHAURL:          "https://gitea.com/owner/repo/commit/abc123",
			wantSHAMessage:      "feat: add new feature\n\nThis is the full commit message with details.",
			wantAuthorName:      "John Doe",
			wantAuthorEmail:     "john@example.com",
			wantAuthorDate:      "2024-01-15T10:30:00Z",
			wantCommitterName:   "Gitea",
			wantCommitterEmail:  "noreply@gitea.com",
			wantCommitterDate:   "2024-01-15T10:31:00Z",
			checkExtendedFields: true,
		},
		{
			name: "basic fields only",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repo",
				SHA:          "def456",
			},
			mockCommitResponse: `{
				"sha": "def456",
				"html_url": "https://gitea.com/owner/repo/commit/def456",
				"commit": {
					"message": "fix: simple fix"
				}
			}`,
			wantSHATitle:   "fix: simple fix",
			wantSHAURL:     "https://gitea.com/owner/repo/commit/def456",
			wantSHAMessage: "fix: simple fix",
		},
		{
			name: "no client error",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repo",
				SHA:          "abc123",
			},
			noClient: true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			var provider *Provider
			if !tt.noClient {
				client, mux, tearDown := tgitea.Setup(t)
				defer tearDown()

				// Mock the GetSingleCommit API endpoint
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/commits/%s", tt.event.Organization, tt.event.Repository, tt.event.SHA),
					func(rw http.ResponseWriter, _ *http.Request) {
						fmt.Fprint(rw, tt.mockCommitResponse)
					})

				provider = &Provider{giteaClient: client}
			} else {
				provider = &Provider{}
			}

			err := provider.GetCommitInfo(ctx, tt.event)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				return
			}

			assert.NilError(t, err)
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
		})
	}
}

func TestGetCommitInfoPRLookupPopulatesURLs(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	client, mux, tearDown := tgitea.Setup(t)
	defer tearDown()

	event := &info.Event{
		Organization:      "owner",
		Repository:        "repo",
		PullRequestNumber: 42,
		// SHA intentionally empty to trigger PR lookup path
	}

	// Mock GetPullRequest endpoint
	mux.HandleFunc("/repos/owner/repo/pulls/42", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{
			"head": {
				"ref": "feature-branch",
				"sha": "abc123",
				"repo": {
					"html_url": "https://gitea.com/fork-owner/repo"
				}
			},
			"base": {
				"ref": "main",
				"repo": {
					"html_url": "https://gitea.com/owner/repo"
				}
			}
		}`)
	})

	// Mock GetSingleCommit endpoint
	mux.HandleFunc("/repos/owner/repo/git/commits/abc123", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{
			"sha": "abc123",
			"html_url": "https://gitea.com/owner/repo/commit/abc123",
			"commit": {
				"message": "feat: test commit"
			}
		}`)
	})

	provider := &Provider{giteaClient: client}
	err := provider.GetCommitInfo(ctx, event)
	assert.NilError(t, err)
	assert.Equal(t, "abc123", event.SHA)
	assert.Equal(t, "feature-branch", event.HeadBranch)
	assert.Equal(t, "main", event.BaseBranch)
	assert.Equal(t, "https://gitea.com/fork-owner/repo", event.HeadURL, "HeadURL should be populated from PR lookup")
	assert.Equal(t, "https://gitea.com/owner/repo", event.BaseURL, "BaseURL should be populated from PR lookup")
}
