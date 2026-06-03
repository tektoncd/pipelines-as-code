package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-github/v81/github"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
)

// script kiddies, don't get too excited, this has been randomly generated with random words.
const fakePrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQC6GorZBeri0eVERMZQDFh5E1RMPjFk9AevaWr27yJse6eiUlos
gY2L2vcZKLOrdvVR+TLLapIMFfg1E1qVr1iTHP3IiSCs1uW6NKDmxEQc9Uf/fG9c
i56tGmTVxLkC94AvlVFmgxtWfHdP3lF2O0EcfRyIi6EIbGkWDqWQVEQG2wIDAQAB
AoGAaKOd6FK0dB5Si6Uj4ERgxosAvfHGMh4n6BAc7YUd1ONeKR2myBl77eQLRaEm
DMXRP+sfDVL5lUQRED62ky1JXlDc0TmdLiO+2YVyXI5Tbej0Q6wGVC25/HedguUX
fw+MdKe8jsOOXVRLrJ2GfpKZ2CmOKGTm/hyrFa10TmeoTxkCQQDa4fvqZYD4vOwZ
CplONnVk+PyQETj+mAyUiBnHEeLpztMImNLVwZbrmMHnBtCNx5We10oCLW+Qndfw
Xi4LgliVAkEA2amSV+TZiUVQmm5j9yzon0rt1FK+cmVWfRS/JAUXyvl+Xh/J+7Gu
QzoEGJNAnzkUIZuwhTfNRWlzURWYA8BVrwJAZFQhfJd6PomaTwAktU0REm9ulTrP
vSNE4PBhoHX6ZOGAqfgi7AgIfYVPm+3rupE5a82TBtx8vvUa/fqtcGkW4QJAaL9t
WPUeJyx/XMJxQzuOe1JA4CQt2LmiBLHeRoRY7ephgQSFXKYmed3KqNT8jWOXp5DY
Q1QWaigUQdpFfNCrqwJBANLgWaJV722PhQXOCmR+INvZ7ksIhJVcq/x1l2BYOLw2
QsncVExbMiPa9Oclo5qLuTosS8qwHm1MJEytp3/SkB8=
-----END RSA PRIVATE KEY-----`

var sampleRepo = &github.Repository{
	Owner: &github.User{
		Login: github.Ptr("owner"),
	},
	Name:          github.Ptr("reponame"),
	DefaultBranch: github.Ptr("main"),
	HTMLURL:       github.Ptr("https://github.com/owner/repo"),
}

var testInstallationID = int64(1)

var samplePRevent = github.PullRequestEvent{
	PullRequest: &github.PullRequest{
		Head: &github.PullRequestBranch{
			SHA: github.Ptr("sampleHeadsha"),
			Ref: github.Ptr("headred"),
		},
		Base: &github.PullRequestBranch{
			SHA: github.Ptr("basesha"),
			Ref: github.Ptr("baseref"),
		},
		User: &github.User{
			Login: github.Ptr("user"),
		},
		Title: github.Ptr("my first PR"),
	},
	Repo: sampleRepo,
}

func githubSHA256Signature(secret string, payload []byte) string {
	hm := hmac.New(sha256.New, []byte(secret))
	hm.Write(payload)
	return "sha256=" + hex.EncodeToString(hm.Sum(nil))
}

var samplePR = github.PullRequest{
	Number: github.Ptr(54321),
	Head: &github.PullRequestBranch{
		SHA:  github.Ptr("samplePRsha"),
		Repo: sampleRepo,
	},
}

var samplePRAnother = github.PullRequest{
	Number: github.Ptr(54321),
	Head: &github.PullRequestBranch{
		SHA:  github.Ptr("samplePRshanew"),
		Repo: sampleRepo,
	},
}

func TestGetPullRequestsWithCommit(t *testing.T) {
	apiHitCount := 0
	tests := []struct {
		name          string
		sha           string
		org           string
		repo          string
		hasClient     bool
		mockAPIs      map[string]func(rw http.ResponseWriter, r *http.Request)
		wantPRsCount  int
		isMergeCommit bool
		wantErr       bool
	}{
		{
			name:      "nil client returns error",
			sha:       "abc123",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: false,
			wantErr:   true,
		},
		{
			name:      "empty sha returns error",
			sha:       "",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			wantErr:   true,
		},
		{
			name:      "empty org returns error",
			sha:       "abc123",
			org:       "",
			repo:      "testrepo",
			hasClient: true,
			wantErr:   true,
		},
		{
			name:      "empty repo returns error",
			sha:       "abc123",
			org:       "testorg",
			repo:      "",
			hasClient: true,
			wantErr:   true,
		},
		{
			name:      "api error unauthorized",
			sha:       "abc123",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testorg/testrepo/commits/abc123/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, http.StatusUnauthorized)
					fmt.Fprint(rw, `{"message": "Unauthorized"}`)
				},
			},
			wantErr: true,
		},
		{
			name:      "commit is part of one PR only",
			sha:       "abc123",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testorg/testrepo/commits/abc123/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"number": 42, "state": "closed"}]`)
				},
			},
			wantPRsCount: 1,
			wantErr:      false,
		},
		{
			name:      "commit is not part of any PR",
			sha:       "xyz789",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testorg/testrepo/commits/xyz789/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[]`)
				},
			},
			wantErr: false,
		},
		{
			name:      "commit is included in multiple PRs",
			sha:       "abc123",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testorg/testrepo/commits/abc123/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"number": 41, "state": "closed"}, {"number": 42, "state": "open"}]`)
				},
			},
			wantPRsCount: 2,
			wantErr:      false,
		},
		{
			name:      "commit is included in multiple PRs with pagination",
			sha:       "abc123",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testorg/testrepo/commits/abc123/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					page := r.URL.Query().Get("page")
					switch page {
					case "", "1":
						// First page with closed PRs
						rw.Header().Set("Link", `<https://api.github.com/repos/testorg/testrepo/commits/abc123/pulls?page=2>; rel="next"`)
						fmt.Fprint(rw, `[{"number": 41, "state": "closed"}]`)
					case "2":
						// Second page with open PR
						fmt.Fprint(rw, `[{"number": 42, "state": "open"}]`)
					}
				},
			},
			wantPRsCount: 2,
			wantErr:      false,
		},
		{
			name:      "commit is part of one PR and is a merge commit",
			sha:       "abc123",
			org:       "testorg",
			repo:      "testrepo",
			hasClient: true,
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testorg/testrepo/commits/abc123/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					apiHitCount++
					if apiHitCount == 3 {
						fmt.Fprint(rw, `[{"number": 42, "state": "closed"}]`)
					} else {
						fmt.Fprint(rw, `[]`)
					}
				},
			},
			wantPRsCount:  1,
			isMergeCommit: true,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			var provider *Provider
			if tt.hasClient {
				fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
				defer teardown()

				// Register API endpoints
				for pattern, handler := range tt.mockAPIs {
					mux.HandleFunc(pattern, handler)
				}

				logger, _ := logger.GetLogger()
				provider = &Provider{
					ghClient: fakeclient,
					Logger:   logger,
				}
			} else {
				logger, _ := logger.GetLogger()
				provider = &Provider{
					Logger: logger,
				}
			}

			prs, err := provider.getPullRequestsWithCommit(ctx, tt.sha, tt.org, tt.repo, tt.isMergeCommit)
			assert.Equal(t, err != nil, tt.wantErr)
			assert.Equal(t, len(prs), tt.wantPRsCount)

			if tt.isMergeCommit {
				assert.Equal(t, apiHitCount, 3)
			}

			if tt.wantErr && err != nil {
				// Verify error messages for validation cases
				switch {
				case tt.sha == "":
					assert.ErrorContains(t, err, "sha cannot be empty")
				case tt.org == "":
					assert.ErrorContains(t, err, "organization cannot be empty")
				case tt.repo == "":
					assert.ErrorContains(t, err, "repository cannot be empty")
				case !tt.hasClient:
					assert.ErrorContains(t, err, "github client is not initialized")
				}
			}
		})
	}
}

func TestIsCommitPartOfPullRequest(t *testing.T) {
	tests := []struct {
		name      string
		sha       string
		org       string
		repo      string
		prs       []*github.PullRequest
		wantFound bool
		wantPRNum int
	}{
		{
			name: "commit is part of an open PR",
			sha:  "abc123",
			org:  "testorg",
			repo: "testrepo",
			prs: []*github.PullRequest{
				{
					Number: github.Ptr(42),
					State:  github.Ptr("open"),
				},
			},
			wantFound: true,
			wantPRNum: 42,
		},
		{
			name: "commit is part of closed PR only",
			sha:  "abc123",
			org:  "testorg",
			repo: "testrepo",
			prs: []*github.PullRequest{
				{
					Number: github.Ptr(42),
					State:  github.Ptr("closed"),
				},
			},
			wantFound: false,
			wantPRNum: 0,
		},
		{
			name:      "commit is not part of any PR",
			sha:       "xyz789",
			org:       "testorg",
			repo:      "testrepo",
			prs:       []*github.PullRequest{},
			wantFound: false,
			wantPRNum: 0,
		},
		{
			name: "multiple PRs but only one is open",
			sha:  "abc123",
			org:  "testorg",
			repo: "testrepo",
			prs: []*github.PullRequest{
				{
					Number: github.Ptr(41),
					State:  github.Ptr("closed"),
				},
				{
					Number: github.Ptr(42),
					State:  github.Ptr("open"),
				},
				{
					Number: github.Ptr(43),
					State:  github.Ptr("closed"),
				},
			},
			wantFound: true,
			wantPRNum: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var provider *Provider
			logger, _ := logger.GetLogger()
			provider = &Provider{
				Logger: logger,
			}

			found, prNum := provider.isCommitPartOfPullRequest(tt.sha, tt.org, tt.repo, tt.prs)

			// Verify results
			assert.Equal(t, found, tt.wantFound)
			assert.Equal(t, prNum, tt.wantPRNum)
		})
	}
}

func TestParsePayLoad(t *testing.T) {
	samplePRNoRepo := samplePRevent
	samplePRNoRepo.Repo = nil
	samplePrEventClosed := samplePRevent
	samplePrEventClosed.Action = github.Ptr("closed")

	sampleGhPRs := []*github.PullRequest{
		{
			Number: github.Ptr(41),
			State:  github.Ptr("closed"),
		},
		{
			Number: github.Ptr(42),
			State:  github.Ptr("open"),
		},
	}

	tests := []struct {
		name                       string
		wantErrString              string
		eventType                  string
		payloadEventStruct         any
		jeez                       string
		triggerTarget              string
		githubClient               bool
		muxReplies                 map[string]any
		shaRet                     string
		targetPipelinerun          string
		targetCancelPipelinerun    string
		wantedBranchName           string
		wantedTagName              string
		wantedPullRequestNumber    int
		isCancelPipelineRunEnabled bool
		isMergeCommit              bool
		skipPushEventForPRCommits  bool
		objectType                 string
	}{
		{
			name:          "bad/unknown event",
			wantErrString: "unknown X-Github-Event",
			eventType:     "unknown",
			triggerTarget: "unknown",
		},
		{
			name:          "bad/invalid json",
			wantErrString: "invalid character",
			eventType:     "pull_request",
			triggerTarget: "unknown",
			jeez:          "xxxx",
		},
		{
			name:               "bad/not supported",
			wantErrString:      "this event is not supported",
			eventType:          "pull_request_review_comment",
			triggerTarget:      "pull_request",
			payloadEventStruct: github.PullRequestReviewCommentEvent{Action: github.Ptr("created")},
		},
		{
			name:               "bad/check run only issue recheck supported",
			wantErrString:      "only issue recheck is supported",
			eventType:          "check_run",
			triggerTarget:      "nonopetitrobot",
			payloadEventStruct: github.CheckRunEvent{Action: github.Ptr("created")},
			githubClient:       true,
		},
		{
			name:               "bad/check run only with github apps",
			wantErrString:      "only supported with github apps",
			eventType:          "check_run",
			triggerTarget:      "pull_request",
			payloadEventStruct: github.CheckRunEvent{Action: github.Ptr("created")},
		},
		{
			name:               "bad/issue comment retest only with github apps",
			wantErrString:      "no github client has been initialized",
			eventType:          "issue_comment",
			triggerTarget:      "pull_request",
			payloadEventStruct: github.IssueCommentEvent{Action: github.Ptr("created")},
		},
		{
			name:               "bad/issue comment not coming from pull request",
			eventType:          "issue_comment",
			triggerTarget:      "pull_request",
			githubClient:       true,
			payloadEventStruct: github.IssueCommentEvent{Action: github.Ptr("created"), Issue: &github.Issue{}},
			wantErrString:      "issue comment is not coming from a pull_request",
		},
		{
			name:          "bad/issue comment invalid pullrequest",
			eventType:     "issue_comment",
			triggerTarget: "pull_request",
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.Ptr("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.Ptr("/bad"),
					},
				},
			},
			wantErrString: "bad pull request number",
		},
		{
			name:          "bad/rerequest error fetching PR",
			githubClient:  true,
			eventType:     "check_run",
			triggerTarget: "pull_request",
			wantErrString: "404",
			payloadEventStruct: github.CheckRunEvent{
				Action: github.Ptr("rerequested"),
				Repo:   sampleRepo,
				CheckRun: &github.CheckRun{
					CheckSuite: &github.CheckSuite{
						PullRequests: []*github.PullRequest{&samplePR},
					},
				},
			},
			shaRet: "samplePRsha",
		},
		{
			name:               "bad/pull request",
			eventType:          "pull_request",
			triggerTarget:      triggertype.PullRequest.String(),
			payloadEventStruct: samplePRNoRepo,
			wantErrString:      "error parsing payload the repository should not be nil",
		},
		{
			name:               "bad/push",
			eventType:          "push",
			triggerTarget:      triggertype.Push.String(),
			payloadEventStruct: github.PushEvent{},
			wantErrString:      "error parsing payload the repository should not be nil",
		},
		{
			name:          "branch/deleted",
			eventType:     "push",
			triggerTarget: triggertype.Push.String(),
			payloadEventStruct: github.PushEvent{
				Repo: &github.PushEventRepository{
					Owner: &github.User{Login: github.Ptr("foo")},
					Name:  github.Ptr("pushRepo"),
				},
				Ref:   github.Ptr("test"),
				After: github.Ptr("0000000000000000000000000000000000000000"),
			},
			wantErrString: "branch test has been deleted, exiting",
		},
		{
			// specific run from a check_suite
			name:          "good/rerequest check_run on pull request",
			eventType:     "check_run",
			githubClient:  true,
			triggerTarget: string(triggertype.PullRequest),
			payloadEventStruct: github.CheckRunEvent{
				Action: github.Ptr("rerequested"),
				Repo:   sampleRepo,
				CheckRun: &github.CheckRun{
					CheckSuite: &github.CheckSuite{
						PullRequests: []*github.PullRequest{&samplePR},
					},
				},
			},
			muxReplies: map[string]any{"/repos/owner/reponame/pulls/54321": samplePR},
			shaRet:     "samplePRsha",
		},
		// all checks in a check_suite
		{
			name:          "good/rerequest check_suite on pull request",
			eventType:     "check_suite",
			githubClient:  true,
			triggerTarget: string(triggertype.PullRequest),
			payloadEventStruct: github.CheckSuiteEvent{
				Action: github.Ptr("rerequested"),
				Repo:   sampleRepo,
				CheckSuite: &github.CheckSuite{
					PullRequests: []*github.PullRequest{&samplePR},
				},
			},
			muxReplies: map[string]any{"/repos/owner/reponame/pulls/54321": samplePR},
			shaRet:     "samplePRsha",
		},
		{
			name:         "good/rerequest on push",
			eventType:    "check_run",
			githubClient: true,
			payloadEventStruct: github.CheckRunEvent{
				Action: github.Ptr("rerequested"),
				Repo:   sampleRepo,
				CheckRun: &github.CheckRun{
					CheckSuite: &github.CheckSuite{
						HeadSHA: github.Ptr("headSHACheckSuite"),
					},
				},
			},
			shaRet: "headSHACheckSuite",
		},
		{
			name:               "bad/issue_comment_not_from_created",
			wantErrString:      "only newly created comment is supported, received: deleted",
			payloadEventStruct: github.IssueCommentEvent{Action: github.Ptr("deleted")},
			eventType:          "issue_comment",
			triggerTarget:      "pull_request",
			githubClient:       true,
		},
		{
			name:          "good/issue comment",
			eventType:     "issue_comment",
			triggerTarget: "pull_request",
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.Ptr("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.Ptr("/666"),
					},
				},
				Repo: sampleRepo,
			},
			muxReplies: map[string]any{"/repos/owner/reponame/pulls/666": samplePR},
			shaRet:     "samplePRsha",
		},
		{
			name:               "good/pull request",
			eventType:          "pull_request",
			triggerTarget:      triggertype.PullRequest.String(),
			payloadEventStruct: samplePRevent,
			shaRet:             "sampleHeadsha",
		},
		{
			name:               "good/pull request closed",
			eventType:          "pull_request",
			triggerTarget:      triggertype.PullRequestClosed.String(),
			payloadEventStruct: samplePrEventClosed,
			shaRet:             "sampleHeadsha",
		},
		{
			name:          "good/push",
			eventType:     "push",
			triggerTarget: "push",
			payloadEventStruct: github.PushEvent{
				Repo: &github.PushEventRepository{
					Owner: &github.User{Login: github.Ptr("owner")},
					Name:  github.Ptr("pushRepo"),
				},
				HeadCommit: &github.HeadCommit{ID: github.Ptr("SHAPush")},
			},
			shaRet: "SHAPush",
		},
		{
			name:          "good/issue comment for retest",
			eventType:     "issue_comment",
			triggerTarget: triggertype.PullRequest.String(),
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.Ptr("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.Ptr("/777"),
					},
				},
				Repo: sampleRepo,
				Comment: &github.IssueComment{
					Body: github.Ptr("/retest dummy"),
				},
			},
			muxReplies:        map[string]any{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
		},
		{
			name:          "good/issue comment for cancel all",
			eventType:     "issue_comment",
			triggerTarget: triggertype.PullRequest.String(),
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.Ptr("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.Ptr("/999"),
					},
				},
				Repo: sampleRepo,
				Comment: &github.IssueComment{
					Body: github.Ptr("/cancel"),
				},
			},
			muxReplies: map[string]any{"/repos/owner/reponame/pulls/999": samplePR},
			shaRet:     "samplePRsha",
		},
		{
			name:          "good/issue comment for cancel a pr",
			eventType:     "issue_comment",
			triggerTarget: triggertype.PullRequest.String(),
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.Ptr("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.Ptr("/888"),
					},
				},
				Repo: sampleRepo,
				Comment: &github.IssueComment{
					Body: github.Ptr("/cancel dummy"),
				},
			},
			muxReplies:              map[string]any{"/repos/owner/reponame/pulls/888": samplePR},
			shaRet:                  "samplePRsha",
			targetCancelPipelinerun: "dummy",
		},
		{
			name:               "bad/commit comment retest only with github apps",
			wantErrString:      "no github client has been initialized",
			eventType:          "commit_comment",
			triggerTarget:      "push",
			payloadEventStruct: github.CommitCommentEvent{Action: github.Ptr("created")},
		},
		{
			name:               "bad/commit comment for event has no repository reference",
			wantErrString:      "error parsing payload the repository should not be nil",
			eventType:          "commit_comment",
			triggerTarget:      "push",
			githubClient:       true,
			payloadEventStruct: github.CommitCommentEvent{},
		},
		{
			name:          "bad/commit comment for /test command does not contain branch keyword",
			wantErrString: "the GitOps comment `/test dummy rbanch:test` does not contain a branch or tag word",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/test dummy rbanch:test"), // rbanch is wrong word for branch 🙂
				},
			},
			muxReplies:        map[string]any{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			wantedBranchName:  "main",
		},
		{
			name:          "good/commit comment for retest a pr",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/retest dummy"),
				},
			},
			muxReplies:        map[string]any{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			wantedBranchName:  "main",
		},
		{
			name:          "good/commit comment for retest all",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/retest"),
				},
			},
			muxReplies:       map[string]any{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:           "samplePRsha",
			wantedBranchName: "main",
		},
		{
			name:          "good/commit comment for test with tag",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/test tag:v1.0.0"),
				},
			},
			shaRet:           "samplePRsha",
			objectType:       "tag",
			wantedBranchName: "refs/tags/v1.0.0",
		},
		{
			name:          "good/commit comment for test with pipelinerun name and tag",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/test dummy tag:v1.0.0"),
				},
			},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			objectType:        "tag",
			wantedBranchName:  "refs/tags/v1.0.0",
		},
		{
			name:          "good/commit comment for test tag with object type commit",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/test dummy tag:v1.0.0"),
				},
			},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			objectType:        "commit",
			wantedBranchName:  "refs/tags/v1.0.0",
		},
		{
			name:          "bad/commit comment for test tag invalid object type",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/test dummy tag:v1.0.0"),
				},
			},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			wantedBranchName:  "refs/tags/v1.0.0",
			objectType:        "blob",
			wantErrString:     "invalid object type for tag v1.0.0: blob",
		},
		{
			name:          "bad/commit comment for test with pipelinerun name and wrong tag keyword",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/test dummy taig:v1.0.0"),
				},
			},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			wantedBranchName:  "refs/tags/v1.0.0",
			wantErrString:     "the GitOps comment `/test dummy taig:v1.0.0` does not contain a branch or tag word",
		},
		{
			name:          "good/commit comment for cancel all",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/999"),
					Body:     github.Ptr("/cancel"),
				},
			},
			muxReplies:                 map[string]any{"/repos/owner/reponame/pulls/999": samplePR},
			shaRet:                     "samplePRsha",
			wantedBranchName:           "main",
			isCancelPipelineRunEnabled: true,
		},
		{
			name:          "good/commit comment for cancel a pr",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/888"),
					Body:     github.Ptr("/cancel dummy"),
				},
			},
			muxReplies:                 map[string]any{"/repos/owner/reponame/pulls/888": samplePR},
			shaRet:                     "samplePRsha",
			targetCancelPipelinerun:    "dummy",
			wantedBranchName:           "main",
			isCancelPipelineRunEnabled: true,
		},
		{
			name:          "good/commit comment for retest with branch name",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/retest dummy branch:test1"),
				},
			},
			muxReplies:                 map[string]any{"/repos/owner/reponame/pulls/7771": samplePR},
			shaRet:                     "samplePRsha",
			targetPipelinerun:          "dummy",
			wantedBranchName:           "test1",
			isCancelPipelineRunEnabled: false,
		},
		{
			name:          "good/commit comment for cancel all with branch name",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/999"),
					Body:     github.Ptr("/cancel branch:test1"),
				},
			},
			muxReplies:                 map[string]any{"/repos/owner/reponame/pulls/9991": samplePR},
			shaRet:                     "samplePRsha",
			wantedBranchName:           "test1",
			isCancelPipelineRunEnabled: true,
		},
		{
			name:          "good/commit comment for cancel a pr with branch name",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/888"),
					Body:     github.Ptr("/cancel dummy branch:test1"),
				},
			},
			muxReplies:                 map[string]any{"/repos/owner/reponame/pulls/8881": samplePR},
			shaRet:                     "samplePRsha",
			targetCancelPipelinerun:    "dummy",
			wantedBranchName:           "test1",
			isCancelPipelineRunEnabled: true,
		},
		{
			name:          "good/commit comment want pull request number",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/888"),
					Body:     github.Ptr("/retest dummy"),
				},
			},
			muxReplies: map[string]any{
				"/repos/owner/reponame/pulls/8881":                samplePR,
				"/repos/owner/reponame/commits/samplePRsha/pulls": []*github.PullRequest{&samplePR},
			},
			shaRet:                  "samplePRsha",
			targetPipelinerun:       "dummy",
			wantedBranchName:        "main",
			wantedPullRequestNumber: 54321,
		},
		{
			name:          "bad/commit comment for cancel a pr with invalid branch name",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/888"),
					Body:     github.Ptr("/cancel dummy branch:test2"),
				},
			},
			muxReplies:                 map[string]any{"/repos/owner/reponame/pulls/8881": samplePR},
			shaRet:                     "samplePRsha",
			targetCancelPipelinerun:    "dummy",
			wantedBranchName:           "test2",
			isCancelPipelineRunEnabled: false,
			wantErrString:              "404 Not Found",
		},
		{
			name:          "commit comment to retest a pr with a SHA is not HEAD commit of the main branch",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRshanew"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/retest dummy"),
				},
			},
			muxReplies:        map[string]any{"/repos/owner/reponame/pulls/777": samplePRAnother},
			shaRet:            "samplePRshanew",
			targetPipelinerun: "dummy",
			wantedBranchName:  "main",
			wantErrString:     "provided SHA samplePRshanew is not the HEAD commit of the branch main",
		},
		{
			name:          "commit comment to retest a pr with a merge commit",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.Ptr("samplePRsha"),
					HTMLURL:  github.Ptr("/777"),
					Body:     github.Ptr("/retest dummy"),
				},
			},
			muxReplies:        map[string]any{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
			wantedBranchName:  "main",
			isMergeCommit:     true,
		},
		{
			name:          "good/skip push event for skip-pr-commits setting",
			eventType:     "push",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.PushEvent{
				Ref: github.Ptr("refs/heads/main"),
				Repo: &github.PushEventRepository{
					Owner: &github.User{Login: github.Ptr("owner")},
					Name:  github.Ptr("pushRepo"),
				},
				HeadCommit: &github.HeadCommit{ID: github.Ptr("SHAPush")},
			},
			shaRet:                    "",
			skipPushEventForPRCommits: true,
			muxReplies:                map[string]any{"/repos/owner/pushRepo/commits/SHAPush/pulls": sampleGhPRs},
			wantErrString:             "",
		},
		{
			name:          "good/skip tag push event for skip-pr-commits setting",
			eventType:     "push",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.PushEvent{
				Ref: github.Ptr("refs/tags/v1.0.0"),
				Repo: &github.PushEventRepository{
					Owner: &github.User{Login: github.Ptr("owner")},
					Name:  github.Ptr("pushRepo"),
				},
				HeadCommit: &github.HeadCommit{ID: github.Ptr("SHAPush")},
			},
			shaRet:                    "SHAPush",
			skipPushEventForPRCommits: true,
			muxReplies:                map[string]any{"/repos/owner/pushRepo/commits/SHAPush/pulls": sampleGhPRs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ghClient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			if !tt.githubClient {
				ghClient = nil
			}

			for key, value := range tt.muxReplies {
				mux.HandleFunc(key, func(rw http.ResponseWriter, _ *http.Request) {
					bjeez, _ := json.Marshal(value)
					fmt.Fprint(rw, string(bjeez))
				})
			}
			if tt.eventType == "commit_comment" {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/branches/test1",
					"owner", "reponame"), func(rw http.ResponseWriter, _ *http.Request) {
					_, err := fmt.Fprintf(rw, `{
			"name": "test1",
			"commit": {
				"sha": "samplePRsha"
			}
		}`)
					assert.NilError(t, err)
				})
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/branches/testnew",
					"owner", "reponame"), func(rw http.ResponseWriter, _ *http.Request) {
					_, err := fmt.Fprintf(rw, `{
			"name": "testnew",
			"commit": {
				"sha": "samplePRshanew"
			}
		}`)
					assert.NilError(t, err)
				})
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/branches/main", "owner", "reponame"), func(rw http.ResponseWriter, _ *http.Request) {
					_, err := fmt.Fprintf(rw, `{
			"name": "main",
			"commit": {
				"sha": "samplePRsha"
			}
		}`)
					assert.NilError(t, err)
				})

				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/ref/tags/v1.0.0", "owner", "reponame"), func(rw http.ResponseWriter, _ *http.Request) {
					ref := &github.Reference{
						Object: &github.GitObject{
							SHA:  github.Ptr("samplePRsha"),
							Type: github.Ptr(tt.objectType),
						},
					}
					bjeez, _ := json.Marshal(ref)
					fmt.Fprint(rw, string(bjeez))
				})
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/tags/samplePRsha", "owner", "reponame"), func(rw http.ResponseWriter, _ *http.Request) {
					tag := &github.Tag{
						Object: &github.GitObject{
							SHA: github.Ptr("samplePRsha"),
						},
					}
					bjeez, _ := json.Marshal(tag)
					fmt.Fprint(rw, string(bjeez))
				})

				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/commits/%s", "owner", "reponame", tt.shaRet), func(rw http.ResponseWriter, _ *http.Request) {
					name := "test"
					email := "test@test.com"
					if tt.isMergeCommit {
						name = "web-flow"
						email = "noreply@github.com"
					}
					commit := &github.Commit{
						SHA: github.Ptr(tt.shaRet),
						Committer: &github.CommitAuthor{
							Email: github.Ptr(email),
							Name:  github.Ptr(name),
						},
					}
					bjeez, _ := json.Marshal(commit)
					fmt.Fprint(rw, string(bjeez))
				})
			}

			logger, _ := logger.GetLogger()
			gprovider := Provider{
				ghClient: ghClient,
				Logger:   logger,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{SkipPushEventForPRCommits: tt.skipPushEventForPRCommits},
				},
			}
			request := &http.Request{Header: map[string][]string{}}
			request.Header.Set("X-GitHub-Event", tt.eventType)

			run := &params.Run{}
			bjeez, _ := json.Marshal(tt.payloadEventStruct)
			jeez := string(bjeez)
			if tt.jeez != "" {
				jeez = tt.jeez
			}
			ret, err := gprovider.ParsePayload(ctx, run, request, jeez)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)
			// If shaRet is empty, this is a skip case (push event for PR commit)
			// In this case, ret should be nil
			if tt.shaRet == "" {
				assert.Assert(t, ret == nil, "Expected nil result for skipped push event")
				return
			}
			assert.Assert(t, ret != nil)
			assert.Equal(t, tt.shaRet, ret.SHA)
			if tt.eventType == triggertype.PullRequest.String() {
				assert.Equal(t, "my first PR", ret.PullRequestTitle)
			}
			if tt.eventType == "commit_comment" {
				assert.Equal(t, tt.wantedBranchName, ret.HeadBranch)
				assert.Equal(t, tt.wantedBranchName, ret.BaseBranch)
				assert.Equal(t, tt.isCancelPipelineRunEnabled, ret.CancelPipelineRuns)
			}
			if tt.wantedPullRequestNumber != 0 {
				assert.Equal(t, tt.wantedPullRequestNumber, ret.PullRequestNumber)
			}
			if tt.targetPipelinerun != "" {
				assert.Equal(t, tt.targetPipelinerun, ret.TargetTestPipelineRun)
			}
			if tt.targetCancelPipelinerun != "" {
				assert.Equal(t, tt.targetCancelPipelinerun, ret.TargetCancelPipelineRun)
			}
			assert.Equal(t, tt.triggerTarget, string(ret.TriggerTarget))
		})
	}
}

func TestAppTokenGeneration(t *testing.T) {
	testNamespace := "pipelinesascode"

	ctxNoSecret, _ := rtesting.SetupFakeContext(t)
	noSecret, _ := testclient.SeedTestData(t, ctxNoSecret, testclient.Data{})
	secretName := "pipelines-as-code-secret"

	ctx, _ := rtesting.SetupFakeContext(t)
	webhookSecret := "webhook-secret"
	vaildSecret, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"github-application-id": []byte("12345"),
					"github-private-key":    []byte(fakePrivateKey),
					"webhook.secret":        []byte(webhookSecret),
				},
			},
		},
	})

	ctxInvalidAppID, _ := rtesting.SetupFakeContext(t)
	invalidAppID, _ := testclient.SeedTestData(t, ctxInvalidAppID, testclient.Data{
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pipelines-as-code-secret",
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"github-application-id": []byte("abcd"),
					"github-private-key":    []byte(fakePrivateKey),
					"webhook.secret":        []byte(webhookSecret),
				},
			},
		},
	})

	ctxInvalidPrivateKey, _ := rtesting.SetupFakeContext(t)
	invalidPrivateKey, _ := testclient.SeedTestData(t, ctxInvalidPrivateKey, testclient.Data{
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pipelines-as-code-secret",
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"github-application-id": []byte("12345"),
					"github-private-key":    []byte("invalid-key"),
					"webhook.secret":        []byte(webhookSecret),
				},
			},
		},
	})

	tests := []struct {
		ctx            context.Context
		ctxNS          string
		name           string
		wantErrSubst   string
		nilClient      bool
		seedData       testclient.Clients
		omitSignature  bool
		enterpriseHost string
		payload        string
		wantLogMessage string
	}{
		{
			name:           "secret not found",
			ctx:            ctxNoSecret,
			ctxNS:          "foo",
			seedData:       noSecret,
			wantErrSubst:   `secrets "pipelines-as-code-secret" not found`,
			wantLogMessage: githubAppTokenMintBlockedLog,
		},
		{
			ctx:            ctx,
			name:           "missing webhook signature",
			ctxNS:          testNamespace,
			seedData:       vaildSecret,
			omitSignature:  true,
			wantErrSubst:   "no signature has been detected",
			wantLogMessage: githubAppTokenMintBlockedLog,
		},
		{
			ctx:            ctx,
			name:           "enterprise host does not match signed repository payload",
			ctxNS:          testNamespace,
			seedData:       vaildSecret,
			enterpriseHost: "127.0.0.1:1",
			wantErrSubst:   `github enterprise host "127.0.0.1:1" does not match repository host "github.com"`,
			wantLogMessage: githubAppTokenExfiltrationBlockedLog,
		},
		{
			ctx:            ctx,
			name:           "enterprise host with missing repository HTML URL",
			ctxNS:          testNamespace,
			seedData:       vaildSecret,
			enterpriseHost: "127.0.0.1:1",
			payload:        fmt.Sprintf(`{"installation":{"id":%d},"repository":{}}`, testInstallationID),
			wantErrSubst:   "repository HTML URL is missing in payload, cannot validate enterprise host",
			wantLogMessage: githubAppTokenExfiltrationBlockedLog,
		},
		{
			ctx:       ctx,
			name:      "secret found",
			ctxNS:     testNamespace,
			seedData:  vaildSecret,
			nilClient: false,
		},
		{
			ctx:          ctxInvalidAppID,
			name:         "invalid app id in secret",
			ctxNS:        testNamespace,
			wantErrSubst: `could not parse the github application_id number from secret: strconv.ParseInt: parsing "abcd": invalid syntax`,
			seedData:     invalidAppID,
		},
		{
			ctx:          ctxInvalidPrivateKey,
			name:         "invalid private key in secret",
			ctxNS:        testNamespace,
			wantErrSubst: `could not parse private key: invalid key: Key must be a PEM encoded PKCS1 or PKCS8 key`,
			seedData:     invalidPrivateKey,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeghclient, mux, serverURL, teardown := ghtesthelper.SetupGH()
			defer teardown()
			mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", testInstallationID), func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, "{}")
			})

			// adding installation id to event to enforce client creation
			samplePRevent.Installation = &github.Installation{
				ID: &testInstallationID,
			}

			jeez, _ := json.Marshal(samplePRevent)
			if tt.payload != "" {
				jeez = []byte(tt.payload)
			}
			testLogger, observedLogs := logger.GetLogger()
			gprovider := Provider{
				Logger:   testLogger,
				ghClient: fakeghclient,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{},
				},
			}

			run := &params.Run{
				Clients: clients.Clients{
					Log:  testLogger,
					Kube: tt.seedData.Kube,
				},

				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: secretName},
				},
			}

			request := &http.Request{Header: map[string][]string{}}
			request.Header.Set("X-GitHub-Event", "pull_request")
			if !tt.omitSignature {
				request.Header.Set(github.SHA256SignatureHeader, githubSHA256Signature(webhookSecret, jeez))
			}
			if tt.enterpriseHost != "" {
				request.Header.Set("X-GitHub-Enterprise-Host", tt.enterpriseHost)
			}
			t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")

			tt.ctx = info.StoreCurrentControllerName(tt.ctx, "default")
			tt.ctx = info.StoreNS(tt.ctx, tt.ctxNS)

			_, err := gprovider.ParsePayload(tt.ctx, run, request, string(jeez))
			if tt.wantErrSubst != "" {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.wantErrSubst)
				if tt.wantLogMessage != "" {
					found := false
					for _, entry := range observedLogs.All() {
						if entry.Message == tt.wantLogMessage {
							found = true
							break
						}
					}
					assert.Assert(t, found, "expected log message %q for blocked GitHub App token mint", tt.wantLogMessage)
				}
				return
			}
			assert.NilError(t, err)
			if tt.nilClient {
				assert.Assert(t, gprovider.Client() == nil)
				return
			}

			// Verify client was created successfully for GitHub App
			assert.Assert(t, gprovider.Client() != nil)
		})
	}
}
