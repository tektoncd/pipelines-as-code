package bitbucketcloud

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ktrysmt/go-bitbucket"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	bbcloudtest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/test"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetConfig(t *testing.T) {
	v := &Provider{}
	config := v.GetConfig()
	assert.Equal(t, config.APIURL, bitbucket.DEFAULT_BITBUCKET_API_BASE_URL)
}

func TestGetTektonDir(t *testing.T) {
	tests := []struct {
		name                 string
		event                *info.Event
		testDirPath          string
		contentContains      string
		wantErr              string
		removeSuffix         bool
		provenance           string
		filterMessageSnippet string
	}{
		{
			name:                 "Get Tekton Directory on pull request",
			event:                bbcloudtest.MakeEvent(&info.Event{TriggerTarget: triggertype.PullRequest}),
			testDirPath:          "../../pipelineascode/testdata/pull_request/.tekton",
			contentContains:      "kind: PipelineRun",
			filterMessageSnippet: "Using PipelineRun definition from source pull_request commit SHA",
		},
		{
			name:                 "Get Tekton Directory on push",
			event:                bbcloudtest.MakeEvent(&info.Event{TriggerTarget: triggertype.Push}),
			testDirPath:          "../../pipelineascode/testdata/pull_request/.tekton",
			contentContains:      "kind: PipelineRun",
			filterMessageSnippet: "Using PipelineRun definition from source push commit SHA",
		},
		{
			name:                 "Get Tekton Directory Mainbranch",
			event:                bbcloudtest.MakeEvent(&info.Event{DefaultBranch: "main"}),
			testDirPath:          "../../pipelineascode/testdata/pull_request/.tekton",
			contentContains:      "kind: PipelineRun",
			provenance:           "default_branch",
			filterMessageSnippet: "Using PipelineRun definition from default_branch: main",
		},
		{
			name:            "Get Tekton Directory and subdirectory",
			event:           bbcloudtest.MakeEvent(nil),
			testDirPath:     "../../pipelineascode/testdata/subdir/.tekton",
			contentContains: "kind: PipelineRun",
		},
		{
			name:        "Bad yaml files in there",
			event:       bbcloudtest.MakeEvent(nil),
			testDirPath: "../../pipelineascode/testdata/bad_yaml/.tekton",
			wantErr:     "error unmarshalling yaml file .tekton/badyaml.yaml: yaml: line 2: did not find expected key",
		},
		{
			name:            "No yaml files in there",
			event:           bbcloudtest.MakeEvent(nil),
			testDirPath:     "../../pipelineascode/testdata/no_yaml/.tekton",
			contentContains: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, exporter := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
			defer tearDown()
			v := &Provider{Logger: fakelogger, bbClient: bbclient}
			bbcloudtest.MuxDirContent(t, mux, tt.event, tt.testDirPath, tt.provenance)
			content, err := v.GetTektonDir(ctx, tt.event, ".tekton", tt.provenance)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error %s, got %v", tt.wantErr, err)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			if tt.contentContains == "" {
				assert.Equal(t, content, "")
				return
			}
			assert.Assert(t, strings.Contains(content, tt.contentContains), "content %s doesn't have %s", content, tt.contentContains)

			if tt.filterMessageSnippet != "" {
				gotcha := exporter.FilterMessageSnippet(tt.filterMessageSnippet)
				assert.Assert(t, gotcha.Len() > 0, "expected to find %s in logs, found %v", tt.filterMessageSnippet, exporter.All())
			}
		})
	}
}

func TestSetClient(t *testing.T) {
	tests := []struct {
		name          string
		wantErrSubstr string
		event         *info.Event
		token         string
	}{
		{
			name: "set token",
			event: &info.Event{
				Provider: &info.Provider{
					Token: "token",
					User:  "user",
				},
			},
		},
		{
			name: "no user",
			event: &info.Event{
				Provider: &info.Provider{
					Token: "token",
					User:  "",
				},
			},
			wantErrSubstr: "no git_provider.user",
		},
		{
			name: "no token",
			event: &info.Event{
				Provider: &info.Provider{
					Token: "",
					User:  "user",
				},
			},
			wantErrSubstr: "no git_provider.secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			core, observer := zapobserver.New(zap.InfoLevel)
			testLog := zap.New(core).Sugar()

			fakeRun := &params.Run{
				Clients: clients.Clients{
					Log: testLog,
				},
			}

			v := Provider{}
			v.SetLogger(testLog)
			err := v.SetClient(ctx, fakeRun, tt.event, nil, nil)

			if tt.wantErrSubstr != "" {
				assert.ErrorContains(t, err, tt.wantErrSubstr)
				return
			}
			assert.Equal(t, tt.event.Provider.Token, *v.Token)
			assert.Equal(t, tt.event.Provider.User, *v.Username)

			logs := observer.TakeAll()
			assert.Assert(t, len(logs) > 0, "expected a log entry, got none")
			expected := fmt.Sprintf("bitbucket-cloud: initialized client with provided token for user=%s", tt.event.Provider.User)
			assert.Equal(t, expected, logs[0].Message)
		})
	}
}

func TestGetCommitInfo(t *testing.T) {
	tests := []struct {
		name       string
		event      *info.Event
		wantErr    bool
		commitinfo types.Commit
		repoinfo   *bitbucket.Repository
	}{
		{
			name:  "Get commit info with author",
			event: bbcloudtest.MakeEvent(nil),
			commitinfo: types.Commit{
				Hash: "convertedcommit",
				Links: types.Links{
					HTML: types.HTMLLink{
						HRef: "https://everywhereigo",
					},
				},
				Message: "Das Commit\n\nWith full message details",
				Author: types.Author{
					User: types.User{DisplayName: "John Doe"},
				},
			},
			repoinfo: &bitbucket.Repository{
				Mainbranch: bitbucket.RepositoryBranch{Name: "branshe"},
			},
		},
		{
			name:  "Get commit info without author",
			event: bbcloudtest.MakeEvent(nil),
			commitinfo: types.Commit{
				Hash: "convertedcommit",
				Links: types.Links{
					HTML: types.HTMLLink{
						HRef: "https://everywhereigo",
					},
				},
				Message: "Simple message",
				Author:  types.Author{},
			},
			repoinfo: &bitbucket.Repository{
				Mainbranch: bitbucket.RepositoryBranch{Name: "branshe"},
			},
		},
		{
			name: "Get commit info no SHA",
			event: bbcloudtest.MakeEvent(&info.Event{
				Organization: "org",
				Repository:   "repo",
				HeadBranch:   "hello",
				SHA:          "none",
			}),
			commitinfo: types.Commit{
				Hash: "convertedcommit",
				Links: types.Links{
					HTML: types.HTMLLink{
						HRef: "https://everywhereigo",
					},
				},
				Message: "Das Commit",
				Author:  types.Author{},
			},
			repoinfo: &bitbucket.Repository{
				Mainbranch: bitbucket.RepositoryBranch{Name: "branshe"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
			defer tearDown()
			v := &Provider{Logger: fakelogger, bbClient: bbclient}
			bbcloudtest.MuxCommit(t, mux, tt.event, tt.commitinfo)
			bbcloudtest.MuxBranch(t, mux, tt.event, tt.commitinfo)
			bbcloudtest.MuxRepoInfo(t, mux, tt.event, tt.repoinfo)

			if err := v.GetCommitInfo(ctx, tt.event); (err != nil) != tt.wantErr {
				t.Errorf("GetCommitInfo() error = %v, wantErr %v", err, tt.wantErr)
			}

			assert.Equal(t, tt.repoinfo.Mainbranch.Name, tt.event.DefaultBranch)
			assert.Equal(t, tt.commitinfo.Links.HTML.HRef, tt.event.SHAURL)
			assert.Equal(t, tt.commitinfo.Hash, tt.event.SHA)
			assert.Equal(t, tt.commitinfo.Message, tt.event.SHATitle)

			// Verify new extended commit fields
			assert.Equal(t, tt.commitinfo.Message, tt.event.SHAMessage, "SHAMessage should match")

			// Bitbucket Cloud only provides author DisplayName (no email or dates)
			if tt.commitinfo.Author.User.DisplayName != "" {
				assert.Equal(t, tt.commitinfo.Author.User.DisplayName, tt.event.SHAAuthorName, "SHAAuthorName should match")
			}
		})
	}
}

func TestCreateStatus(t *testing.T) {
	originalPipelineRunName := "hello-af9ch"
	tests := []struct {
		name                  string
		wantErr               bool
		status                status.StatusOpts
		applicationName       string
		expectedDescSubstr    string
		expectedCommentSubstr string
	}{
		{
			name: "skipped",
			status: status.StatusOpts{
				Conclusion:              "skipped",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "Skipping",
		},
		{
			name: "neutral",
			status: status.StatusOpts{
				Conclusion:              "neutral",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "stopped",
		},
		{
			name: "completed with comment",
			status: status.StatusOpts{
				Conclusion:              "success",
				Status:                  "completed",
				OriginalPipelineRunName: originalPipelineRunName,
				Text:                    "Happy as a bunny",
			},
			expectedDescSubstr:    "validated",
			expectedCommentSubstr: "Happy as a bunny",
		},
		{
			name: "failed",
			status: status.StatusOpts{
				Conclusion:              "failure",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "Failed",
		},
		{
			name: "details url",
			status: status.StatusOpts{
				Conclusion:              "failure",
				DetailsURL:              "http://fail.com",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "Failed",
		},
		{
			name: "pending",
			status: status.StatusOpts{
				Conclusion:              "pending",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "started",
		},
		{
			name: "success",
			status: status.StatusOpts{
				Conclusion:              "success",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "validated",
		},
		{
			name: "completed",
			status: status.StatusOpts{
				Conclusion:              "completed",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			expectedDescSubstr: "Completed",
		},
		{
			name: "application name",
			status: status.StatusOpts{
				Conclusion:              "completed",
				OriginalPipelineRunName: originalPipelineRunName,
			},
			applicationName:    "HELLO APP",
			expectedDescSubstr: "Completed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
			defer tearDown()

			appName := tt.applicationName
			if appName == "" {
				appName = settings.PACApplicationNameDefaultValue
			}

			v := &Provider{
				bbClient: bbclient,
				run:      params.New(),
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: appName,
					},
				},
			}
			event := bbcloudtest.MakeEvent(nil)
			event.EventType = "pull_request"
			event.Provider.Token = "token"

			bbcloudtest.MuxCreateCommitstatus(t, mux, event, tt.expectedDescSubstr, appName, tt.status)
			bbcloudtest.MuxCreateComment(t, mux, event, tt.expectedCommentSubstr)

			err := v.CreateStatus(ctx, event, tt.status)
			assert.NilError(t, err)
		})
	}
}

func TestGetCommitStatuses(t *testing.T) {
	tests := []struct {
		name         string
		noClient     bool
		statuses     []types.Status
		wantErr      bool
		wantStatuses []provider.CommitStatusInfo
	}{
		{
			name:     "nil client returns error",
			noClient: true,
			wantErr:  true,
		},
		{
			name: "statuses with mixed states",
			statuses: []types.Status{
				{Key: "Pipelines as Code CI / lint", State: "SUCCESSFUL"},
				{Key: "Pipelines as Code CI / test", State: "FAILED"},
				{Key: "Pipelines as Code CI / build", State: "INPROGRESS"},
			},
			wantStatuses: []provider.CommitStatusInfo{
				{Name: "Pipelines as Code CI / lint", Status: "successful"},
				{Name: "Pipelines as Code CI / test", Status: "failed"},
				{Name: "Pipelines as Code CI / build", Status: "inprogress"},
			},
		},
		{
			name: "deduplication removes duplicate key and status pairs",
			statuses: []types.Status{
				{Key: "Pipelines as Code CI / lint", State: "SUCCESSFUL"},
				{Key: "Pipelines as Code CI / lint", State: "SUCCESSFUL"},
			},
			wantStatuses: []provider.CommitStatusInfo{
				{Name: "Pipelines as Code CI / lint", Status: "successful"},
			},
		},
		{
			name:         "empty statuses",
			statuses:     []types.Status{},
			wantStatuses: nil,
		},
		{
			name: "raw key without prefix returned as-is",
			statuses: []types.Status{
				{Key: "my-very-long-pipeline-run-name", State: "SUCCESSFUL"},
			},
			wantStatuses: []provider.CommitStatusInfo{
				{Name: "my-very-long-pipeline-run-name", Status: "successful"},
			},
		},
		{
			name: "truncated key with hash returned as-is",
			statuses: []types.Status{
				{Key: "ccccccccccccccccccccccccccccccccc-5de6bf", State: "SUCCESSFUL"},
			},
			wantStatuses: []provider.CommitStatusInfo{
				{Name: "ccccccccccccccccccccccccccccccccc-5de6bf", Status: "successful"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			if tt.noClient {
				v := &Provider{}
				_, err := v.GetCommitStatuses(ctx, bbcloudtest.MakeEvent(nil))
				assert.Assert(t, err != nil)
				return
			}

			bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
			defer tearDown()

			event := bbcloudtest.MakeEvent(nil)
			bbcloudtest.MuxListCommitStatuses(t, mux, event, tt.statuses)

			v := &Provider{bbClient: bbclient}

			got, err := v.GetCommitStatuses(ctx, event)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, len(got), len(tt.wantStatuses))
			for i, want := range tt.wantStatuses {
				assert.Equal(t, got[i].Name, want.Name)
				assert.Equal(t, got[i].Status, want.Status)
			}
		})
	}
}
