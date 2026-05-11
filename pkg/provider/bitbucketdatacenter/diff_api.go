package bitbucketdatacenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jenkins-x/go-scm/scm"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
)

// getMergeCommitChanges gets the changes between two commits for a merge commit.
// this endpoint is not implemented in the go-scm package, so we need to use the raw HTTP request.
// but we're using the client from the go-scm package to make the request.
func (v *Provider) getMergeCommitChanges(ctx context.Context, org, repo, fromCommit, toCommit string, opts *scm.ListOptions) ([]*scm.Change, *scm.Response, error) {
	params := url.Values{}
	params.Set("since", fromCommit)
	params.Set("until", toCommit)
	params.Set("start", fmt.Sprintf("%d", (opts.Page-1)*opts.Size))
	params.Set("limit", fmt.Sprintf("%d", opts.Size))
	path := fmt.Sprintf("rest/api/1.0/projects/%s/repos/%s/changes?%s",
		url.PathEscape(org),
		url.PathEscape(repo),
		params.Encode())

	resp, err := v.Client().Do(ctx, &scm.Request{
		Method: "GET",
		Path:   path,
		Header: http.Header{
			"Content-Type":      {"application/json"},
			"x-atlassian-token": {"no-check"},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get merge commit changes: %w", err)
	}
	defer resp.Body.Close()

	if resp.Status > 300 {
		var errResp struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		errMsg := fmt.Sprintf("status code: %d", resp.Status)
		if decErr := json.NewDecoder(resp.Body).Decode(&errResp); decErr == nil && len(errResp.Errors) > 0 {
			errMsg = fmt.Sprintf("%s, message: %s", errMsg, errResp.Errors[0].Message)
		}
		return nil, nil, fmt.Errorf("failed to get merge commit changes: %s", errMsg)
	}

	out := new(types.DiffStats)

	err = json.NewDecoder(resp.Body).Decode(out)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode merge commit changes: %w", err)
	}

	if !out.LastPage {
		resp.Page.First = 1
		resp.Page.Next = opts.Page + 1
	}

	return convertDiffstats(out), resp, nil
}

func convertDiffstats(from *types.DiffStats) []*scm.Change {
	to := make([]*scm.Change, 0, len(from.Values))
	for _, v := range from.Values {
		to = append(to, convertDiffstat(v))
	}
	return to
}

func convertDiffstat(from *types.DiffStat) *scm.Change {
	to := &scm.Change{
		Path:     from.Path.ToString,
		Added:    from.Type == "ADD",
		Modified: from.Type == "MODIFY",
		Renamed:  from.Type == "MOVE",
		Deleted:  from.Type == "DELETE",
	}
	if from.SrcPath != nil {
		to.PreviousPath = from.SrcPath.ToString
	}
	return to
}
