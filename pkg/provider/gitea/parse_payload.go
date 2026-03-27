package gitea

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/forgejostructs"
)

func populateEventFromGiteaPullRequest(event *info.Event, pr *forgejostructs.PullRequest) {
	if pr == nil {
		return
	}

	event.PullRequestTitle = pr.Title
	if pr.Head != nil {
		event.SHA = pr.Head.Sha
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
	if pr.HTMLURL != "" && event.SHA != "" {
		event.SHAURL = fmt.Sprintf("%s/commit/%s", pr.HTMLURL, event.SHA)
	}
}

func (v *Provider) ParsePayload(_ context.Context, _ *params.Run, request *http.Request,
	payload string,
) (*info.Event, error) {
	// TODO: parse request to figure out which event
	var processedEvent *info.Event

	eventType := request.Header.Get("X-Gitea-Event-Type")
	if eventType == "" {
		return nil, fmt.Errorf("failed to find event type in request header")
	}

	payloadB := []byte(payload)
	eventInt, err := parseWebhook(whEventType(eventType), payloadB)
	if err != nil {
		return nil, err
	}

	switch gitEvent := eventInt.(type) {
	case *forgejostructs.PullRequestPayload:
		processedEvent = info.NewEvent()
		// // Organization:  event.GetRepo().GetOwner().GetLogin(),
		if gitEvent.Sender != nil {
			processedEvent.Sender = gitEvent.Sender.UserName
		}
		if gitEvent.Repository != nil {
			processedEvent.DefaultBranch = gitEvent.Repository.DefaultBranch
			processedEvent.URL = gitEvent.Repository.HTMLURL
			processedEvent.Repository = gitEvent.Repository.Name
			if gitEvent.Repository.Owner != nil {
				processedEvent.Organization = gitEvent.Repository.Owner.UserName
			}
		}
		populateEventFromGiteaPullRequest(processedEvent, gitEvent.PullRequest)
		processedEvent.PullRequestNumber = int(gitEvent.Index)
		processedEvent.TriggerTarget = triggertype.PullRequest
		processedEvent.EventType = triggertype.PullRequest.String()
		if provider.Valid(string(gitEvent.Action), []string{pullRequestLabelUpdated}) {
			processedEvent.EventType = string(triggertype.PullRequestLabeled)
		}
		if gitEvent.PullRequest != nil {
			for _, label := range gitEvent.PullRequest.Labels {
				if label == nil {
					continue
				}
				processedEvent.PullRequestLabel = append(processedEvent.PullRequestLabel, label.Name)
			}
		}
		if gitEvent.Action == forgejostructs.HookIssueClosed {
			processedEvent.TriggerTarget = triggertype.PullRequestClosed
		}
	case *forgejostructs.PushPayload:
		processedEvent = info.NewEvent()
		if gitEvent.HeadCommit != nil {
			processedEvent.SHA = gitEvent.HeadCommit.ID
			processedEvent.SHAURL = gitEvent.HeadCommit.URL
			processedEvent.SHATitle = gitEvent.HeadCommit.Message
		}
		if processedEvent.SHA == "" {
			processedEvent.SHA = gitEvent.Before
		}
		if gitEvent.Repo != nil {
			if gitEvent.Repo.Owner != nil {
				processedEvent.Organization = gitEvent.Repo.Owner.UserName
			}
			processedEvent.Repository = gitEvent.Repo.Name
			processedEvent.DefaultBranch = gitEvent.Repo.DefaultBranch
			processedEvent.URL = gitEvent.Repo.HTMLURL
			processedEvent.BaseURL = gitEvent.Repo.HTMLURL
			processedEvent.HeadURL = processedEvent.BaseURL // in push events Head URL is the same as BaseURL
		}
		if gitEvent.Sender != nil {
			processedEvent.Sender = gitEvent.Sender.UserName
		}
		processedEvent.BaseBranch = gitEvent.Ref
		processedEvent.EventType = eventType
		processedEvent.HeadBranch = processedEvent.BaseBranch // in push events Head Branch is the same as Basebranch
		processedEvent.TriggerTarget = "push"
	case *forgejostructs.IssueCommentPayload:
		if gitEvent.Issue == nil || gitEvent.Issue.PullRequest == nil {
			return info.NewEvent(), fmt.Errorf("issue comment is not coming from a pull_request")
		}
		processedEvent = info.NewEvent()
		if gitEvent.Repository != nil {
			if gitEvent.Repository.Owner != nil {
				processedEvent.Organization = gitEvent.Repository.Owner.UserName
			}
			processedEvent.Repository = gitEvent.Repository.Name
			processedEvent.URL = gitEvent.Repository.HTMLURL
			processedEvent.DefaultBranch = gitEvent.Repository.DefaultBranch
		}
		if gitEvent.Sender != nil {
			processedEvent.Sender = gitEvent.Sender.UserName
		}
		processedEvent.TriggerTarget = triggertype.PullRequest
		if gitEvent.Comment != nil {
			opscomments.SetEventTypeAndTargetPR(processedEvent, gitEvent.Comment.Body, "/")
		}
		populateEventFromGiteaPullRequest(processedEvent, gitEvent.PullRequest)
		if gitEvent.Issue.URL != "" {
			processedEvent.PullRequestNumber, err = convertPullRequestURLtoNumber(gitEvent.Issue.URL)
			if err != nil {
				if gitEvent.PullRequest == nil || gitEvent.PullRequest.Index == 0 {
					return nil, err
				}
				processedEvent.PullRequestNumber = int(gitEvent.PullRequest.Index)
			}
		} else if gitEvent.PullRequest != nil {
			processedEvent.PullRequestNumber = int(gitEvent.PullRequest.Index)
		}
	default:
		return nil, fmt.Errorf("event %s is not supported", eventType)
	}

	processedEvent.Event = eventInt
	return processedEvent, nil
}
