package github

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCheckPolicyAllowing(t *testing.T) {
	tests := []struct {
		name         string
		allowedTeams []string
		reply        string
		otherReply   string
		wantAllowed  bool
		wantReason   string
	}{
		{
			name:         "user is a member of the allowed team",
			allowedTeams: []string{"allowedTeam"},
			wantAllowed:  true,
			wantReason:   "allowing user: allowedUser as a member of the team: allowedTeam",
			reply:        `[{"login": "allowedUser"}]`,
		},
		{
			name:         "user is not a member of the allowed team",
			allowedTeams: []string{"otherteam"},
			wantAllowed:  false,
			wantReason:   "user: allowedUser is not a member of any of the allowed teams: [otherteam]",
			otherReply:   `[{"login": "myuser"}]`,
		},
		{
			name:         "team is not found",
			allowedTeams: []string{"nothere"},
			wantAllowed:  false,
			wantReason:   "team: nothere is not found on the organization: myorg",
		},
		{
			name:         "error while getting team membership",
			allowedTeams: []string{"allowedTeam"},
			wantAllowed:  false,
			wantReason:   `error while getting team membership for user: allowedUser in team: allowedTeam, error: invalid character 't' in literal true (expecting 'r')`,
			reply:        `tttttt`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			event := &info.Event{
				Organization: "myorg",
				Sender:       "allowedUser",
			}
			mux.HandleFunc("/orgs/myorg/teams/allowedTeam/members", func(rw http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
					rw.Header().Add("Link", `<https://api.github.com/orgs/myorg/teams/allowedTeam/members?page=2&per_page=1>; rel="next"`)
					fmt.Fprint(rw, `[]`)
				} else {
					fmt.Fprint(rw, tt.reply)
				}
			})
			mux.HandleFunc("/orgs/myorg/teams/otherteam/members", func(rw http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
					rw.Header().Add("Link", `<https://api.github.com/orgs/myorg/teams/otherteam/members?page=2&per_page=1>; rel="next"`)
					fmt.Fprint(rw, `[]`)
				} else {
					fmt.Fprint(rw, tt.otherReply)
				}
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{},
			}}
			gprovider := Provider{
				ghClient:      fakeclient,
				repo:          repo,
				Logger:        logger,
				PaginedNumber: 1,
			}

			gotAllowed, gotReason := gprovider.CheckPolicyAllowing(ctx, event, tt.allowedTeams)
			assert.Equal(t, tt.wantAllowed, gotAllowed)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

func TestOkToTestComment(t *testing.T) {
	tests := []struct {
		name                string
		commentsReply       string
		runevent            info.Event
		allowed             bool
		wantErr             bool
		rememberOkToTest    bool
		gitOpsCommentPrefix string
	}{
		{
			name:          "good issue comment event",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "good issue pull request event",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
					},
				},
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "bad event origin",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.CheckRunEvent{
					CheckRun: &github.CheckRun{
						HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "no-ok-to-test",
			commentsReply: `[{"body": "Foo Bar", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "ok-to-test-not-from-owner",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "notowner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "good issue comment event without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "good issue pull request event without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "bad event origin without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.CheckRunEvent{
					CheckRun: &github.CheckRun{
						HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "no-ok-to-test without remember",
			commentsReply: `{"body": "Foo Bar", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "ok-to-test-not-from-owner without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "notowner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:                "good issue comment event with custom prefix",
			commentsReply:       `[{"body": "/pac ok-to-test", "user": {"login": "owner"}}]`,
			gitOpsCommentPrefix: "pac",
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.runevent.TriggerTarget = "ok-to-test-comment"
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			mux.HandleFunc("/repos/owner/issues/comments/0", func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.commentsReply)
			})
			mux.HandleFunc("/repos/owner/issues/1/comments", func(rw http.ResponseWriter, r *http.Request) {
				// this will test if pagination works okay
				if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
					rw.Header().Add("Link", `<https://api.github.com/owner/repo/issues/1/comments?page=2&per_page=1>; rel="next"`)
					fmt.Fprint(rw, `[{"body": "Foo Bar", "user": {"login": "notallowed"}}]`, tt.commentsReply)
				} else {
					fmt.Fprint(rw, tt.commentsReply)
				}
			})
			mux.HandleFunc("/repos/owner/collaborators", func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, "[]")
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{
					GitOpsCommandPrefix: tt.gitOpsCommentPrefix,
				},
			}}
			pacopts := &info.PacOpts{
				Settings: settings.Settings{
					RememberOKToTest: tt.rememberOkToTest,
				},
			}
			gprovider := Provider{
				ghClient:      fakeclient,
				repo:          repo,
				Logger:        logger,
				PaginedNumber: 1,
				Run:           &params.Run{},
				pacInfo:       pacopts,
			}

			got, err := gprovider.IsAllowed(ctx, &tt.runevent)
			if (err != nil) != tt.wantErr {
				t.Errorf("aclCheck() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.allowed {
				t.Errorf("aclCheck() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestOkToTestCommentSHA(t *testing.T) {
	tests := []struct {
		name                   string
		commentsReply          string
		commentBody            string
		runevent               info.Event
		allowed                bool
		wantErr                bool
		requireOkToTestSHA     bool
		pullRequestReply       string
		pullRequestListComment string
		gitOpsCommentPrefix    string
	}{
		{
			name:               "good issue comment event with sha",
			commentsReply:      `[{"body": "/ok-to-test ABCDEF1", "user": {"login": "owner"}}]`,
			commentBody:        "/ok-to-test ABCDEF1",
			pullRequestReply:   `{"head": {"sha": "abcdef1234567890"}, "base": {"repo": {"html_url": "http://url.com/owner/repo/1"}}}`,
			requireOkToTestSHA: true,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed: true,
			wantErr: false,
		},
		{
			name:               "bad issue comment event with sha",
			commentsReply:      `[{"body": "/ok-to-test 1234567", "user": {"login": "owner"}}]`,
			commentBody:        "/ok-to-test 1234567",
			pullRequestReply:   `{"head": {"sha": "7654321"}, "base": {"repo": {"html_url": "http://url.com/owner/repo/1"}}}`,
			requireOkToTestSHA: true,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed: false,
			wantErr: true,
		},
		{
			name:               "good issue comment event without sha",
			commentsReply:      `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			commentBody:        "/ok-to-test",
			pullRequestReply:   `{"head": {"sha": "1234567890"}, "base": {"repo": {"html_url": "http://url.com/owner/repo/1"}}}`,
			requireOkToTestSHA: false,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed: true,
			wantErr: false,
		},
		{
			name:               "bad issue comment event without sha when required",
			commentsReply:      `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			commentBody:        "/ok-to-test",
			pullRequestReply:   `{"head": {"sha": "1234567890"}, "base": {"repo": {"html_url": "http://url.com/owner/repo/1"}}}`,
			requireOkToTestSHA: true,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed: false,
			wantErr: true,
		},
		{
			name:                "good issue comment event with sha and custom prefix",
			commentsReply:       `[{"body": "/pac ok-to-test ABCDEF1", "user": {"login": "owner"}}]`,
			commentBody:         "/pac ok-to-test ABCDEF1",
			gitOpsCommentPrefix: "pac ",
			pullRequestReply:    `{"head": {"sha": "abcdef1234567890"}, "base": {"repo": {"html_url": "http://url.com/owner/repo/1"}}}`,
			requireOkToTestSHA:  true,
			runevent: info.Event{
				Organization: "owner",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event: &github.IssueCommentEvent{
					Issue: &github.Issue{
						PullRequestLinks: &github.PullRequestLinks{
							HTMLURL: github.Ptr("http://url.com/owner/repo/1"),
						},
					},
				},
			},
			allowed: true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.runevent.TriggerTarget = "ok-to-test-comment"
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			mux.HandleFunc("/repos/owner/issues/comments/0", func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.commentsReply)
			})
			mux.HandleFunc("/repos/owner/repo/pulls/1", func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.pullRequestReply)
			})
			mux.HandleFunc("/repos/owner/repo/issues/1/comments", func(rw http.ResponseWriter, r *http.Request) {
				// this will test if pagination works okay
				if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
					rw.Header().Add("Link", `<https://api.github.com/owner/repo/issues/1/comments?page=2&per_page=1>; rel="next"`)
					fmt.Fprint(rw, `[{"body": "Foo Bar", "user": {"login": "notallowed"}}]`, tt.commentsReply)
				} else {
					fmt.Fprint(rw, tt.commentsReply)
				}
			})
			mux.HandleFunc("/repos/owner/collaborators", func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, "[]")
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			tdata := testclient.Data{}
			repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
				URL: "http://url.com/owner/repo/1",
				Settings: &v1alpha1.Settings{
					GitOpsCommandPrefix: tt.gitOpsCommentPrefix,
				},
			}}
			tdata.Repositories = []*v1alpha1.Repository{repo}
			pacopts := &info.PacOpts{
				Settings: settings.Settings{
					RequireOkToTestSHA: tt.requireOkToTestSHA,
				},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			gprovider := Provider{
				ghClient:      fakeclient,
				repo:          repo,
				Logger:        logger,
				PaginedNumber: 1,
				Run:           &params.Run{Clients: clients.Clients{PipelineAsCode: stdata.PipelineAsCode}},
				pacInfo:       pacopts,
			}

			payload := fmt.Sprintf(`{"action": "created", "repository": {"name": "repo", "owner": {"login": "owner"}}, "sender": {"login": %q}, "issue": {"pull_request": {"html_url": "https://github.com/owner/repo/pull/1"}}, "comment": {"body": %q}}`,
				tt.runevent.Sender, tt.commentBody)
			_, err := gprovider.ParsePayload(ctx, gprovider.Run, &http.Request{Header: http.Header{"X-Github-Event": []string{"issue_comment"}}}, payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("aclCheck() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestAclCheckAll(t *testing.T) {
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	orgallowed := "allowed"
	orgdenied := "denied"
	collabOwner := "collab"
	collabRepo := "collabRepo"
	collaborator := "collaborator"
	repoOwnerFileAllowed := "repoOwnerAllowed"

	errit := "err"

	mux.HandleFunc("/orgs/"+orgallowed+"/members", func(rw http.ResponseWriter, r *http.Request) {
		// this will test if pagination works okay
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			rw.Header().Add("Link", `<https://api.github.com/orgs/`+orgallowed+`/members?page=2&per_page=1>; rel="next"`)
			fmt.Fprint(rw, `[{"login": "notallowed"}]`, orgallowed)
		} else {
			fmt.Fprintf(rw, `[{"login": "login_%s"}]`, orgallowed)
		}
	})

	mux.HandleFunc("/orgs/"+orgdenied+"/members", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `[]`)
	})

	mux.HandleFunc("/repos/"+orgdenied+"/collaborators", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `[]`)
	})

	mux.HandleFunc("/orgs/"+errit+"/members", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `x1x`)
	})

	mux.HandleFunc("/orgs/"+repoOwnerFileAllowed+"/members", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `[]`)
	})
	mux.HandleFunc("/repos/"+repoOwnerFileAllowed+"/collaborators", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `[]`)
	})
	mux.HandleFunc("/repos/"+repoOwnerFileAllowed+"/contents/OWNERS", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"name": "OWNERS", "path": "OWNERS", "sha": "ownerssha"}`)
	})

	mux.HandleFunc("/repos/"+repoOwnerFileAllowed+"/git/blobs/ownerssha", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"content": "%s"}`, base64.RawStdEncoding.EncodeToString([]byte("approvers:\n  - approved\n")))
	})

	mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/collaborators/%v", collabOwner, collabRepo, collaborator), func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	})

	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)
	gprovider := Provider{
		ghClient:      fakeclient,
		Logger:        logger,
		PaginedNumber: 1,
	}

	tests := []struct {
		name     string
		runevent info.Event
		allowed  bool
		wantErr  bool
	}{
		{
			name: "sender allowed in org",
			runevent: info.Event{
				Organization: orgallowed,
				Sender:       "login_allowed",
			},
			allowed: true,
			wantErr: false,
		},
		{
			name: "sender allowed from owner file",
			runevent: info.Event{
				Organization: repoOwnerFileAllowed,
				Sender:       "approved",
			},
			allowed: true,
			wantErr: false,
		},
		{
			name: "owner is sender is allowed",
			runevent: info.Event{
				Organization: orgallowed,
				Sender:       "allowed",
			},
			allowed: true,
			wantErr: false,
		},
		{
			name: "sender allowed since collaborator on repo",
			runevent: info.Event{
				Organization: collabOwner,
				Repository:   collabRepo,
				Sender:       collaborator,
			},
			allowed: true,
			wantErr: false,
		},
		{
			name: "sender not allowed in org",
			runevent: info.Event{
				Organization: orgdenied,
				Sender:       "notallowed",
			},
			allowed: false,
			wantErr: false,
		},
		{
			name: "err it",
			runevent: info.Event{
				Organization: errit,
				Sender:       "error",
			},
			allowed: false,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gprovider.aclCheckAll(ctx, &tt.runevent)
			if (err != nil) != tt.wantErr {
				t.Errorf("aclCheckAll() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.allowed {
				t.Errorf("aclCheckAll() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestIfPullRequestIsForSameRepoWithoutFork(t *testing.T) {
	tests := []struct {
		name              string
		event             *info.Event
		commitFiles       []*github.CommitFile
		pullRequestNumber int
		allowed           bool
		wantError         bool
	}{
		{
			name: "when pull request raised by non owner to the repository where non owner did not fork but have permission to create branch",
			event: &info.Event{
				Organization:      "owner",
				Sender:            "nonowner",
				Repository:        "repo",
				PullRequestNumber: 1,
				HeadURL:           "http://org.com/owner/repo",
				BaseURL:           "http://org.com/owner/repo",
				HeadBranch:        "main",
				BaseBranch:        "dependabot",
				Event:             &github.PullRequestEvent{},
			},
			pullRequestNumber: 1,
			allowed:           true,
			wantError:         false,
		}, {
			name: "when HeadURL is not populated on the event",
			event: &info.Event{
				Organization:      "owner",
				Sender:            "nonowner",
				Repository:        "repo",
				PullRequestNumber: 1,
				Event:             &github.PullRequestEvent{},
			},
			pullRequestNumber: 1,
			allowed:           false,
			wantError:         false,
		}, {
			name: "when pull request raised by non owner to the repository where non owner don't have any permissions",
			event: &info.Event{
				Organization:      "owner",
				Sender:            "nonowner",
				Repository:        "repo",
				PullRequestNumber: 1,
				HeadURL:           "http://org.com/owner/repo",
				BaseURL:           "http://org.com/owner/repo1",
				HeadBranch:        "main",
				BaseBranch:        "dependabot",
				Event:             &github.PullRequestEvent{},
			},
			pullRequestNumber: 1,
			allowed:           false,
			wantError:         false,
		}, {
			name: "when issue comment sender is not trusted, same repo shortcut is not applied",
			event: &info.Event{
				Organization:      "owner",
				Sender:            "nonowner",
				Repository:        "repo",
				PullRequestNumber: 1,
				HeadURL:           "http://org.com/owner/repo",
				BaseURL:           "http://org.com/owner/repo",
				HeadBranch:        "main",
				BaseBranch:        "dependabot",
				Event:             &github.IssueCommentEvent{},
			},
			pullRequestNumber: 1,
			allowed:           false,
			wantError:         false,
		}, {
			name: "when check run rerequest resolves to same repo pull request the shortcut is applied",
			event: &info.Event{
				Organization:      "owner",
				Sender:            "dependabot[bot]",
				Repository:        "repo",
				PullRequestNumber: 1,
				HeadURL:           "http://org.com/owner/repo",
				BaseURL:           "http://org.com/owner/repo",
				HeadBranch:        "dependabot/npm-foo",
				BaseBranch:        "main",
				Event:             &github.CheckRunEvent{},
			},
			pullRequestNumber: 1,
			allowed:           true,
			wantError:         false,
		}, {
			name: "when check suite rerequest resolves to same repo pull request the shortcut is applied",
			event: &info.Event{
				Organization:      "owner",
				Sender:            "dependabot[bot]",
				Repository:        "repo",
				PullRequestNumber: 1,
				HeadURL:           "http://org.com/owner/repo",
				BaseURL:           "http://org.com/owner/repo",
				HeadBranch:        "dependabot/npm-foo",
				BaseBranch:        "main",
				Event:             &github.CheckSuiteEvent{},
			},
			pullRequestNumber: 1,
			allowed:           true,
			wantError:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, _, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			ctx, _ := rtesting.SetupFakeContext(t)

			repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{},
			}}
			gprovider := Provider{
				ghClient: fakeclient,
				repo:     repo,
			}

			got, err := gprovider.aclCheckAll(ctx, tt.event)
			if (err != nil) != tt.wantError {
				t.Errorf("aclCheck() error = %v, wantErr %v", err, tt.wantError)
				return
			}
			if got != tt.allowed {
				t.Errorf("aclCheck() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestACLCheckAllIssueCommentLogsShortcutSkip(t *testing.T) {
	fakeclient, _, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	ctx, _ := rtesting.SetupFakeContext(t)
	repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
		Settings: &v1alpha1.Settings{},
	}}
	observer, logs := zapobserver.New(zap.DebugLevel)

	gprovider := Provider{
		ghClient: fakeclient,
		repo:     repo,
		Logger:   zap.New(observer).Sugar(),
	}

	allowed, err := gprovider.aclCheckAll(ctx, &info.Event{
		Organization:      "owner",
		Sender:            "nonowner",
		Repository:        "repo",
		PullRequestNumber: 1,
		HeadURL:           "http://org.com/owner/repo",
		BaseURL:           "http://org.com/owner/repo",
		HeadBranch:        "main",
		BaseBranch:        "dependabot",
		Event:             &github.IssueCommentEvent{},
	})
	assert.NilError(t, err)
	assert.Equal(t, false, allowed)
	assert.Assert(t, logs.FilterMessageSnippet("Skipping same-repo pull request shortcut for untrusted event").Len() == 1)
}
