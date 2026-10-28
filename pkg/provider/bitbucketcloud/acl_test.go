package bitbucketcloud

import (
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	bbcloudtest "github.com/tektoncd/pipelines-as-code/pkg/provider/bitbucketcloud/test"
	"github.com/tektoncd/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestIsAllowed(t *testing.T) {
	type fields struct {
		workspaceMembers []types.Member
		comments         []types.Comment
		filescontents    map[string]string
	}
	tests := []struct {
		name    string
		event   *info.Event
		fields  fields
		want    bool
		wantErr bool
	}{
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

			bbcloudtest.MuxOrgMember(t, mux, tt.event, tt.fields.workspaceMembers)
			bbcloudtest.MuxComments(t, mux, tt.event, tt.fields.comments)
			bbcloudtest.MuxFiles(t, mux, tt.event, tt.fields.filescontents, "")

			v := &Provider{bbClient: bbclient}
			got, err := v.IsAllowed(ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("Provider.IsAllowed() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Provider.IsAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
