package bitbucketdatacenter

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	bbv1test "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/test"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCheckValidPayload(t *testing.T) {
	tests := []struct {
		name          string
		wantErrString string
		payloadEvent  types.PullRequestEvent
	}{
		{
			name: "missing toRef.project",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{},
					},
				},
			},
			wantErrString: "bitbucket toRef project is nil",
		},
		{
			name: "empty toRef.project.key",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Project: &types.Project{},
						},
					},
				},
			},
			wantErrString: "bitbucket toRef project key is empty",
		},
		{
			name: "empty toRef.repositoryName",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Project: &types.Project{
								Key: "PROJ",
							},
						},
					},
				},
			},
			wantErrString: "bitbucket toRef repository name is empty",
		},
		{
			name: "missing toRef.latestCommit",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					FromRef: types.PullRequestRef{},
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
					},
				},
			},
			wantErrString: "bitbucket toRef latest commit is empty",
		},
		{
			name: "missing fromRef.project",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{},
					},
				},
			},
			wantErrString: "bitbucket fromRef project is nil",
		},
		{
			name: "empty fromRef.projectKey",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Project: &types.Project{},
						},
					},
				},
			},
			wantErrString: "bitbucket fromRef project key is empty",
		},
		{
			name: "empty fromRef.repositoryName",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Project: &types.Project{
								Key: "PROJ",
							},
						},
					},
				},
			},
			wantErrString: "bitbucket fromRef repository name is empty",
		},
		{
			name: "missing fromRef.latestCommit",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
					},
				},
			},
			wantErrString: "bitbucket fromRef latest commit is empty",
		},
		{
			name: "zero pull request ID",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
				},
			},
			wantErrString: "bitbucket pull request ID is zero",
		},
		{
			name: "missing repository links",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					ID: 1,
				},
			},
			wantErrString: "bitbucket toRef repository links are nil or empty",
		},
		{
			name: "empty toRef display ID",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					ID: 1,
				},
			},
			wantErrString: "bitbucket toRef display ID is empty",
		},
		{
			name: "empty fromRef display ID",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
								Links: types.Links{
									Self: []types.SelfLink{{Href: "http://example.com"}},
								},
							},
						},
						DisplayID:    "main",
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						LatestCommit: "abcd",
					},
					ID: 1,
				},
			},
			wantErrString: "bitbucket fromRef display ID is empty",
		},
		{
			name: "missing fromRef repository links",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
								Links: types.Links{
									Self: []types.SelfLink{{Href: "http://example.com"}},
								},
							},
						},
						DisplayID:    "main",
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
						},
						DisplayID:    "feature",
						LatestCommit: "abcd",
					},
					ID: 1,
				},
			},
			wantErrString: "bitbucket fromRef repository links are nil or empty",
		},
		{
			name: "empty toRef repository clone links",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Self: []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "repo",
							Project: &types.Project{
								Key: "PROJ",

								Links: types.Links{
									Self: []types.SelfLink{{Href: "http://example.com"}},
								},
							},
						},
						DisplayID:    "main",
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						DisplayID:    "feature",
						LatestCommit: "abcd",
						Repository: types.Repository{
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Self: []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "dest",
						},
					},
					ID: 1,
				},
				Actor: types.UserWithLinks{},
			},
			wantErrString: "bitbucket toRef repository clone links are empty",
		},
		{
			name: "empty fromRef repository clone links",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "repo",
							Project: &types.Project{
								Key: "PROJ",

								Links: types.Links{
									Self: []types.SelfLink{{Href: "http://example.com"}},
								},
							},
						},
						DisplayID:    "main",
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						DisplayID:    "feature",
						LatestCommit: "abcd",
						Repository: types.Repository{
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Self: []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "dest",
						},
					},
					ID: 1,
				},
				Actor: types.UserWithLinks{},
			},
			wantErrString: "bitbucket fromRef repository clone links are empty",
		},
		{
			name: "zero actor ID",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "repo",
							Project: &types.Project{
								Key: "PROJ",

								Links: types.Links{
									Self: []types.SelfLink{{Href: "http://example.com"}},
								},
							},
						},
						DisplayID:    "main",
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						DisplayID:    "feature",
						LatestCommit: "abcd",
						Repository: types.Repository{
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "dest",
						},
					},
					ID: 1,
				},
				Actor: types.UserWithLinks{},
			},
			wantErrString: "bitbucket actor ID is zero",
		},
		{
			name: "empty actor name",
			payloadEvent: types.PullRequestEvent{
				PullRequest: types.PullRequest{
					ToRef: types.PullRequestRef{
						Repository: types.Repository{
							Name: "repo",
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
								Links: types.Links{
									Self: []types.SelfLink{{Href: "http://example.com"}},
								},
							},
						},
						DisplayID:    "main",
						LatestCommit: "abcd",
					},
					FromRef: types.PullRequestRef{
						DisplayID:    "feature",
						LatestCommit: "abcd",
						Repository: types.Repository{
							Project: &types.Project{
								Key:  "PROJ",
								Name: "repo",
							},
							Links: &struct {
								Clone []types.CloneLink `json:"clone,omitempty"`
								Self  []types.SelfLink  `json:"self,omitempty"`
							}{
								Clone: []types.CloneLink{{Href: "http://example.com"}},
								Self:  []types.SelfLink{{Href: "http://example.com"}},
							},
							Name: "dest",
						},
					},
					ID: 1,
				},
				Actor: types.UserWithLinks{
					ID: 1,
				},
			},
			wantErrString: "bitbucket actor name is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkValidPayload(&tt.payloadEvent)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestParsePayload(t *testing.T) {
	ev1 := &info.Event{
		AccountID:    "12345",
		Sender:       "sender",
		Organization: "PROJ",
		Repository:   "repo",
		URL:          "http://forge/PROJ/repo/browse",
		SHA:          "abcd",
		CloneURL:     "http://clone/PROJ/repo",
	}

	tests := []struct {
		name                    string
		payloadEvent            any
		expEvent                *info.Event
		eventType               string
		wantErrSubstr           string
		rawStr                  string
		targetPipelinerun       string
		canceltargetPipelinerun string
	}{
		{
			name:          "bad/invalid event type",
			eventType:     "pr:nono",
			payloadEvent:  types.PullRequest{},
			wantErrSubstr: "event \"pr:nono\" is not supported",
		},
		{
			name:          "bad/bad json",
			eventType:     "pr:opened",
			payloadEvent:  types.PullRequest{},
			rawStr:        "rageAgainst",
			wantErrSubstr: "invalid character",
		},
		{
			name:      "bad/url",
			eventType: "pr:opened",
			payloadEvent: bbv1test.MakePREvent(
				&info.Event{
					AccountID:    "12345",
					Sender:       "sender",
					Organization: "PROJ",
					Repository:   "repo",
					URL:          "\x03💢\x16",
					SHA:          "abcd",
				}, "",
			),
			wantErrSubstr: "invalid control character",
		},
		{
			name:         "good/pull_request",
			eventType:    "pr:opened",
			payloadEvent: bbv1test.MakePREvent(ev1, ""),
			expEvent:     ev1,
		},
		{
			name:         "good/push",
			eventType:    "repo:refs_changed",
			payloadEvent: bbv1test.MakePushEvent(ev1, []types.PushRequestEventChange{{ToHash: ev1.SHA, RefID: "base"}}, []types.Commit{{ID: ev1.SHA}}),
			expEvent:     ev1,
		},
		{
			name:          "bad/changes are empty in push",
			eventType:     "repo:refs_changed",
			payloadEvent:  bbv1test.MakePushEvent(ev1, []types.PushRequestEventChange{}, []types.Commit{}),
			expEvent:      ev1,
			wantErrSubstr: "push event contains no commits under 'changes'; cannot proceed",
		},
		{
			name:          "bad/commits are empty in push",
			eventType:     "repo:refs_changed",
			payloadEvent:  bbv1test.MakePushEvent(ev1, []types.PushRequestEventChange{{ToHash: ev1.SHA, RefID: "base"}}, []types.Commit{}),
			expEvent:      ev1,
			wantErrSubstr: "push event contains no commits; cannot proceed",
		},
		{
			name:         "good/comment ok-to-test",
			eventType:    "pr:comment:added",
			payloadEvent: bbv1test.MakePREvent(ev1, "/ok-to-test"),
			expEvent:     ev1,
		},
		{
			name:         "good/comment test",
			eventType:    "pr:comment:added",
			payloadEvent: bbv1test.MakePREvent(ev1, "/test"),
			expEvent:     ev1,
		},
		{
			name:              "good/comment retest a pr",
			eventType:         "pr:comment:added",
			payloadEvent:      bbv1test.MakePREvent(ev1, "/retest dummy"),
			expEvent:          ev1,
			targetPipelinerun: "dummy",
		},
		{
			name:                    "good/comment cancel a pr",
			eventType:               "pr:comment:added",
			payloadEvent:            bbv1test.MakePREvent(ev1, "/cancel dummy"),
			expEvent:                ev1,
			canceltargetPipelinerun: "dummy",
		},
		{
			name:         "good/comment cancel all",
			eventType:    "pr:comment:added",
			payloadEvent: bbv1test.MakePREvent(ev1, "/cancel"),
			expEvent:     ev1,
		},
		{
			name:      "branch/deleted with zero hash",
			eventType: "repo:refs_changed",
			payloadEvent: bbv1test.MakePushEvent(
				ev1, []types.PushRequestEventChange{
					{
						ToHash: "0000000000000000000000000000000000000000",
						RefID:  "refs/heads/feature-branch",
						Type:   "DELETE",
					},
				}, []types.Commit{},
			),
			expEvent:      ev1,
			wantErrSubstr: "branch delete event is not supported; cannot proceed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			v := &Provider{}

			req := &http.Request{Header: map[string][]string{}}
			req.Header.Set("X-Event-Key", tt.eventType)

			run := &params.Run{
				Info: info.Info{},
			}
			_b, err := json.Marshal(tt.payloadEvent)
			assert.NilError(t, err)
			payload := string(_b)
			if tt.rawStr != "" {
				payload = tt.rawStr
			}

			got, err := v.ParsePayload(ctx, run, req, payload)
			if tt.wantErrSubstr != "" {
				assert.ErrorContains(t, err, tt.wantErrSubstr)
				return
			}
			assert.NilError(t, err)
			// Handle case where expEvent is nil (e.g., branch deletion)
			if tt.expEvent == nil {
				if got != nil {
					t.Fatalf("expected event to be nil, got: %+v", got)
				}
				return
			}
			// assert SHA ID
			assert.Equal(t, got.AccountID, tt.expEvent.AccountID)

			// test that we got slashed
			assert.Equal(t, got.URL+"/browse", tt.expEvent.URL)

			assert.Equal(t, got.CloneURL, tt.expEvent.CloneURL)

			if tt.targetPipelinerun != "" {
				assert.Equal(t, got.TargetTestPipelineRun, tt.targetPipelinerun)
			}
			if tt.canceltargetPipelinerun != "" {
				assert.Equal(t, got.TargetCancelPipelineRun, tt.canceltargetPipelinerun)
			}
		})
	}
}
