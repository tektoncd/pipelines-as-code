package bitbucketcloud

import (
	"net/http"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	bbcloudtest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/test"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestIsAllowed(t *testing.T) {
	type fields struct {
		workspaceMembers []types.Member
		comments         []types.Comment
		filescontents    map[string]string
		membersStatus    int
	}
	tests := []struct {
		name    string
		event   *info.Event
		fields  fields
		want    bool
		wantErr bool
		token   string
	}{
		{
			name:  "access token allows sender in default branch OWNERS after forbidden membership request",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Owner"}),
			token: "ATCT-access-token",
			fields: fields{
				membersStatus: http.StatusForbidden,
				filescontents: map[string]string{"OWNERS": "approvers:\n  - Owner\n"},
			},
			want: true,
		},
		{
			name:  "access token allows sender in OWNERS after unauthorized membership request",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Owner"}),
			token: "ATCT-access-token",
			fields: fields{
				membersStatus: http.StatusUnauthorized,
				filescontents: map[string]string{"OWNERS": "approvers:\n  - Owner\n"},
			},
			want: true,
		},
		{
			name:  "access token allows sender through default branch OWNERS_ALIASES",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Owner"}),
			token: "ATCT-access-token",
			fields: fields{
				membersStatus: http.StatusForbidden,
				filescontents: map[string]string{
					"OWNERS":         "approvers:\n  - team\n",
					"OWNERS_ALIASES": "aliases:\n  team:\n    - Owner\n",
				},
			},
			want: true,
		},
		{
			name:  "access token rejects sender added to PR OWNERS_ALIASES",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Outsider"}),
			token: "ATCT-access-token",
			fields: fields{
				membersStatus: http.StatusForbidden,
				filescontents: map[string]string{
					"OWNERS":         "approvers:\n  - team\n",
					"OWNERS_ALIASES": "aliases:\n  team:\n    - Owner\n",
				},
			},
		},
		{
			name:  "access token allows ok-to-test from OWNERS account",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Outsider"}),
			token: "ATCT-access-token",
			fields: fields{
				membersStatus: http.StatusForbidden,
				filescontents: map[string]string{"OWNERS": "approvers:\n  - Owner\n"},
				comments:      []types.Comment{{Content: types.Content{Raw: "/ok-to-test"}, User: types.User{AccountID: "Owner"}}},
			},
			want: true,
		},
		{
			name:  "access token propagates workspace server errors",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Owner"}),
			token: "ATCT-access-token",
			fields: fields{
				membersStatus: http.StatusInternalServerError,
				filescontents: map[string]string{"OWNERS": "approvers:\n  - Owner\n"},
			},
			wantErr: true,
		},
		{
			name:  "API token propagates forbidden membership errors",
			event: bbcloudtest.MakeEvent(&info.Event{AccountID: "Owner"}),
			token: "ATAT-api-token",
			fields: fields{
				membersStatus: http.StatusForbidden,
				filescontents: map[string]string{"OWNERS": "approvers:\n  - Owner\n"},
			},
			wantErr: true,
		},
		{
			name:  "allowed/user is owner",
			event: bbcloudtest.MakeEvent(&info.Event{Sender: "member", AccountID: "IsaMember"}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							Nickname:  "member",
							AccountID: "IsaMember",
						},
					},
				},
			},
			want: true,
		},
		{
			name:  "allowed/from a comment owner",
			event: bbcloudtest.MakeEvent(&info.Event{Sender: "NotAllowedAtFirst"}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
				comments: []types.Comment{
					{
						Content: types.Content{Raw: "/ok-to-test"},
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "allowed/from owner file who is not part of workspace",
			event: bbcloudtest.MakeEvent(&info.Event{
				SHA:    "abcd",
				Sender: "NotAllowedAtFirst",
			}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							AccountID: "Randomweirdo",
						},
					},
				},
				comments: []types.Comment{
					{
						Content: types.Content{Raw: "/ok-to-test"},
						User: types.User{
							AccountID: "AllowedFromOwnerFile",
						},
					},
				},
				filescontents: map[string]string{
					"OWNERS": "---\n approvers:\n  - accountid\n",
				},
			},
			want: true,
		},
		{
			name:  "allowed/from an ownerfile who is a workspace member",
			event: bbcloudtest.MakeEvent(&info.Event{Sender: "NotAllowedAtFirst"}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
				comments: []types.Comment{
					{
						Content: types.Content{Raw: "/ok-to-test"},
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
			},
			want: true,
		},
		{
			name:  "disallowed/same nickname different account id",
			event: bbcloudtest.MakeEvent(&info.Event{Sender: "Bouffon", AccountID: "AccBouffon"}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							Nickname:  "Bouffon",
							AccountID: "NottheSameAccountID",
						},
					},
				},
			},
			want: false,
		},
		{
			name:  "disallowed/not a valid ok-to-test comment",
			event: bbcloudtest.MakeEvent(&info.Event{Sender: "Bouffon", AccountID: "AccBouffon"}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
				comments: []types.Comment{
					{
						Content: types.Content{Raw: "not a valid\n /ok-to-test"},
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
			},
			want: false,
		},
		{
			name:  "allowed/ok-to-test on new line",
			event: bbcloudtest.MakeEvent(&info.Event{Sender: "Bouffon", AccountID: "AccBouffon"}),
			fields: fields{
				workspaceMembers: []types.Member{
					{
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
				comments: []types.Comment{
					{
						Content: types.Content{Raw: "not a valid\n/ok-to-test"},
						User: types.User{
							AccountID: "Owner",
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			bbclient, mux, tearDown := bbcloudtest.SetupBBCloudClient(t)
			defer tearDown()

			if tt.fields.membersStatus != 0 {
				mux.HandleFunc("/workspaces/"+tt.event.Organization+"/members", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.fields.membersStatus)
				})
			} else {
				bbcloudtest.MuxOrgMember(t, mux, tt.event, tt.fields.workspaceMembers)
			}
			bbcloudtest.MuxComments(t, mux, tt.event, tt.fields.comments)
			bbcloudtest.MuxFiles(t, mux, tt.event, tt.fields.filescontents, "default_branch")
			// A PR's OWNERS file must never authorize its sender or commenters.
			bbcloudtest.MuxFiles(t, mux, tt.event, map[string]string{
				"OWNERS":         "approvers:\n  - " + tt.event.AccountID + "\n",
				"OWNERS_ALIASES": "aliases:\n  team:\n    - " + tt.event.AccountID + "\n",
			}, "")

			v := &Provider{bbClient: bbclient, Token: &tt.token}
			got, err := v.IsAllowed(ctx, tt.event)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, got, tt.want)
		})
	}
}
