package bitbucketdatacenter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jenkins-x/go-scm/scm"
	bbtest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/test"
	bbtypes "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetMergeCommitChanges(t *testing.T) {
	tests := []struct {
		name             string
		fromCommit       string
		toCommit         string
		diffStats        *bbtypes.DiffStats
		wantChangesCount int
		wantAdded        int
		wantModified     int
		wantRenamed      int
		wantDeleted      int
		wantPreviousPath string
		wantErr          string
		apiStatusCode    int
		setup            func(t *testing.T, mux *http.ServeMux)
	}{
		{
			name:       "good/single page of changes",
			fromCommit: "abc123",
			toCommit:   "def456",
			diffStats: &bbtypes.DiffStats{
				Pagination: bbtypes.Pagination{
					Start:    0,
					Size:     4,
					Limit:    100,
					LastPage: true,
				},
				Values: []*bbtypes.DiffStat{
					{Path: bbtypes.DiffPath{ToString: "added.go"}, Type: "ADD"},
					{Path: bbtypes.DiffPath{ToString: "modified.txt"}, Type: "MODIFY"},
					{Path: bbtypes.DiffPath{ToString: "renamed.yaml"}, Type: "MOVE"},
					{Path: bbtypes.DiffPath{ToString: "deleted.md"}, Type: "DELETE"},
				},
			},
			wantChangesCount: 4,
			wantAdded:        1,
			wantModified:     1,
			wantRenamed:      1,
			wantDeleted:      1,
		},
		{
			name:       "good/empty changes",
			fromCommit: "abc123",
			toCommit:   "def456",
			diffStats: &bbtypes.DiffStats{
				Pagination: bbtypes.Pagination{
					Start:    0,
					Size:     0,
					Limit:    100,
					LastPage: true,
				},
				Values: []*bbtypes.DiffStat{},
			},
			wantChangesCount: 0,
		},
		{
			name:       "good/change with srcPath for rename",
			fromCommit: "abc123",
			toCommit:   "def456",
			diffStats: &bbtypes.DiffStats{
				Pagination: bbtypes.Pagination{
					Start:    0,
					Size:     1,
					Limit:    100,
					LastPage: true,
				},
				Values: []*bbtypes.DiffStat{
					{
						Path:    bbtypes.DiffPath{ToString: "new/path.go"},
						SrcPath: &bbtypes.DiffPath{ToString: "old/path.go"},
						Type:    "MOVE",
					},
				},
			},
			wantChangesCount: 1,
			wantRenamed:      1,
			wantPreviousPath: "old/path.go",
		},
		{
			name:          "bad/api returns error status",
			fromCommit:    "abc123",
			toCommit:      "def456",
			apiStatusCode: http.StatusBadRequest,
			wantErr:       "failed to get merge commit changes: status code: 400",
		},
		{
			name:       "bad/api returns invalid json",
			fromCommit: "abc123",
			toCommit:   "def456",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/projects/owner/repos/repo/changes", func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, "invalid json{{{")
				})
			},
			wantErr: "failed to decode merge commit changes",
		},
		{
			name:       "good/pagination sets next page",
			fromCommit: "abc123",
			toCommit:   "def456",
			diffStats: &bbtypes.DiffStats{
				Pagination: bbtypes.Pagination{
					Start:    0,
					Size:     100,
					Limit:    100,
					LastPage: false,
					NextPage: 100,
				},
				Values: []*bbtypes.DiffStat{
					{Path: bbtypes.DiffPath{ToString: "file.go"}, Type: "ADD"},
				},
			},
			wantChangesCount: 1,
			wantAdded:        1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			client, mux, tearDown, _ := bbtest.SetupBBDataCenterClient()
			defer tearDown()

			if tt.setup != nil {
				tt.setup(t, mux)
			} else {
				mux.HandleFunc("/projects/owner/repos/repo/changes", func(w http.ResponseWriter, r *http.Request) {
					if tt.apiStatusCode != 0 {
						w.WriteHeader(tt.apiStatusCode)
						return
					}
					assert.Equal(t, r.URL.Query().Get("since"), tt.fromCommit)
					assert.Equal(t, r.URL.Query().Get("until"), tt.toCommit)
					b, err := json.Marshal(tt.diffStats)
					assert.NilError(t, err)
					fmt.Fprint(w, string(b))
				})
			}

			v := &Provider{client: client}
			opts := &scm.ListOptions{Page: 1, Size: apiResponseLimit}
			changes, resp, err := v.getMergeCommitChanges(ctx, "owner", "repo", tt.fromCommit, tt.toCommit, opts)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantChangesCount, len(changes))

			added, modified, renamed, deleted := 0, 0, 0, 0
			for _, c := range changes {
				if c.Added {
					added++
				}
				if c.Modified {
					modified++
				}
				if c.Renamed {
					renamed++
				}
				if c.Deleted {
					deleted++
				}
			}
			assert.Equal(t, tt.wantAdded, added)
			assert.Equal(t, tt.wantModified, modified)
			assert.Equal(t, tt.wantRenamed, renamed)
			assert.Equal(t, tt.wantDeleted, deleted)

			if tt.wantPreviousPath != "" {
				assert.Equal(t, tt.wantPreviousPath, changes[0].PreviousPath)
			}

			if tt.diffStats != nil && !tt.diffStats.LastPage {
				assert.Equal(t, 1, resp.Page.First)
				assert.Equal(t, 2, resp.Page.Next)
			}
		})
	}
}
