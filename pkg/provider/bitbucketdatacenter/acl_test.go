/*
Copyright 2021 Red Hat

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package bitbucketdatacenter

import (
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	bbv1test "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/test"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"

	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestIsAllowed(t *testing.T) {
	ownerAccountID := 1234
	otherAccountID := 6666

	type fields struct {
		projectMembers            []*bbv1test.UserPermission
		repoMembers               []*bbv1test.UserPermission
		projGroups                []*bbv1test.ProjGroup
		repoGroups                []*bbv1test.ProjGroup
		groupMembers              map[string][]bbv1test.GroupMember
		activities                []*bbv1test.Activity
		filescontents             map[string]string
		defaultBranchLatestCommit string
		pullRequestNumber         int
	}
	tests := []struct {
		name          string
		event         *info.Event
		fields        fields
		isAllowed     bool
		wantErrSubstr string
	}{
		{
			name:  "allowed/user is owner",
			event: bbv1test.MakeEvent(&info.Event{Sender: "member", AccountID: fmt.Sprintf("%d", ownerAccountID)}),
			fields: fields{
				projectMembers: []*bbv1test.UserPermission{
					{
						User: types.User{
							Slug: "member",
						},
					},
				},
				pullRequestNumber: 1,
			},
			isAllowed: true,
		},
		{
			name: "allowed/from a comment owner",
			event: bbv1test.MakeEvent(&info.Event{
				AccountID: fmt.Sprintf("%d", otherAccountID),
				Sender:    "NotAllowedAtFirst",
			}),
			fields: fields{
				projectMembers: []*bbv1test.UserPermission{
					{
						User: types.User{
							Slug: "member",
						},
					},
				},
				activities: []*bbv1test.Activity{
					{
						Action: "COMMENTED",
						Comment: types.ActivityComment{
							Text: "/ok-to-test",
							Author: types.User{
								Slug: "member",
							},
						},
					},
				},
				pullRequestNumber: 1,
			},
			isAllowed: true,
		},
		{
			name: "allowed/from owner file who is not part of workspace",
			event: bbv1test.MakeEvent(&info.Event{
				AccountID:     fmt.Sprintf("%d", otherAccountID),
				DefaultBranch: "default",
			}),
			fields: fields{
				defaultBranchLatestCommit: "defaultlatestcommit",
				activities: []*bbv1test.Activity{
					{
						Action: "COMMENTED",
						Comment: types.ActivityComment{
							Text: "/ok-to-test",
							Author: types.User{
								ID: 15551,
							},
						},
					},
				},
				filescontents: map[string]string{
					"OWNERS": "---\n approvers:\n  - 15551\n",
				},
			},
			isAllowed: true,
		},
		{
			name: "disallowed/from an ownerfile that has nothing to do with sender",
			event: bbv1test.MakeEvent(
				&info.Event{
					AccountID: "0000",
					Sender:    "NotAllowed",
				},
			),
			fields: fields{
				projectMembers: []*bbv1test.UserPermission{
					{
						User: types.User{
							ID: 1234,
						},
					},
				},
				filescontents: map[string]string{
					"OWNERS": "---\n approvers:\n  - 1234\n",
				},
			},
			isAllowed: false,
		},
		{
			name:  "allowed/user is in project group",
			event: bbv1test.MakeEvent(&info.Event{Sender: "sender", AccountID: fmt.Sprintf("%d", otherAccountID)}),
			fields: fields{
				projGroups: []*bbv1test.ProjGroup{
					{
						Group:      bbv1test.Group{Name: "project-devs"},
						Permission: "PROJECT_WRITE",
					},
				},
				groupMembers: map[string][]bbv1test.GroupMember{
					"project-devs": {
						{Name: "sender", Slug: "sender"},
					},
				},
			},
			isAllowed: true,
		},
		{
			name:  "allowed/user is in repo group",
			event: bbv1test.MakeEvent(&info.Event{Sender: "sender", AccountID: fmt.Sprintf("%d", otherAccountID)}),
			fields: fields{
				repoGroups: []*bbv1test.ProjGroup{
					{
						Group:      bbv1test.Group{Name: "repo-devs"},
						Permission: "REPO_WRITE",
					},
				},
				groupMembers: map[string][]bbv1test.GroupMember{
					"repo-devs": {
						{Name: "sender", Slug: "sender"},
					},
				},
			},
			isAllowed: true,
		},
		{
			name:  "disallowed/user not in any group members",
			event: bbv1test.MakeEvent(&info.Event{Sender: "outsider", AccountID: fmt.Sprintf("%d", otherAccountID)}),
			fields: fields{
				projGroups: []*bbv1test.ProjGroup{
					{
						Group:      bbv1test.Group{Name: "project-devs"},
						Permission: "PROJECT_WRITE",
					},
				},
				repoGroups: []*bbv1test.ProjGroup{
					{
						Group:      bbv1test.Group{Name: "repo-devs"},
						Permission: "REPO_WRITE",
					},
				},
				groupMembers: map[string][]bbv1test.GroupMember{
					"project-devs": {
						{Name: "someone-else", Slug: "someone-else"},
					},
					"repo-devs": {
						{Name: "another-person", Slug: "another-person"},
					},
				},
			},
			isAllowed: false,
		},
		{
			name:  "disallowed/same nickname different account id",
			event: bbv1test.MakeEvent(&info.Event{Sender: "Bouffon", AccountID: "6666"}),
			fields: fields{
				projectMembers: []*bbv1test.UserPermission{
					{
						User: types.User{
							DisplayName: "Bouffon",
							ID:          7777,
						},
					},
				},
			},
			isAllowed: false,
		},
		{
			name:  "disallowed/not a valid ok-to-test comment",
			event: bbv1test.MakeEvent(&info.Event{Sender: "Bouffon", AccountID: "6666"}),
			fields: fields{
				projectMembers: []*bbv1test.UserPermission{
					{
						User: types.User{
							ID: ownerAccountID,
						},
					},
				},
				activities: []*bbv1test.Activity{
					{
						Comment: types.ActivityComment{
							Text: "not a valid\n /ok-to-test",
							Author: types.User{
								ID: ownerAccountID,
							},
						},
					},
				},
			},
			isAllowed: false,
		},
		{
			name: "allowed/ok-to-test on new line",
			event: bbv1test.MakeEvent(&info.Event{
				AccountID: fmt.Sprintf("%d", otherAccountID),
				Sender:    "NotAllowedAtFirst",
			}),
			fields: fields{
				projectMembers: []*bbv1test.UserPermission{
					{
						User: types.User{
							ID: ownerAccountID,
						},
					},
				},
				activities: []*bbv1test.Activity{
					{
						Action: "COMMENTED",
						Comment: types.ActivityComment{
							Text: "this is a valid\n/ok-to-test",
							Author: types.User{
								ID: ownerAccountID,
							},
						},
					},
				},
			},
			isAllowed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			client, mux, tearDown, tURL := bbv1test.SetupBBDataCenterClient()
			defer tearDown()
			bbv1test.MuxProjectMemberShip(t, mux, tt.event, tt.fields.projectMembers)
			bbv1test.MuxRepoMemberShip(t, mux, tt.event, tt.fields.repoMembers)
			bbv1test.MuxProjectGroupMembership(t, mux, tt.event, tt.fields.projGroups)
			bbv1test.MuxRepoGroupMembership(t, mux, tt.event, tt.fields.repoGroups)
			bbv1test.MuxGroupMembers(t, mux, tt.fields.groupMembers)
			bbv1test.MuxPullRequestActivities(t, mux, tt.event, tt.fields.pullRequestNumber, tt.fields.activities)
			bbv1test.MuxFiles(t, mux, tt.event, tt.fields.defaultBranchLatestCommit, "", tt.fields.filescontents, false)

			v := &Provider{
				baseURL:                   tURL,
				client:                    client,
				defaultBranchLatestCommit: tt.fields.defaultBranchLatestCommit,
				pullRequestNumber:         tt.fields.pullRequestNumber,
				projectKey:                tt.event.Organization,
			}

			got, err := v.IsAllowed(ctx, tt.event)
			if tt.wantErrSubstr != "" {
				assert.ErrorContains(t, err, tt.wantErrSubstr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.isAllowed, got, "BitbucketDataCenter.IsAllowed() = %v, want %v", got, tt.isAllowed)
		})
	}
}
