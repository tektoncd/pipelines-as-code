package bitbucketdatacenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
)

// checkValidPayload checks if the payload is valid.
func checkValidPayload(e *types.PullRequestEvent) error {
	if e.PullRequest.ToRef.Repository.Project == nil {
		return fmt.Errorf("bitbucket toRef project is nil")
	}
	if e.PullRequest.ToRef.Repository.Project.Key == "" {
		return fmt.Errorf("bitbucket toRef project key is empty")
	}
	if e.PullRequest.ToRef.Repository.Name == "" {
		return fmt.Errorf("bitbucket toRef repository name is empty")
	}
	if e.PullRequest.ToRef.LatestCommit == "" {
		return fmt.Errorf("bitbucket toRef latest commit is empty")
	}

	if e.PullRequest.FromRef.Repository.Project == nil {
		return fmt.Errorf("bitbucket fromRef project is nil")
	}
	if e.PullRequest.FromRef.Repository.Project.Key == "" {
		return fmt.Errorf("bitbucket fromRef project key is empty")
	}
	if e.PullRequest.FromRef.Repository.Name == "" {
		return fmt.Errorf("bitbucket fromRef repository name is empty")
	}
	if e.PullRequest.FromRef.LatestCommit == "" {
		return fmt.Errorf("bitbucket fromRef latest commit is empty")
	}
	if e.PullRequest.ID == 0 {
		return fmt.Errorf("bitbucket pull request ID is zero")
	}

	if e.PullRequest.ToRef.Repository.Links == nil || len(e.PullRequest.ToRef.Repository.Links.Self) == 0 {
		return fmt.Errorf("bitbucket toRef repository links are nil or empty")
	}
	if e.PullRequest.ToRef.DisplayID == "" {
		return fmt.Errorf("bitbucket toRef display ID is empty")
	}
	if e.PullRequest.FromRef.DisplayID == "" {
		return fmt.Errorf("bitbucket fromRef display ID is empty")
	}
	if e.PullRequest.FromRef.Repository.Links == nil || len(e.PullRequest.FromRef.Repository.Links.Self) == 0 {
		return fmt.Errorf("bitbucket fromRef repository links are nil or empty")
	}
	if len(e.PullRequest.ToRef.Repository.Links.Clone) == 0 {
		return fmt.Errorf("bitbucket toRef repository clone links are empty")
	}
	if len(e.PullRequest.FromRef.Repository.Links.Clone) == 0 {
		return fmt.Errorf("bitbucket fromRef repository clone links are empty")
	}

	if e.Actor.ID == 0 {
		return fmt.Errorf("bitbucket actor ID is zero")
	}
	if e.Actor.Name == "" {
		return fmt.Errorf("bitbucket actor name is empty")
	}
	return nil
}

// sanitizeEventURL returns the URL to the event without the /browse.
func sanitizeEventURL(eventURL string) string {
	if strings.HasSuffix(eventURL, "/browse") {
		return eventURL[:len(eventURL)-len("/browse")]
	}
	return eventURL
}

// sanitizeOwner remove ~ from OWNER in case of personal repos.
func sanitizeOwner(owner string) string {
	return strings.ReplaceAll(owner, "~", "")
}

// ParsePayload parses the payload from the event.
func (v *Provider) ParsePayload(_ context.Context, _ *params.Run, request *http.Request,
	payload string,
) (*info.Event, error) {
	processedEvent := info.NewEvent()

	eventType := request.Header.Get("X-Event-Key")
	eventPayload, err := parsePayloadType(eventType)
	if err != nil {
		return info.NewEvent(), err
	}

	if err := json.Unmarshal([]byte(payload), &eventPayload); err != nil {
		return info.NewEvent(), err
	}

	processedEvent.Event = eventPayload

	switch e := eventPayload.(type) {
	case *types.PullRequestEvent:
		if provider.Valid(eventType, []string{"pr:from_ref_updated", "pr:opened", "pr:merged", "pr:modified", "pr:reviewer:approved"}) {
			processedEvent.TriggerTarget = triggertype.PullRequest
			processedEvent.EventType = triggertype.PullRequest.String()
		} else if provider.Valid(eventType, []string{"pr:comment:added", "pr:comment:edited"}) {
			switch {
			case provider.IsTestRetestComment(e.Comment.Text):
				processedEvent.TriggerTarget = triggertype.PullRequest
				if strings.Contains(e.Comment.Text, "/test") {
					processedEvent.EventType = "test-comment"
				} else {
					processedEvent.EventType = "retest-comment"
				}
				processedEvent.TargetTestPipelineRun = provider.GetPipelineRunFromTestComment(e.Comment.Text)
			case provider.IsOkToTestComment(e.Comment.Text):
				processedEvent.TriggerTarget = triggertype.PullRequest
				processedEvent.EventType = "ok-to-test-comment"
			case provider.IsCancelComment(e.Comment.Text):
				processedEvent.TriggerTarget = triggertype.PullRequest
				processedEvent.EventType = "cancel-comment"
				processedEvent.CancelPipelineRuns = true
				processedEvent.TargetCancelPipelineRun = provider.GetPipelineRunFromCancelComment(e.Comment.Text)
			}
			processedEvent.TriggerComment = e.Comment.Text
		}

		if err := checkValidPayload(e); err != nil {
			return nil, err
		}

		// TODO: It's Really not an OWNER but a PROJECT
		processedEvent.Organization = e.PullRequest.ToRef.Repository.Project.Key
		processedEvent.Repository = e.PullRequest.ToRef.Repository.Name
		processedEvent.SHA = e.PullRequest.FromRef.LatestCommit
		processedEvent.PullRequestNumber = e.PullRequest.ID
		processedEvent.URL = e.PullRequest.ToRef.Repository.Links.Self[0].Href
		processedEvent.BaseBranch = e.PullRequest.ToRef.DisplayID
		processedEvent.HeadBranch = e.PullRequest.FromRef.DisplayID
		processedEvent.BaseURL = e.PullRequest.ToRef.Repository.Links.Self[0].Href
		processedEvent.HeadURL = e.PullRequest.FromRef.Repository.Links.Self[0].Href
		processedEvent.AccountID = fmt.Sprintf("%d", e.Actor.ID)
		processedEvent.Sender = e.Actor.Name
		for _, value := range e.PullRequest.FromRef.Repository.Links.Clone {
			if value.Name == "http" {
				processedEvent.CloneURL = value.Href
			}
		}
		v.pullRequestNumber = e.PullRequest.ID
	case *types.PushRequestEvent:
		processedEvent.TriggerTarget = triggertype.Push
		processedEvent.EventType = triggertype.Push.String()
		processedEvent.Organization = e.Repository.Project.Key
		processedEvent.Repository = e.Repository.Slug

		if len(e.Changes) == 0 {
			return nil, fmt.Errorf("push event contains no commits under 'changes'; cannot proceed")
		}

		// Check for branch deletion - if any change is a DELETE type with zero hash, skip processing
		for _, change := range e.Changes {
			if provider.IsZeroSHA(change.ToHash) && change.Type == "DELETE" {
				return nil, fmt.Errorf("branch delete event is not supported; cannot proceed")
			}
		}

		if len(e.Commits) == 0 {
			return nil, fmt.Errorf("push event contains no commits; cannot proceed")
		}

		// In Bitbucket Data Center, when a pull request is merged, it creates two commits in the repository:
		// 1. A merge commit, which is represented by `changes[0].ToHash`.
		// 2. The actual commit containing the changes from the source branch.
		//
		// However, the merge commit often does not contain any file changes itself,
		// which can cause issues when determining whether file modifications should trigger PipelineRuns.
		//
		// Typically, a regular (non-merge) commit has a single parent, but a merge commit has two parents:
		// - The first parent is the previous HEAD of the destination branch (the branch into which the PR was merged).
		// - The second parent is the HEAD of the source branch (the branch being merged).
		//
		// if we detect a merge commit, we will use the previous HEAD commit to get changes from the previous HEAD to the current HEAD.
		if len(e.Commits) > 1 && len(e.Commits[0].Parents) > 1 {
			v.previousHeadCommit = e.Commits[0].Parents[0].ID
		}

		processedEvent.SHA = e.Changes[0].ToHash
		processedEvent.URL = e.Repository.Links.Self[0].Href
		processedEvent.BaseBranch = e.Changes[0].RefID
		processedEvent.HeadBranch = e.Changes[0].RefID
		processedEvent.BaseURL = e.Repository.Links.Self[0].Href
		processedEvent.HeadURL = e.Repository.Links.Self[0].Href
		processedEvent.AccountID = fmt.Sprintf("%d", e.Actor.ID)
		processedEvent.Sender = e.Actor.Name
		// Should we care about clone via SSH or just only do HTTP clones?
		for _, value := range e.Repository.Links.Clone {
			if value.Name == "http" {
				processedEvent.CloneURL = value.Href
			}
		}
	default:
		return nil, fmt.Errorf("event %s is not supported", eventType)
	}

	v.projectKey = processedEvent.Organization
	processedEvent.Organization = sanitizeOwner(processedEvent.Organization)
	processedEvent.URL = sanitizeEventURL(processedEvent.URL)

	// TODO: is this the right way? I guess i have no way to know what is the
	// baseURL of a bitbucket data center unless there is something in the API?
	// remove everything after /project in the URL to get the basePath
	pURL, err := url.Parse(processedEvent.URL)
	if err != nil {
		return nil, err
	}

	v.baseURL = fmt.Sprintf("%s://%s", pURL.Scheme, pURL.Host)
	return processedEvent, nil
}

func parsePayloadType(event string) (any, error) {
	// bitbucket data center event type has `pr:` prefix for pull request
	// but in case of push event it is `repo:` prefix for both bitbucket data center
	// and cloud, so we check the event name directly
	var localEvent string
	if strings.HasPrefix(event, "pr:") {
		if !provider.Valid(event, []string{
			"pr:from_ref_updated", "pr:opened", "pr:comment:added", "pr:comment:edited", "pr:merged", "pr:modified", "pr:reviewer:approved",
		}) {
			return nil, fmt.Errorf("event \"%s\" is not supported", event)
		}
		localEvent = triggertype.PullRequest.String()
	} else if event == "repo:refs_changed" {
		localEvent = "push"
	}

	var intfType any
	switch localEvent {
	case triggertype.PullRequest.String():
		intfType = &types.PullRequestEvent{}
	case "push":
		intfType = &types.PushRequestEvent{}
	default:
		intfType = nil
	}
	return intfType, nil
}
