package bitbucketcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
)

const bitbucketCloudIPrangesList = "https://ip-ranges.atlassian.com/"

// lastForwarderForIP get last ip from the X-Forwarded-For chain
// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For
func lastForwarderForIP(xff string) string {
	split := strings.Split(xff, ",")
	return split[len(split)-1]
}

// checkFromPublicCloudIPS Grab public IP from public cloud and make sure we match it.
func (v *Provider) checkFromPublicCloudIPS(ctx context.Context, run *params.Run, sourceIP string) (bool, error) {
	if !v.pacInfo.BitbucketCloudCheckSourceIP {
		return true, nil
	}

	if sourceIP == "" {
		return false, fmt.Errorf("we need to check the source_ip but no source_ip has been passed")
	}
	sourceIP = lastForwarderForIP(sourceIP)

	netsourceIP := net.ParseIP(sourceIP)
	data, err := run.Clients.GetURL(ctx, bitbucketCloudIPrangesList)
	if err != nil {
		return false, err
	}

	ipranges := &types.IPRanges{}
	err = json.Unmarshal(data, &ipranges)
	if err != nil {
		return false, err
	}

	extraIPEnv := v.pacInfo.BitbucketCloudAdditionalSourceIP
	if extraIPEnv != "" {
		for _, value := range strings.Split(extraIPEnv, ",") {
			if !strings.Contains(value, "/") {
				value = fmt.Sprintf("%s/32", value)
			}
			ipranges.Items = append(ipranges.Items, types.IPRangesItem{
				CIDR: strings.TrimSpace(value),
			})
		}
	}
	for _, value := range ipranges.Items {
		_, cidr, err := net.ParseCIDR(value.CIDR)
		if err != nil {
			return false, err
		}
		if cidr.Contains(netsourceIP) {
			return true, nil
		}
	}
	return false,
		fmt.Errorf("payload from %s is not coming from the public bitbucket cloud ips as defined here: %s",
			sourceIP, bitbucketCloudIPrangesList)
}

func parsePayloadType(event, rawPayload string) (any, error) {
	var payload any

	var localEvent string
	if strings.HasPrefix(event, "pullrequest:") {
		if !provider.Valid(event, PullRequestAllEvents) {
			return nil, fmt.Errorf("event %s is not supported", event)
		}
		localEvent = triggertype.PullRequest.String()
	} else if provider.Valid(event, pushRepo) {
		localEvent = "push"
	}

	switch localEvent {
	case triggertype.PullRequest.String():
		payload = &types.PullRequestEvent{}
	case "push":
		payload = &types.PushRequestEvent{}
	default:
		return nil, nil
	}
	err := json.Unmarshal([]byte(rawPayload), payload)
	return payload, err
}

func (v *Provider) ParsePayload(ctx context.Context, run *params.Run, request *http.Request, payload string) (*info.Event, error) {
	processedEvent := info.NewEvent()

	event := request.Header.Get("X-Event-Key")
	eventInt, err := parsePayloadType(event, payload)
	if err != nil || eventInt == nil {
		return info.NewEvent(), err
	}

	err = json.Unmarshal([]byte(payload), &eventInt)
	if err != nil {
		return info.NewEvent(), err
	}

	sourceIP := request.Header.Get("X-Forwarded-For")
	allowed, err := v.checkFromPublicCloudIPS(ctx, run, sourceIP)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("payload is not coming from the public bitbucket cloud ips as defined here: %s",
			bitbucketCloudIPrangesList)
	}

	processedEvent.Event = eventInt
	switch e := eventInt.(type) {
	case *types.PullRequestEvent:
		processedEvent.TriggerTarget = triggertype.PullRequest
		switch {
		case provider.Valid(event, pullRequestsCreated):
			processedEvent.EventType = triggertype.PullRequest.String()
		case provider.Valid(event, pullRequestsCommentCreated):
			opscomments.SetEventTypeAndTargetPR(processedEvent, e.Comment.Content.Raw, "/")
		case provider.Valid(event, pullRequestsClosed):
			processedEvent.EventType = string(triggertype.PullRequestClosed)
			processedEvent.TriggerTarget = triggertype.PullRequestClosed
		}
		processedEvent.Organization = e.Repository.Workspace.Slug
		processedEvent.Repository = strings.Split(e.Repository.FullName, "/")[1]
		processedEvent.SHA = e.PullRequest.Source.Commit.Hash
		processedEvent.URL = e.Repository.Links.HTML.HRef
		processedEvent.BaseBranch = e.PullRequest.Destination.Branch.Name
		processedEvent.HeadBranch = e.PullRequest.Source.Branch.Name
		processedEvent.BaseURL = e.PullRequest.Destination.Repository.Links.HTML.HRef
		processedEvent.HeadURL = e.PullRequest.Source.Repository.Links.HTML.HRef
		processedEvent.AccountID = e.PullRequest.Author.AccountID
		processedEvent.Sender = e.PullRequest.Author.Nickname
		processedEvent.PullRequestNumber = e.PullRequest.ID
		processedEvent.PullRequestTitle = e.PullRequest.Title
	case *types.PushRequestEvent:
		processedEvent.Event = "push"
		processedEvent.TriggerTarget = "push"
		processedEvent.EventType = "push"
		processedEvent.Organization = e.Repository.Workspace.Slug
		processedEvent.Repository = strings.Split(e.Repository.FullName, "/")[1]
		processedEvent.SHA = e.Push.Changes[0].New.Target.Hash
		processedEvent.URL = e.Repository.Links.HTML.HRef
		processedEvent.HeadBranch = e.Push.Changes[0].Old.Name
		processedEvent.BaseURL = e.Push.Changes[0].New.Target.Links.HTML.HRef
		processedEvent.HeadURL = e.Push.Changes[0].Old.Target.Links.HTML.HRef
		if e.Push.Changes[0].New.Type == "tag" {
			processedEvent.BaseBranch = fmt.Sprintf("refs/tags/%s", e.Push.Changes[0].New.Name)
		} else {
			processedEvent.BaseBranch = e.Push.Changes[0].New.Name
		}
		processedEvent.AccountID = e.Actor.AccountID
		processedEvent.Sender = e.Actor.Nickname
	default:
		return nil, fmt.Errorf("event %s is not recognized", event)
	}
	return processedEvent, nil
}
