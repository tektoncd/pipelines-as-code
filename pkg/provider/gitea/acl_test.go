package gitea

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/forgejostructs"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCheckPolicyAllowing(t *testing.T) {
	tests := []struct {
		name                string
		allowedTeams        []string
		listOrgReply        string
		listOrgStatusCode   int
		listTeamMemberships string
		wantAllowed         bool
		wantReason          string
	}{
		{
			name:                "user is a member of the allowed team",
			allowedTeams:        []string{"allowedTeam"},
			wantAllowed:         true,
			wantReason:          "allowing user: allowedUser as a member of the team: allowedTeam",
			listOrgReply:        `[{"name": "allowedTeam", "id": 1}]`,
			listTeamMemberships: `{"id": 2}`,
		},
		{
			name:                "user is not a member of the allowed team",
			allowedTeams:        []string{"otherteam"},
			wantAllowed:         false,
			wantReason:          "user: allowedUser is not a member of any of the allowed teams: [otherteam]",
			listOrgReply:        `[{"name": "notgoodteam", "id": 1}]`,
			listTeamMemberships: `{"id": 0}`,
		},
		{
			name:         "no team in org is found",
			allowedTeams: []string{"nothere"},
			wantAllowed:  false,
			wantReason:   "no teams on org myorg",
		},
		{
			name:         "error while getting team membership",
			allowedTeams: []string{"allowedTeam"},
			wantAllowed:  false,
			wantReason:   `error while getting org team, error: invalid character 't' in literal true (expecting 'r')`,
			listOrgReply: `ttttttaaa`,
		},
		{
			name:              "forbidden when listing org teams",
			allowedTeams:      []string{"allowedTeam"},
			listOrgStatusCode: http.StatusForbidden,
			wantAllowed:       false,
			wantReason:        "unable to list teams on org myorg: the token used doesn't have permission to list teams in this org, make sure the token owner is a member of the org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			event := &info.Event{
				Organization: "myorg",
				Sender:       "allowedUser",
			}
			if tt.listOrgReply != "" || tt.listOrgStatusCode != 0 {
				mux.HandleFunc(fmt.Sprintf("/orgs/%s/teams", event.Organization), func(rw http.ResponseWriter, _ *http.Request) {
					if tt.listOrgStatusCode != 0 {
						rw.WriteHeader(tt.listOrgStatusCode)
						return
					}
					fmt.Fprint(rw, tt.listOrgReply)
				})
			}
			mux.HandleFunc(fmt.Sprintf("/teams/1/members/%s", event.Sender), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.listTeamMemberships)
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{},
				},
			}
			gprovider := Provider{
				giteaClient: fakeclient,
				repo:        repo,
				Logger:      logger,
			}

			gotAllowed, gotReason := gprovider.CheckPolicyAllowing(ctx, event, tt.allowedTeams)
			assert.Equal(t, tt.wantAllowed, gotAllowed)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

func TestCheckPolicyAllowingCaching(t *testing.T) {
	fakeclient, mux, teardown := tgitea.Setup(t)
	defer teardown()

	apiCallCount := 0
	event := &info.Event{
		Organization: "myorg",
		Sender:       "allowedUser",
	}

	mux.HandleFunc("/orgs/myorg/teams", func(rw http.ResponseWriter, _ *http.Request) {
		apiCallCount++
		fmt.Fprint(rw, `[{"name": "team1", "id": 1}]`)
	})
	mux.HandleFunc("/teams/1/members/allowedUser", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"id": 2}`)
	})

	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	gprovider := Provider{giteaClient: fakeclient, Logger: logger}

	allowed1, reason1 := gprovider.CheckPolicyAllowing(ctx, event, []string{"team1"})
	assert.Assert(t, allowed1)
	assert.Equal(t, "allowing user: allowedUser as a member of the team: team1", reason1)

	allowed2, reason2 := gprovider.CheckPolicyAllowing(ctx, event, []string{"team1"})
	assert.Assert(t, allowed2)
	assert.Equal(t, "allowing user: allowedUser as a member of the team: team1", reason2)

	assert.Equal(t, 1, apiCallCount)
}

func TestOkToTestComment(t *testing.T) {
	issueCommentPayload := &forgejostructs.IssueCommentPayload{
		Comment: &forgejostructs.Comment{
			ID: 1,
		},
		Issue: &forgejostructs.Issue{
			URL: "http://url.com/owner/repo/1",
		},
	}
	pullRequestPayload := &forgejostructs.PullRequestPayload{
		PullRequest: &forgejostructs.PullRequest{
			HTMLURL: "http://url.com/owner/repo/1",
		},
	}
	tests := []struct {
		name             string
		commentsReply    string
		runevent         info.Event
		allowed          bool
		wantErr          bool
		rememberOkToTest bool
	}{
		{
			name:          "allowed_from_org/good issue comment event",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "allowed_from_org/good issue pull request event",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        pullRequestPayload,
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "disallowed/bad event origin",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        &forgejostructs.RepositoryPayload{},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "disallowed/no-ok-to-test",
			commentsReply: `[{"body": "Foo Bar", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "disallowed/ok-to-test-not-from-owner",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "notowner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "notowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: true,
		},
		{
			name:          "allowed_from_org/good issue comment event without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed:          true,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "allowed_from_org/good issue pull request event without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        pullRequestPayload,
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "disallowed/bad event origin without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        &forgejostructs.RepositoryPayload{},
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "disallowed/no-ok-to-test without remember",
			commentsReply: `{"body": "Foo Bar", "user": {"login": "owner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
		{
			name:          "disallowed/ok-to-test-not-from-owner without remember",
			commentsReply: `{"body": "/ok-to-test", "user": {"login": "notowner"}}`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "notowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed:          false,
			wantErr:          false,
			rememberOkToTest: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			tt.runevent.TriggerTarget = "ok-to-test-comment"
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/1/comments", tt.runevent.Organization,
				tt.runevent.Repository),
				func(rw http.ResponseWriter,
					_ *http.Request,
				) {
					fmt.Fprint(rw, tt.commentsReply)
				})
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/comments/1", tt.runevent.Organization,
				tt.runevent.Repository),
				func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, tt.commentsReply)
				})
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", tt.runevent.Organization,
				tt.runevent.Repository, tt.runevent.Sender), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, `{"permission": "none"}`)
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			gprovider := Provider{
				giteaClient: fakeclient,
				Logger:      logger,
				run:         &params.Run{},
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						RememberOKToTest: tt.rememberOkToTest,
					},
				},
			}
			isAllowed, err := gprovider.IsAllowed(ctx, &tt.runevent)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.Assert(t, err == nil)
			}
			assert.Assert(t, isAllowed == tt.allowed)
		})
	}
}

func TestAclCheckAll(t *testing.T) {
	type allowedRules struct {
		ownerFile bool
		read      bool
		collabo   bool
		admin     bool
	}
	tests := []struct {
		name         string
		runevent     info.Event
		wantErr      bool
		allowedRules allowedRules
		allowed      bool
	}{
		{
			name: "allowed when sender has repository write collaborator permission",
			runevent: info.Event{
				Organization: "collabo",
				Repository:   "repo",
				Sender:       "login_allowed",
			},
			allowedRules: allowedRules{collabo: true},
			allowed:      true,
			wantErr:      false,
		},
		{
			name: "allowed when sender has repository admin permission",
			runevent: info.Event{
				Organization: "collabo",
				Repository:   "repo",
				Sender:       "login_allowed",
			},
			allowedRules: allowedRules{admin: true},
			allowed:      true,
			wantErr:      false,
		},
		{
			name: "allowed when sender is approver in OWNERS file",
			runevent: info.Event{
				Organization:  "collabo",
				Repository:    "repo",
				Sender:        "approved_from_owner_file",
				DefaultBranch: "maine",
				BaseBranch:    "maine",
			},
			allowedRules: allowedRules{ownerFile: true},
			allowed:      true,
			wantErr:      false,
		},
		{
			name: "disallowed when sender has no collaborator or OWNERS approval",
			runevent: info.Event{
				Organization: "denied",
				Repository:   "denied",
				Sender:       "notallowed",
			},
			allowed: false,
			wantErr: false,
		},
		{
			name: "allowed when sender has repository read permission",
			runevent: info.Event{
				Organization: "denied",
				Repository:   "denied",
				Sender:       "notallowed",
			},
			allowedRules: allowedRules{read: true},
			allowed:      false,
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			ctx, _ := rtesting.SetupFakeContext(t)
			gprovider := Provider{
				giteaClient: fakeclient,
				Logger:      logger,
			}

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", tt.runevent.Organization,
				tt.runevent.Repository, tt.runevent.Sender), func(rw http.ResponseWriter, _ *http.Request) {
				rw.WriteHeader(http.StatusOK)
				permission := "none"
				switch {
				case tt.allowedRules.admin:
					permission = "admin"
				case tt.allowedRules.collabo:
					permission = "write"
				case tt.allowedRules.read:
					permission = "read"
				}
				fmt.Fprintf(rw, `{"permission": "%s"}`, permission)
			})
			if tt.allowedRules.ownerFile {
				url := fmt.Sprintf("/repos/%s/%s/contents/OWNERS", tt.runevent.Organization, tt.runevent.Repository)
				mux.HandleFunc(url, func(rw http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("ref") != tt.runevent.DefaultBranch {
						rw.WriteHeader(http.StatusNotFound)
						return
					}
					encoded := base64.StdEncoding.EncodeToString([]byte(
						fmt.Sprintf("approvers:\n  - %s\n", tt.runevent.Sender),
					))
					// encode to json
					b, err := json.Marshal(forgejo.ContentsResponse{
						Content: &encoded,
					})
					if err != nil {
						rw.WriteHeader(http.StatusInternalServerError)
						return
					}
					rw.WriteHeader(http.StatusOK)
					_, _ = rw.Write(b)
				})
			}
			isAllowed, err := gprovider.aclCheckAll(ctx, &tt.runevent)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.Assert(t, err == nil)
			}
			assert.Assert(t, isAllowed == tt.allowed)
		})
	}
}
