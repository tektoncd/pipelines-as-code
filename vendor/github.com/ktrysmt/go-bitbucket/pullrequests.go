package bitbucket

import (
	"encoding/json"
	"net/url"
)

type PullRequests struct {
	c *Client
}

func (p *PullRequests) Create(po *PullRequestsOptions) (interface{}, error) {
	data, err := p.buildPullRequestBody(po)
	if err != nil {
		return nil, err
	}
	urlStr := p.c.requestUrl("/repositories/%s/%s/pullrequests/", po.Owner, po.RepoSlug)
	return p.c.executeWithContext("POST", urlStr, data, po.ctx)
}

func (p *PullRequests) Update(po *PullRequestsOptions) (interface{}, error) {
	data, err := p.buildPullRequestBody(po)
	if err != nil {
		return nil, err
	}
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID
	return p.c.execute("PUT", urlStr, data)
}

func (p *PullRequests) GetByCommit(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/commit/" + po.Commit + "/pullrequests/"
	return p.c.executePaginated("GET", urlStr, "", nil)
}

func (p *PullRequests) GetCommits(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/commits/"
	return p.c.executePaginated("GET", urlStr, "", nil)
}

func (p *PullRequests) List(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + url.PathEscape(po.Owner) + "/" + url.PathEscape(po.RepoSlug) + "/pullrequests/"

	if po.States != nil && len(po.States) != 0 {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		query := parsed.Query()
		for _, state := range po.States {
			query.Set("state", state)
		}
		parsed.RawQuery = query.Encode()
		urlStr = parsed.String()
	}

	if po.Query != "" {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		query := parsed.Query()
		query.Set("q", po.Query)
		parsed.RawQuery = query.Encode()
		urlStr = parsed.String()
	}

	if po.Sort != "" {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		query := parsed.Query()
		query.Set("sort", po.Sort)
		parsed.RawQuery = query.Encode()
		urlStr = parsed.String()
	}

	return p.c.executePaginated("GET", urlStr, "", nil)
}

/*
Redirect to List() which is the function name declared in bitbucket.go
by Yoshimatsu on 6/10/19. This is to prevent breakage for anyone using
the missnamed Gets() function call.
*/
func (p *PullRequests) Gets(po *PullRequestsOptions) (interface{}, error) {
	return p.List(po)
}

func (p *PullRequests) Get(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID
	return p.c.execute("GET", urlStr, "")
}

func (p *PullRequests) Activities(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/activity"
	return p.c.executePaginated("GET", urlStr, "", nil)
}

func (p *PullRequests) Activity(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/activity"
	return p.c.execute("GET", urlStr, "")
}

func (p *PullRequests) Commits(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/commits"
	return p.c.executePaginated("GET", urlStr, "", nil)
}

func (p *PullRequests) Patch(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/patch"
	return p.c.executeRaw("GET", urlStr, "")
}

func (p *PullRequests) Diff(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/diff"
	return p.c.executeRaw("GET", urlStr, "")
}

func (p *PullRequests) Merge(po *PullRequestsOptions) (interface{}, error) {
	data, err := p.buildPullRequestBody(po)
	if err != nil {
		return nil, err
	}
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/merge"
	return p.c.executeWithContext("POST", urlStr, data, po.ctx)
}

func (p *PullRequests) Decline(po *PullRequestsOptions) (interface{}, error) {
	data, err := p.buildPullRequestBody(po)
	if err != nil {
		return nil, err
	}
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/decline"
	return p.c.executeWithContext("POST", urlStr, data, po.ctx)
}

func (p *PullRequests) Approve(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/approve"
	return p.c.executeWithContext("POST", urlStr, "", po.ctx)
}

func (p *PullRequests) UnApprove(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/approve"
	return p.c.execute("DELETE", urlStr, "")
}

func (p *PullRequests) RequestChanges(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/request-changes"
	return p.c.executeWithContext("POST", urlStr, "", po.ctx)
}

func (p *PullRequests) UnRequestChanges(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/request-changes"
	return p.c.execute("DELETE", urlStr, "")
}

func (p *PullRequests) AddComment(co *PullRequestCommentOptions) (interface{}, error) {
	data, err := p.buildPullRequestCommentBody(co)
	if err != nil {
		return nil, err
	}

	urlStr := p.c.requestUrl("/repositories/%s/%s/pullrequests/%s/comments", co.Owner, co.RepoSlug, co.PullRequestID)
	return p.c.executeWithContext("POST", urlStr, data, co.ctx)
}

func (p *PullRequests) UpdateComment(co *PullRequestCommentOptions) (interface{}, error) {
	data, err := p.buildPullRequestCommentBody(co)
	if err != nil {
		return nil, err
	}

	urlStr := p.c.requestUrl("/repositories/%s/%s/pullrequests/%s/comments/%s", co.Owner, co.RepoSlug, co.PullRequestID, co.CommentId)
	return p.c.execute("PUT", urlStr, data)
}

func (p *PullRequests) DeleteComment(co *PullRequestCommentOptions) (interface{}, error) {
	urlStr := p.c.requestUrl("/repositories/%s/%s/pullrequests/%s/comments/%s", co.Owner, co.RepoSlug, co.PullRequestID, co.CommentId)
	return p.c.execute("DELETE", urlStr, "")
}

func (p *PullRequests) GetComments(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/comments/"
	return p.c.executePaginated("GET", urlStr, "", nil)
}

func (p *PullRequests) GetComment(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/comments/" + po.CommentID
	return p.c.execute("GET", urlStr, "")
}

func (p *PullRequests) Statuses(po *PullRequestsOptions) (interface{}, error) {
	urlStr := p.c.GetApiBaseURL() + "/repositories/" + po.Owner + "/" + po.RepoSlug + "/pullrequests/" + po.ID + "/statuses"
	if po.Query != "" {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		query := parsed.Query()
		query.Set("q", po.Query)
		parsed.RawQuery = query.Encode()
		urlStr = parsed.String()
	}

	if po.Sort != "" {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		query := parsed.Query()
		query.Set("sort", po.Sort)
		parsed.RawQuery = query.Encode()
		urlStr = parsed.String()
	}
	return p.c.executePaginated("GET", urlStr, "", nil)
}

func (p *PullRequests) buildPullRequestBody(po *PullRequestsOptions) (string, error) {
	body := map[string]interface{}{}
	body["source"] = map[string]interface{}{}
	body["destination"] = map[string]interface{}{}
	body["message"] = ""

	// A PUT replaces the PR's reviewers with any list it carries, so a nil
	// Reviewers omits the key to keep them; an empty non-nil slice clears them.
	if po.Reviewers != nil {
		reviewers := make([]map[string]string, len(po.Reviewers))
		for i, uuid := range po.Reviewers {
			reviewers[i] = map[string]string{"uuid": uuid}
		}
		body["reviewers"] = reviewers
	}

	if po.SourceBranch != "" {
		body["source"].(map[string]interface{})["branch"] = map[string]string{"name": po.SourceBranch}
	}

	if po.SourceRepository != "" {
		body["source"].(map[string]interface{})["repository"] = map[string]interface{}{"full_name": po.SourceRepository}
	}

	if po.DestinationBranch != "" {
		body["destination"].(map[string]interface{})["branch"] = map[string]interface{}{"name": po.DestinationBranch}
	}

	if po.DestinationCommit != "" {
		body["destination"].(map[string]interface{})["commit"] = map[string]interface{}{"hash": po.DestinationCommit}
	}

	// A PUT overwrites every field it carries, so a nil Title or Description
	// omits the key to keep the current value; a pointer to "" sets it to empty.
	if po.Title != nil {
		body["title"] = *po.Title
	}

	if po.Description != nil {
		body["description"] = *po.Description
	}

	if po.Message != "" {
		body["message"] = po.Message
	}

	// Without close_source_branch, Merge falls back to the value chosen when the
	// pull request was created, so a nil CloseSourceBranch omits the key.
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-merge-post
	if po.CloseSourceBranch != nil {
		body["close_source_branch"] = *po.CloseSourceBranch
	}

	if po.MergeStrategy != "" {
		body["merge_strategy"] = po.MergeStrategy
	}

	if po.Draft {
		body["draft"] = true
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (p *PullRequests) buildPullRequestCommentBody(co *PullRequestCommentOptions) (string, error) {
	body := map[string]interface{}{}
	body["content"] = map[string]interface{}{
		"raw": co.Content,
	}

	if co.Parent != nil {
		body["parent"] = map[string]interface{}{
			"id": co.Parent,
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	return string(data), nil
}
