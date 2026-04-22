package cel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/forgejostructs"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// Helper functions (moved from utils package)

// getHeaderCaseInsensitive performs case-insensitive header lookup.
func getHeaderCaseInsensitive(headers map[string]string, key string) string {
	// First try exact match for performance
	if value, ok := headers[key]; ok {
		return value
	}
	// Then try case-insensitive match
	lowerKey := strings.ToLower(key)
	for k, v := range headers {
		if strings.ToLower(k) == lowerKey {
			return v
		}
	}
	return ""
}

// extractOrgFromPath extracts organization from GitLab-style path with namespace.
func extractOrgFromPath(pathWithNamespace string) string {
	parts := strings.Split(pathWithNamespace, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], "/")
	}
	return ""
}

// extractRepoFromPath extracts repository name from GitLab-style path with namespace.
func extractRepoFromPath(pathWithNamespace string) string {
	parts := strings.Split(pathWithNamespace, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// extractPullRequestNumber extracts pull request number from issue URL.
func extractPullRequestNumber(issueURL string) int {
	if issueURL == "" {
		return 0
	}
	parts := strings.Split(issueURL, "/")
	if len(parts) > 0 {
		if num, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return num
		}
	}
	return 0
}

// newCELEvent creates a new event structure for CEL evaluation with common boilerplate.
func newCELEvent(body []byte, headers map[string]string, eventTypeHeader string) *info.Event {
	event := info.NewEvent()
	event.EventType = getHeaderCaseInsensitive(headers, eventTypeHeader)
	event.Request.Payload = body
	event.Request.Header = http.Header{}

	// Copy all headers to the event request
	for k, v := range headers {
		event.Request.Header.Set(k, v)
	}

	return event
}

// WebhookParser defines the interface for provider-specific CEL webhook parsing.
type WebhookParser interface {
	// ParsePayload parses the webhook payload into a provider-specific event structure.
	ParsePayload(eventType string, body []byte) (any, error)

	// PopulateEvent extracts common fields from the provider-specific event structure
	// and populates the info.Event with the appropriate values.
	PopulateEvent(event *info.Event, parsedEvent any) error

	// GetEventTypeHeader returns the header name used by this provider to indicate
	// the event type (e.g., "X-GitHub-Event", "X-Gitlab-Event").
	GetEventTypeHeader() string
}

// parseWebhookForCEL provides a common implementation for CEL webhook parsing.
func parseWebhookForCEL(body []byte, headers map[string]string, parser WebhookParser) (*info.Event, error) {
	// Create the common event structure
	event := newCELEvent(body, headers, parser.GetEventTypeHeader())

	// Validate that the event type is present
	if event.EventType == "" {
		return nil, fmt.Errorf("unknown %s", parser.GetEventTypeHeader())
	}

	// Parse the payload using provider-specific logic
	parsedEvent, err := parser.ParsePayload(event.EventType, body)
	if err != nil {
		return nil, err
	}

	// Store the parsed event for CEL body access
	event.Event = parsedEvent

	// Populate the event fields using provider-specific logic
	if err := parser.PopulateEvent(event, parsedEvent); err != nil {
		return nil, err
	}

	return event, nil
}

// GitHub CEL Parser Implementation

type GitHubParser struct{}

func (p *GitHubParser) GetEventTypeHeader() string {
	return "X-Github-Event"
}

func (p *GitHubParser) ParsePayload(eventType string, body []byte) (any, error) {
	return github.ParseWebHook(eventType, body)
}

func (p *GitHubParser) PopulateEvent(event *info.Event, parsedEvent any) error {
	switch gitEvent := parsedEvent.(type) {
	case *github.PullRequestEvent:
		event.Organization = gitEvent.GetRepo().GetOwner().GetLogin()
		event.Repository = gitEvent.GetRepo().GetName()
		event.Sender = gitEvent.GetSender().GetLogin()
		event.URL = gitEvent.GetRepo().GetHTMLURL()
		event.SHA = gitEvent.GetPullRequest().GetHead().GetSHA()
		event.SHAURL = fmt.Sprintf("%s/commit/%s", gitEvent.GetPullRequest().GetHTMLURL(), gitEvent.GetPullRequest().GetHead().GetSHA())
		event.PullRequestTitle = gitEvent.GetPullRequest().GetTitle()
		event.HeadBranch = gitEvent.GetPullRequest().GetHead().GetRef()
		event.BaseBranch = gitEvent.GetPullRequest().GetBase().GetRef()
		event.HeadURL = gitEvent.GetPullRequest().GetHead().GetRepo().GetHTMLURL()
		event.BaseURL = gitEvent.GetPullRequest().GetBase().GetRepo().GetHTMLURL()
		event.DefaultBranch = gitEvent.GetRepo().GetDefaultBranch()
		event.PullRequestNumber = gitEvent.GetPullRequest().GetNumber()
		event.TriggerTarget = triggertype.PullRequest
		if gitEvent.GetPullRequest() != nil {
			for _, label := range gitEvent.GetPullRequest().Labels {
				event.PullRequestLabel = append(event.PullRequestLabel, label.GetName())
			}
		}
	case *github.PushEvent:
		event.Organization = gitEvent.GetRepo().GetOwner().GetLogin()
		event.Repository = gitEvent.GetRepo().GetName()
		event.Sender = gitEvent.GetSender().GetLogin()
		event.URL = gitEvent.GetRepo().GetHTMLURL()
		event.SHA = gitEvent.GetHeadCommit().GetID()
		event.SHAURL = gitEvent.GetHeadCommit().GetURL()
		event.SHATitle = gitEvent.GetHeadCommit().GetMessage()
		event.HeadBranch = gitEvent.GetRef()
		event.BaseBranch = gitEvent.GetRef()
		event.HeadURL = gitEvent.GetRepo().GetHTMLURL()
		event.BaseURL = gitEvent.GetRepo().GetHTMLURL()
		event.DefaultBranch = gitEvent.GetRepo().GetDefaultBranch()
		event.TriggerTarget = triggertype.Push
	case *github.IssueCommentEvent:
		if gitEvent.GetIssue().GetPullRequestLinks() == nil {
			return fmt.Errorf("issue comment is not from a pull request")
		}
		event.Organization = gitEvent.GetRepo().GetOwner().GetLogin()
		event.Repository = gitEvent.GetRepo().GetName()
		event.Sender = gitEvent.GetSender().GetLogin()
		event.URL = gitEvent.GetRepo().GetHTMLURL()
		event.DefaultBranch = gitEvent.GetRepo().GetDefaultBranch()
		event.TriggerTarget = triggertype.PullRequest
		event.TriggerComment = gitEvent.GetComment().GetBody()
		event.PullRequestNumber = gitEvent.GetIssue().GetNumber()
	case *github.CommitCommentEvent:
		event.Organization = gitEvent.GetRepo().GetOwner().GetLogin()
		event.Repository = gitEvent.GetRepo().GetName()
		event.Sender = gitEvent.GetSender().GetLogin()
		event.URL = gitEvent.GetRepo().GetHTMLURL()
		event.SHA = gitEvent.GetComment().GetCommitID()
		event.DefaultBranch = gitEvent.GetRepo().GetDefaultBranch()
		event.TriggerTarget = triggertype.Push
		event.TriggerComment = gitEvent.GetComment().GetBody()
	default:
		return fmt.Errorf("unsupported GitHub event type: %T", gitEvent)
	}
	return nil
}

// GitHubParserWithToken extends GitHubParser with API enrichment capabilities.
type GitHubParserWithToken struct {
	Token string
}

func (p *GitHubParserWithToken) GetEventTypeHeader() string {
	return "X-Github-Event"
}

func (p *GitHubParserWithToken) ParsePayload(eventType string, body []byte) (any, error) {
	return github.ParseWebHook(eventType, body)
}

func (p *GitHubParserWithToken) PopulateEvent(event *info.Event, parsedEvent any) error {
	// If no token provided, fallback to basic parsing
	if p.Token == "" {
		basicParser := &GitHubParser{}
		return basicParser.PopulateEvent(event, parsedEvent)
	}

	// Use the basic parser first to set up basic fields
	basicParser := &GitHubParser{}
	if err := basicParser.PopulateEvent(event, parsedEvent); err != nil {
		return err
	}

	// TODO: Add API enrichment logic here when token is provided
	// For now, we fallback to basic parsing to maintain compatibility
	return nil
}

// GitLab CEL Parser Implementation

type GitLabParser struct{}

func (p *GitLabParser) GetEventTypeHeader() string {
	return "X-Gitlab-Event"
}

func (p *GitLabParser) ParsePayload(eventType string, body []byte) (any, error) {
	return gitlab.ParseWebhook(gitlab.EventType(eventType), body)
}

func (p *GitLabParser) PopulateEvent(event *info.Event, parsedEvent any) error {
	switch gitEvent := parsedEvent.(type) {
	case *gitlab.MergeEvent:
		event.Organization = extractOrgFromPath(gitEvent.Project.PathWithNamespace)
		event.Repository = extractRepoFromPath(gitEvent.Project.PathWithNamespace)
		event.URL = gitEvent.Project.WebURL
		event.DefaultBranch = gitEvent.Project.DefaultBranch
		if gitEvent.User != nil {
			event.Sender = gitEvent.User.Username
		}
		event.SHA = gitEvent.ObjectAttributes.LastCommit.ID
		event.SHAURL = gitEvent.ObjectAttributes.LastCommit.URL
		event.SHATitle = gitEvent.ObjectAttributes.LastCommit.Title
		event.HeadBranch = gitEvent.ObjectAttributes.SourceBranch
		event.BaseBranch = gitEvent.ObjectAttributes.TargetBranch
		if gitEvent.ObjectAttributes.Source != nil {
			event.HeadURL = gitEvent.ObjectAttributes.Source.WebURL
		}
		if gitEvent.ObjectAttributes.Target != nil {
			event.BaseURL = gitEvent.ObjectAttributes.Target.WebURL
		}
		event.PullRequestNumber = int(gitEvent.ObjectAttributes.IID)
		event.PullRequestTitle = gitEvent.ObjectAttributes.Title
		event.TriggerTarget = triggertype.PullRequest
		if gitEvent.ObjectAttributes.Action == "close" {
			event.TriggerTarget = triggertype.PullRequestClosed
		}
		for _, label := range gitEvent.Labels {
			event.PullRequestLabel = append(event.PullRequestLabel, label.Title)
		}
	case *gitlab.PushEvent:
		if len(gitEvent.Commits) == 0 {
			return fmt.Errorf("no commits attached to this push event")
		}
		lastCommitIdx := len(gitEvent.Commits) - 1
		event.Organization = extractOrgFromPath(gitEvent.Project.PathWithNamespace)
		event.Repository = extractRepoFromPath(gitEvent.Project.PathWithNamespace)
		event.URL = gitEvent.Project.WebURL
		event.HeadURL = gitEvent.Project.WebURL
		event.BaseURL = gitEvent.Project.WebURL
		event.DefaultBranch = gitEvent.Project.DefaultBranch
		event.Sender = gitEvent.UserUsername
		if gitEvent.Commits[lastCommitIdx] != nil {
			event.SHA = gitEvent.Commits[lastCommitIdx].ID
			event.SHAURL = gitEvent.Commits[lastCommitIdx].URL
			event.SHATitle = gitEvent.Commits[lastCommitIdx].Title
		}
		event.HeadBranch = gitEvent.Ref
		event.BaseBranch = gitEvent.Ref
		event.TriggerTarget = triggertype.Push
	case *gitlab.TagEvent:
		if len(gitEvent.Commits) == 0 {
			return fmt.Errorf("no commits attached to this tag event")
		}
		lastCommitIdx := len(gitEvent.Commits) - 1
		event.Sender = gitEvent.UserUsername
		event.Organization = extractOrgFromPath(gitEvent.Project.PathWithNamespace)
		event.Repository = extractRepoFromPath(gitEvent.Project.PathWithNamespace)
		event.DefaultBranch = gitEvent.Project.DefaultBranch
		event.URL = gitEvent.Project.WebURL
		event.HeadURL = gitEvent.Project.WebURL
		event.BaseURL = gitEvent.Project.WebURL
		event.HeadBranch = gitEvent.Ref
		event.BaseBranch = gitEvent.Ref
		if gitEvent.Commits[lastCommitIdx] != nil {
			event.SHA = gitEvent.Commits[lastCommitIdx].ID
			event.SHAURL = gitEvent.Commits[lastCommitIdx].URL
			event.SHATitle = gitEvent.Commits[lastCommitIdx].Title
		}
		event.TriggerTarget = triggertype.Push
	default:
		return fmt.Errorf("unsupported GitLab event type: %T", gitEvent)
	}
	return nil
}

// Bitbucket Cloud CEL Parser Implementation

type BitbucketCloudParser struct{}

func (p *BitbucketCloudParser) GetEventTypeHeader() string {
	return "X-Event-Key"
}

func (p *BitbucketCloudParser) ParsePayload(eventType string, body []byte) (any, error) {
	switch {
	case strings.HasPrefix(eventType, "pullrequest:"):
		var prEvent types.PullRequestEvent
		if err := json.Unmarshal(body, &prEvent); err != nil {
			return nil, fmt.Errorf("failed to parse Bitbucket Cloud pull request event: %w", err)
		}
		return &prEvent, nil
	case eventType == "repo:push":
		var pushEvent types.PushRequestEvent
		if err := json.Unmarshal(body, &pushEvent); err != nil {
			return nil, fmt.Errorf("failed to parse Bitbucket Cloud push event: %w", err)
		}
		return &pushEvent, nil
	default:
		return nil, fmt.Errorf("unsupported Bitbucket Cloud event type: %s", eventType)
	}
}

func (p *BitbucketCloudParser) PopulateEvent(event *info.Event, parsedEvent any) error {
	switch e := parsedEvent.(type) {
	case *types.PullRequestEvent:
		event.Organization = e.Repository.Workspace.Slug
		repoParts := strings.Split(e.Repository.FullName, "/")
		if len(repoParts) > 1 {
			event.Repository = repoParts[1]
		} else {
			event.Repository = e.Repository.FullName
		}
		event.Sender = e.PullRequest.Author.Nickname
		event.URL = e.Repository.Links.HTML.HRef
		event.SHA = e.PullRequest.Source.Commit.Hash
		event.HeadBranch = e.PullRequest.Source.Branch.Name
		event.BaseBranch = e.PullRequest.Destination.Branch.Name
		event.HeadURL = e.PullRequest.Source.Repository.Links.HTML.HRef
		event.BaseURL = e.PullRequest.Destination.Repository.Links.HTML.HRef
		event.PullRequestNumber = e.PullRequest.ID
		event.PullRequestTitle = e.PullRequest.Title
		event.TriggerTarget = triggertype.PullRequest
		if event.EventType == "pullrequest:rejected" || event.EventType == "pullrequest:fulfilled" {
			event.TriggerTarget = triggertype.PullRequestClosed
		}
		// Handle comment events
		if e.Comment.Content.Raw != "" {
			event.TriggerComment = e.Comment.Content.Raw
		}
	case *types.PushRequestEvent:
		event.Organization = e.Repository.Workspace.Slug
		repoParts := strings.Split(e.Repository.FullName, "/")
		if len(repoParts) > 1 {
			event.Repository = repoParts[1]
		} else {
			event.Repository = e.Repository.FullName
		}
		event.Sender = e.Actor.Nickname
		event.URL = e.Repository.Links.HTML.HRef
		if len(e.Push.Changes) > 0 {
			event.SHA = e.Push.Changes[0].New.Target.Hash
			event.HeadBranch = e.Push.Changes[0].New.Name
			event.BaseBranch = e.Push.Changes[0].New.Name
			event.HeadURL = e.Repository.Links.HTML.HRef
			event.BaseURL = e.Repository.Links.HTML.HRef
		}
		event.TriggerTarget = triggertype.Push
	default:
		return fmt.Errorf("unsupported Bitbucket Cloud event type: %T", e)
	}
	return nil
}

// Bitbucket Data Center CEL Parser Implementation

type BitbucketDataCenterParser struct{}

func (p *BitbucketDataCenterParser) GetEventTypeHeader() string {
	return "X-Event-Key"
}

func (p *BitbucketDataCenterParser) ParsePayload(eventType string, body []byte) (any, error) {
	switch {
	case strings.HasPrefix(eventType, "pr:"):
		// Parse as a generic pull request event structure
		var prData map[string]any
		if err := json.Unmarshal(body, &prData); err != nil {
			return nil, fmt.Errorf("failed to parse Bitbucket Data Center pull request event: %w", err)
		}
		return prData, nil
	case eventType == "repo:refs_changed":
		// Parse as a generic push event structure
		var pushData map[string]any
		if err := json.Unmarshal(body, &pushData); err != nil {
			return nil, fmt.Errorf("failed to parse Bitbucket Data Center push event: %w", err)
		}
		return pushData, nil
	default:
		return nil, fmt.Errorf("unsupported Bitbucket Data Center event type: %s", eventType)
	}
}

// sanitizeOwner remove ~ from OWNER in case of personal repos.
func sanitizeOwner(owner string) string {
	return strings.ReplaceAll(owner, "~", "")
}

// sanitizeEventURL returns the URL to the event without the /browse.
func sanitizeEventURL(eventURL string) string {
	if strings.HasSuffix(eventURL, "/browse") {
		return eventURL[:len(eventURL)-len("/browse")]
	}
	return eventURL
}

func (p *BitbucketDataCenterParser) PopulateEvent(event *info.Event, parsedEvent any) error {
	data, ok := parsedEvent.(map[string]any)
	if !ok {
		return fmt.Errorf("expected map[string]any for Bitbucket Data Center event, got %T", parsedEvent)
	}

	switch {
	case strings.HasPrefix(event.EventType, "pr:"):
		// Extract basic information from the pull request payload structure
		if pullRequest, ok := data["pullRequest"].(map[string]any); ok {
			if toRef, ok := pullRequest["toRef"].(map[string]any); ok {
				if repository, ok := toRef["repository"].(map[string]any); ok {
					if project, ok := repository["project"].(map[string]any); ok {
						if key, ok := project["key"].(string); ok {
							event.Organization = sanitizeOwner(key)
						}
					}
					if name, ok := repository["name"].(string); ok {
						event.Repository = name
					}
					if links, ok := repository["links"].(map[string]any); ok {
						if self, ok := links["self"].([]any); ok && len(self) > 0 {
							if selfLink, ok := self[0].(map[string]any); ok {
								if href, ok := selfLink["href"].(string); ok {
									event.URL = sanitizeEventURL(href)
									event.BaseURL = sanitizeEventURL(href)
								}
							}
						}
					}
				}
				if displayID, ok := toRef["displayId"].(string); ok {
					event.BaseBranch = displayID
				}
			}
			if fromRef, ok := pullRequest["fromRef"].(map[string]any); ok {
				if displayID, ok := fromRef["displayId"].(string); ok {
					event.HeadBranch = displayID
				}
				if latestCommit, ok := fromRef["latestCommit"].(string); ok {
					event.SHA = latestCommit
				}
				if repository, ok := fromRef["repository"].(map[string]any); ok {
					if links, ok := repository["links"].(map[string]any); ok {
						if self, ok := links["self"].([]any); ok && len(self) > 0 {
							if selfLink, ok := self[0].(map[string]any); ok {
								if href, ok := selfLink["href"].(string); ok {
									event.HeadURL = sanitizeEventURL(href)
								}
							}
						}
					}
				}
			}
			if id, ok := pullRequest["id"].(float64); ok {
				event.PullRequestNumber = int(id)
			}
			if title, ok := pullRequest["title"].(string); ok {
				event.PullRequestTitle = title
			}
		}
		if actor, ok := data["actor"].(map[string]any); ok {
			if name, ok := actor["name"].(string); ok {
				event.Sender = name
			}
		}
		// Handle comment events
		if comment, ok := data["comment"].(map[string]any); ok {
			if text, ok := comment["text"].(string); ok {
				event.TriggerComment = text
			}
		}
		event.TriggerTarget = triggertype.PullRequest
	case event.EventType == "repo:refs_changed":
		// Extract basic information from push event
		if repository, ok := data["repository"].(map[string]any); ok {
			if project, ok := repository["project"].(map[string]any); ok {
				if key, ok := project["key"].(string); ok {
					event.Organization = sanitizeOwner(key)
				}
			}
			if name, ok := repository["name"].(string); ok {
				event.Repository = name
			}
			if links, ok := repository["links"].(map[string]any); ok {
				if self, ok := links["self"].([]any); ok && len(self) > 0 {
					if selfLink, ok := self[0].(map[string]any); ok {
						if href, ok := selfLink["href"].(string); ok {
							event.URL = sanitizeEventURL(href)
							event.BaseURL = sanitizeEventURL(href)
							event.HeadURL = sanitizeEventURL(href)
						}
					}
				}
			}
		}
		if actor, ok := data["actor"].(map[string]any); ok {
			if name, ok := actor["name"].(string); ok {
				event.Sender = name
			}
		}
		if changes, ok := data["changes"].([]any); ok && len(changes) > 0 {
			if change, ok := changes[0].(map[string]any); ok {
				if toHash, ok := change["toHash"].(string); ok {
					event.SHA = toHash
				}
				if refID, ok := change["refId"].(string); ok {
					event.HeadBranch = refID
					event.BaseBranch = refID
				}
			}
		}
		event.TriggerTarget = triggertype.Push
	default:
		return fmt.Errorf("unsupported Bitbucket Data Center event type: %s", event.EventType)
	}
	return nil
}

// Gitea CEL Parser Implementation

type GiteaParser struct{}

func populateGiteaPullRequestFields(event *info.Event, pr *forgejostructs.PullRequest) {
	if pr == nil {
		return
	}

	event.PullRequestTitle = pr.Title
	if pr.Head != nil {
		event.SHA = pr.Head.Sha
		if pr.HTMLURL != "" && pr.Head.Sha != "" {
			event.SHAURL = fmt.Sprintf("%s/commit/%s", pr.HTMLURL, pr.Head.Sha)
		}
		event.HeadBranch = pr.Head.Ref
		if pr.Head.Repository != nil {
			event.HeadURL = pr.Head.Repository.HTMLURL
		}
	}
	if pr.Base != nil {
		event.BaseBranch = pr.Base.Ref
		if pr.Base.Repository != nil {
			event.BaseURL = pr.Base.Repository.HTMLURL
		}
	}
}

func (p *GiteaParser) GetEventTypeHeader() string {
	return "X-Gitea-Event-Type"
}

type ForgejoParser struct {
	GiteaParser
}

func (p *ForgejoParser) GetEventTypeHeader() string {
	return "X-Forgejo-Event-Type"
}

func (p *GiteaParser) ParsePayload(eventType string, body []byte) (any, error) {
	var eventInt any
	switch eventType {
	case "push":
		eventInt = &forgejostructs.PushPayload{}
	case "pull_request":
		eventInt = &forgejostructs.PullRequestPayload{}
	case "issue_comment", "pull_request_comment":
		eventInt = &forgejostructs.IssueCommentPayload{}
	default:
		return nil, fmt.Errorf("unsupported Gitea event type: %s", eventType)
	}

	// Parse the payload into the eventInt interface
	if err := json.Unmarshal(body, &eventInt); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gitea payload: %w", err)
	}

	return eventInt, nil
}

func (p *GiteaParser) PopulateEvent(event *info.Event, parsedEvent any) error {
	switch gitEvent := parsedEvent.(type) {
	case *forgejostructs.PullRequestPayload:
		if gitEvent.Repository != nil {
			if gitEvent.Repository.Owner != nil {
				event.Organization = gitEvent.Repository.Owner.UserName
			}
			event.Repository = gitEvent.Repository.Name
			event.URL = gitEvent.Repository.HTMLURL
			event.DefaultBranch = gitEvent.Repository.DefaultBranch
		}
		if gitEvent.Sender != nil {
			event.Sender = gitEvent.Sender.UserName
		}
		if gitEvent.PullRequest != nil {
			populateGiteaPullRequestFields(event, gitEvent.PullRequest)
			event.PullRequestNumber = int(gitEvent.Index)
			for _, label := range gitEvent.PullRequest.Labels {
				if label != nil {
					event.PullRequestLabel = append(event.PullRequestLabel, label.Name)
				}
			}
		}
		event.TriggerTarget = triggertype.PullRequest
		if gitEvent.Action == forgejostructs.HookIssueClosed {
			event.TriggerTarget = triggertype.PullRequestClosed
		}
	case *forgejostructs.PushPayload:
		if gitEvent.Repo != nil {
			if gitEvent.Repo.Owner != nil {
				event.Organization = gitEvent.Repo.Owner.UserName
			}
			event.Repository = gitEvent.Repo.Name
			event.URL = gitEvent.Repo.HTMLURL
			event.HeadURL = gitEvent.Repo.HTMLURL
			event.BaseURL = gitEvent.Repo.HTMLURL
			event.DefaultBranch = gitEvent.Repo.DefaultBranch
		}
		if gitEvent.Sender != nil {
			event.Sender = gitEvent.Sender.UserName
		}
		if gitEvent.HeadCommit != nil {
			event.SHA = gitEvent.HeadCommit.ID
			if event.SHA == "" {
				event.SHA = gitEvent.Before
			}
			event.SHAURL = gitEvent.HeadCommit.URL
			event.SHATitle = gitEvent.HeadCommit.Message
		} else if gitEvent.Before != "" {
			event.SHA = gitEvent.Before
		}
		event.HeadBranch = gitEvent.Ref
		event.BaseBranch = gitEvent.Ref
		event.TriggerTarget = triggertype.Push
	case *forgejostructs.IssueCommentPayload:
		issue := gitEvent.Issue
		if issue == nil || issue.PullRequest == nil {
			return fmt.Errorf("issue comment is not from a pull request")
		}
		if gitEvent.Repository != nil {
			if gitEvent.Repository.Owner != nil {
				event.Organization = gitEvent.Repository.Owner.UserName
			}
			event.Repository = gitEvent.Repository.Name
			event.URL = gitEvent.Repository.HTMLURL
			event.DefaultBranch = gitEvent.Repository.DefaultBranch
		}
		if gitEvent.Sender != nil {
			event.Sender = gitEvent.Sender.UserName
		}
		event.TriggerTarget = triggertype.PullRequest
		if gitEvent.Comment != nil {
			event.TriggerComment = gitEvent.Comment.Body
		}
		event.PullRequestNumber = extractPullRequestNumber(issue.URL)
		populateGiteaPullRequestFields(event, gitEvent.PullRequest)
		if event.PullRequestNumber == 0 && gitEvent.PullRequest != nil {
			event.PullRequestNumber = int(gitEvent.PullRequest.Index)
		}
	default:
		return fmt.Errorf("unsupported Gitea event type: %T", gitEvent)
	}
	return nil
}
