package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-github/v61/github"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
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
		Login: github.String("owner"),
	},
	Name:          github.String("reponame"),
	DefaultBranch: github.String("defaultbranch"),
	HTMLURL:       github.String("https://github.com/owner/repo"),
}

var testInstallationID = int64(1)

func githubSHA256Signature(secret string, payload []byte) string {
	hm := hmac.New(sha256.New, []byte(secret))
	hm.Write(payload)
	return "sha256=" + hex.EncodeToString(hm.Sum(nil))
}

var samplePRevent = github.PullRequestEvent{
	PullRequest: &github.PullRequest{
		Head: &github.PullRequestBranch{
			SHA: github.String("sampleHeadsha"),
			Ref: github.String("headred"),
		},
		Base: &github.PullRequestBranch{
			SHA: github.String("basesha"),
			Ref: github.String("baseref"),
		},
		User: &github.User{
			Login: github.String("user"),
		},
		Title: github.String("my first PR"),
	},
	Repo: sampleRepo,
}

var samplePR = github.PullRequest{
	Number: github.Int(54321),
	Head: &github.PullRequestBranch{
		SHA:  github.String("samplePRsha"),
		Repo: sampleRepo,
	},
}

var samplePRAnother = github.PullRequest{
	Number: github.Int(54321),
	Head: &github.PullRequestBranch{
		SHA:  github.String("samplePRshanew"),
		Repo: sampleRepo,
	},
}

func TestParsePayLoad(t *testing.T) {
	tests := []struct {
		name                       string
		wantErrString              string
		eventType                  string
		payloadEventStruct         interface{}
		jeez                       string
		triggerTarget              string
		githubClient               bool
		muxReplies                 map[string]interface{}
		shaRet                     string
		targetPipelinerun          string
		targetCancelPipelinerun    string
		wantedBranchName           string
		isCancelPipelineRunEnabled bool
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
			payloadEventStruct: github.PullRequestReviewCommentEvent{Action: github.String("created")},
		},
		{
			name:               "bad/check run only issue recheck supported",
			wantErrString:      "only issue recheck is supported",
			eventType:          "check_run",
			triggerTarget:      "nonopetitrobot",
			payloadEventStruct: github.CheckRunEvent{Action: github.String("created")},
			githubClient:       true,
		},
		{
			name:               "bad/check run only with github apps",
			wantErrString:      "only supported with github apps",
			eventType:          "check_run",
			triggerTarget:      "pull_request",
			payloadEventStruct: github.CheckRunEvent{Action: github.String("created")},
		},
		{
			name:               "bad/issue comment retest only with github apps",
			wantErrString:      "only supported with github apps",
			eventType:          "issue_comment",
			triggerTarget:      "pull_request",
			payloadEventStruct: github.IssueCommentEvent{Action: github.String("created")},
		},
		{
			name:               "bad/issue comment not coming from pull request",
			eventType:          "issue_comment",
			triggerTarget:      "pull_request",
			githubClient:       true,
			payloadEventStruct: github.IssueCommentEvent{Action: github.String("created"), Issue: &github.Issue{}},
			wantErrString:      "issue comment is not coming from a pull_request",
		},
		{
			name:          "bad/issue comment invalid pullrequest",
			eventType:     "issue_comment",
			triggerTarget: "pull_request",
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.String("/bad"),
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
				Action: github.String("rerequested"),
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
			// specific run from a check_suite
			name:          "good/rerequest check_run on pull request",
			eventType:     "check_run",
			githubClient:  true,
			triggerTarget: "issue-recheck",
			payloadEventStruct: github.CheckRunEvent{
				Action: github.String("rerequested"),
				Repo:   sampleRepo,
				CheckRun: &github.CheckRun{
					CheckSuite: &github.CheckSuite{
						PullRequests: []*github.PullRequest{&samplePR},
					},
				},
			},
			muxReplies: map[string]interface{}{"/repos/owner/reponame/pulls/54321": samplePR},
			shaRet:     "samplePRsha",
		},
		// all checks in a check_suite
		{
			name:          "good/rerequest check_suite on pull request",
			eventType:     "check_suite",
			githubClient:  true,
			triggerTarget: "issue-recheck",
			payloadEventStruct: github.CheckSuiteEvent{
				Action: github.String("rerequested"),
				Repo:   sampleRepo,
				CheckSuite: &github.CheckSuite{
					PullRequests: []*github.PullRequest{&samplePR},
				},
			},
			muxReplies: map[string]interface{}{"/repos/owner/reponame/pulls/54321": samplePR},
			shaRet:     "samplePRsha",
		},
		{
			name:          "good/rerequest on push",
			eventType:     "check_run",
			githubClient:  true,
			triggerTarget: "issue-recheck",
			payloadEventStruct: github.CheckRunEvent{
				Action: github.String("rerequested"),
				Repo:   sampleRepo,
				CheckRun: &github.CheckRun{
					CheckSuite: &github.CheckSuite{
						HeadSHA: github.String("headSHACheckSuite"),
					},
				},
			},
			shaRet: "headSHACheckSuite",
		},
		{
			name:               "bad/issue_comment_not_from_created",
			wantErrString:      "only newly created comment is supported, received: deleted",
			payloadEventStruct: github.IssueCommentEvent{Action: github.String("deleted")},
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
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.String("/666"),
					},
				},
				Repo: sampleRepo,
			},
			muxReplies: map[string]interface{}{"/repos/owner/reponame/pulls/666": samplePR},
			shaRet:     "samplePRsha",
		},
		{
			name:               "good/pull request",
			eventType:          "pull_request",
			triggerTarget:      "pull_request",
			payloadEventStruct: samplePRevent,
			shaRet:             "sampleHeadsha",
		},
		{
			name:          "good/push",
			eventType:     "push",
			triggerTarget: "push",
			payloadEventStruct: github.PushEvent{
				Repo: &github.PushEventRepository{
					Owner: &github.User{Login: github.String("owner")},
					Name:  github.String("pushRepo"),
				},
				HeadCommit: &github.HeadCommit{ID: github.String("SHAPush")},
			},
			shaRet: "SHAPush",
		},
		{
			name:          "good/issue comment for retest",
			eventType:     "issue_comment",
			triggerTarget: "pull_request",
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.String("/777"),
					},
				},
				Repo: sampleRepo,
				Comment: &github.IssueComment{
					Body: github.String("/retest dummy"),
				},
			},
			muxReplies:        map[string]interface{}{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:            "samplePRsha",
			targetPipelinerun: "dummy",
		},
		{
			name:          "good/issue comment for cancel all",
			eventType:     "issue_comment",
			triggerTarget: "pull_request",
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.String("/999"),
					},
				},
				Repo: sampleRepo,
				Comment: &github.IssueComment{
					Body: github.String("/cancel"),
				},
			},
			muxReplies: map[string]interface{}{"/repos/owner/reponame/pulls/999": samplePR},
			shaRet:     "samplePRsha",
		},
		{
			name:          "good/issue comment for cancel a pr",
			eventType:     "issue_comment",
			triggerTarget: "pull_request",
			githubClient:  true,
			payloadEventStruct: github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						HTMLURL: github.String("/888"),
					},
				},
				Repo: sampleRepo,
				Comment: &github.IssueComment{
					Body: github.String("/cancel dummy"),
				},
			},
			muxReplies:              map[string]interface{}{"/repos/owner/reponame/pulls/888": samplePR},
			shaRet:                  "samplePRsha",
			targetCancelPipelinerun: "dummy",
		},
		{
			name:               "bad/commit comment retest only with github apps",
			wantErrString:      "only supported with github apps",
			eventType:          "commit_comment",
			triggerTarget:      "push",
			payloadEventStruct: github.CommitCommentEvent{Action: github.String("created")},
		},
		{
			name:          "good/commit comment for retest a pr",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/777"),
					Body:     github.String("/retest dummy"),
				},
			},
			muxReplies:        map[string]interface{}{"/repos/owner/reponame/pulls/777": samplePR},
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
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/777"),
					Body:     github.String("/retest"),
				},
			},
			muxReplies:       map[string]interface{}{"/repos/owner/reponame/pulls/777": samplePR},
			shaRet:           "samplePRsha",
			wantedBranchName: "main",
		},
		{
			name:          "good/commit comment for cancel all",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/999"),
					Body:     github.String("/cancel"),
				},
			},
			muxReplies:                 map[string]interface{}{"/repos/owner/reponame/pulls/999": samplePR},
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
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/888"),
					Body:     github.String("/cancel dummy"),
				},
			},
			muxReplies:                 map[string]interface{}{"/repos/owner/reponame/pulls/888": samplePR},
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
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/777"),
					Body:     github.String("/retest dummy branch:test1"),
				},
			},
			muxReplies:                 map[string]interface{}{"/repos/owner/reponame/pulls/7771": samplePR},
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
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/999"),
					Body:     github.String("/cancel branch:test1"),
				},
			},
			muxReplies:                 map[string]interface{}{"/repos/owner/reponame/pulls/9991": samplePR},
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
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/888"),
					Body:     github.String("/cancel dummy branch:test1"),
				},
			},
			muxReplies:                 map[string]interface{}{"/repos/owner/reponame/pulls/8881": samplePR},
			shaRet:                     "samplePRsha",
			targetCancelPipelinerun:    "dummy",
			wantedBranchName:           "test1",
			isCancelPipelineRunEnabled: true,
		},
		{
			name:          "good/commit comment for cancel a pr with invalid branch name",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.String("samplePRsha"),
					HTMLURL:  github.String("/888"),
					Body:     github.String("/cancel dummy branch:test2"),
				},
			},
			muxReplies:                 map[string]interface{}{"/repos/owner/reponame/pulls/8881": samplePR},
			shaRet:                     "samplePRsha",
			targetCancelPipelinerun:    "dummy",
			wantedBranchName:           "test2",
			isCancelPipelineRunEnabled: false,
			wantErrString:              "404 Not Found",
		},
		{
			name:          "commit comment to retest a pr with a SHA that does not exist in the main branch",
			eventType:     "commit_comment",
			triggerTarget: "push",
			githubClient:  true,
			payloadEventStruct: github.CommitCommentEvent{
				Repo: sampleRepo,
				Comment: &github.RepositoryComment{
					CommitID: github.String("samplePRshanew"),
					HTMLURL:  github.String("/777"),
					Body:     github.String("/retest dummy"),
				},
			},
			muxReplies:        map[string]interface{}{"/repos/owner/reponame/pulls/777": samplePRAnother},
			shaRet:            "samplePRshanew",
			targetPipelinerun: "dummy",
			wantedBranchName:  "main",
			wantErrString:     "provided branch main does not contains sha samplePRshanew",
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
			}
			logger, _ := logger.GetLogger()
			gprovider := Provider{
				Client: ghClient,
				Logger: logger,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{},
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
			assert.Assert(t, ret != nil)
			assert.Equal(t, tt.shaRet, ret.SHA)
			if tt.eventType == "pull_request" {
				assert.Equal(t, "my first PR", ret.PullRequestTitle)
			}
			if tt.eventType == "commit_comment" {
				assert.Equal(t, tt.wantedBranchName, ret.HeadBranch)
				assert.Equal(t, tt.wantedBranchName, ret.BaseBranch)
				assert.Equal(t, tt.isCancelPipelineRunEnabled, ret.CancelPipelineRuns)
			}
			if tt.targetPipelinerun != "" {
				assert.Equal(t, tt.targetPipelinerun, ret.TargetTestPipelineRun)
			}
			if tt.targetCancelPipelinerun != "" {
				assert.Equal(t, tt.targetCancelPipelinerun, ret.TargetCancelPipelineRun)
			}
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
		ctx                 context.Context
		ctxNS               string
		name                string
		wantErrSubst        string
		nilClient           bool
		seedData            testclient.Clients
		resultBaseURL       string
		checkInstallIDs     []int64
		extraRepoInstallIDs map[string]string
		omitSignature       bool
		enterpriseHost      string
		payload             string
		wantLogMessage      string
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
			ctx:             ctx,
			name:            "check installation ids are set",
			ctxNS:           testNamespace,
			seedData:        vaildSecret,
			nilClient:       false,
			checkInstallIDs: []int64{123},
		},
		{
			ctx:                 ctx,
			name:                "check extras installations ids set",
			ctxNS:               testNamespace,
			seedData:            vaildSecret,
			nilClient:           false,
			checkInstallIDs:     []int64{123},
			extraRepoInstallIDs: map[string]string{"another/one": "789", "andanother/two": "10112"},
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

			if len(tt.checkInstallIDs) > 0 {
				samplePRevent.PullRequest = &github.PullRequest{
					// order is important here for the check later
					Base: &github.PullRequestBranch{
						Repo: &github.Repository{
							ID: github.Int64(tt.checkInstallIDs[0]),
						},
					},
				}
			}

			jeez, _ := json.Marshal(samplePRevent)
			if tt.payload != "" {
				jeez = []byte(tt.payload)
			}
			testLogger, observedLogs := logger.GetLogger()
			gprovider := Provider{
				Logger: testLogger,
				Client: fakeghclient,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{},
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

			run := &params.Run{
				Clients: clients.Clients{
					Log:  testLogger,
					Kube: tt.seedData.Kube,
				},

				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: secretName},
				},
			}

			if len(tt.checkInstallIDs) > 0 {
				gprovider.pacInfo.SecretGHAppRepoScoped = true
			}
			if len(tt.extraRepoInstallIDs) > 0 {
				extras := ""
				for name := range tt.extraRepoInstallIDs {
					split := strings.Split(name, "/")
					mux.HandleFunc(fmt.Sprintf("/repos/%s/%s", split[0], split[1]), func(w http.ResponseWriter, _ *http.Request) {
						// i can't do a for name, iid and use iid, cause golang shadows the variable out of the for loop
						// a bit stupid
						sid := tt.extraRepoInstallIDs[fmt.Sprintf("%s/%s", split[0], split[1])]
						_, _ = fmt.Fprintf(w, `{"id": %s}`, sid)
					})
					extras += fmt.Sprintf("%s, ", name)
				}

				gprovider.pacInfo.SecretGHAppRepoScoped = true
				gprovider.pacInfo.SecretGhAppTokenScopedExtraRepos = extras
			}

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
				assert.Assert(t, gprovider.Client == nil)
				return
			}

			for k, id := range tt.checkInstallIDs {
				if gprovider.RepositoryIDs[k] != id {
					t.Errorf("got %d, want %d", gprovider.RepositoryIDs[k], id)
				}
			}

			for _, extraid := range tt.extraRepoInstallIDs {
				// checkInstallIDs and extraRepoInstallIds are merged and extraRepoInstallIds is after
				found := false
				extraIDInt, _ := strconv.ParseInt(extraid, 10, 64)
				for _, rid := range gprovider.RepositoryIDs {
					if extraIDInt == rid {
						found = true
					}
				}
				assert.Assert(t, found, "Could not find %s in %s", extraIDInt, tt.extraRepoInstallIDs)
			}

			assert.Assert(t, gprovider.Client != nil)
			if tt.resultBaseURL != "" {
				assert.Equal(t, gprovider.Client.BaseURL.String(), tt.resultBaseURL)
			}
		})
	}
}
