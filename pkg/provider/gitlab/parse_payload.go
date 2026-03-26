package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (v *Provider) ParsePayload(ctx context.Context, run *params.Run, request *http.Request,
	payload string,
) (*info.Event, error) {
	event := request.Header.Get("X-Gitlab-Event")
	if event == "" {
		return nil, fmt.Errorf("failed to find event type in request header")
	}

	payloadB := []byte(payload)
	eventInt, err := gitlab.ParseWebhook(gitlab.EventType(event), payloadB)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(payloadB, &eventInt)

	// Remove the " Hook" suffix so looks better in status, and since we don't
	// really use it anymore we good to do whatever we want with it for
	// cosmetics.
	processedEvent := info.NewEvent()
	processedEvent.EventType = strings.ReplaceAll(event, " Hook", "")
	processedEvent.Event = eventInt
	switch gitEvent := eventInt.(type) {
	case *gitlab.MergeEvent:
		// Organization:  event.GetRepo().GetOwner().GetLogin(),
		processedEvent.Sender = gitEvent.User.Username
		processedEvent.DefaultBranch = gitEvent.Project.DefaultBranch
		processedEvent.URL = gitEvent.Project.WebURL
		processedEvent.SHA = gitEvent.ObjectAttributes.LastCommit.ID
		processedEvent.SHAURL = gitEvent.ObjectAttributes.LastCommit.URL
		processedEvent.SHATitle = gitEvent.ObjectAttributes.LastCommit.Title
		processedEvent.SHAMessage = gitEvent.ObjectAttributes.LastCommit.Message
		processedEvent.HeadBranch = gitEvent.ObjectAttributes.SourceBranch
		processedEvent.BaseBranch = gitEvent.ObjectAttributes.TargetBranch
		processedEvent.HeadURL = gitEvent.ObjectAttributes.Source.WebURL
		processedEvent.BaseURL = gitEvent.ObjectAttributes.Target.WebURL
		processedEvent.PullRequestNumber = int(gitEvent.ObjectAttributes.IID)
		processedEvent.PullRequestTitle = gitEvent.ObjectAttributes.Title
		v.targetProjectID = gitEvent.Project.ID
		v.sourceProjectID = gitEvent.ObjectAttributes.SourceProjectID
		v.userID = gitEvent.User.ID

		v.pathWithNamespace = gitEvent.ObjectAttributes.Target.PathWithNamespace
		processedEvent.Organization, processedEvent.Repository = getOrgRepo(v.pathWithNamespace)
		processedEvent.SourceProjectID = gitEvent.ObjectAttributes.SourceProjectID
		processedEvent.TargetProjectID = gitEvent.Project.ID

		processedEvent.TriggerTarget = triggertype.PullRequest
		processedEvent.EventType = strings.ReplaceAll(event, " Hook", "")

		// This is a label update, like adding or removing a label from a MR.
		if gitEvent.Changes.Labels.Current != nil {
			processedEvent.EventType = triggertype.PullRequestLabeled.String()
		}
		for _, label := range gitEvent.Labels {
			processedEvent.PullRequestLabel = append(processedEvent.PullRequestLabel, label.Title)
		}
		if gitEvent.ObjectAttributes.Action == "close" {
			processedEvent.TriggerTarget = triggertype.PullRequestClosed
		}
	case *gitlab.TagEvent:
		// GitLab sends same event for both Tag creation and deletion i.e. "Tag Push Hook".
		// if gitEvent.After is containing all zeros and gitEvent.CheckoutSHA is empty
		// it is Delete "Tag Push Hook".
		if isZeroSHA(gitEvent.After) && gitEvent.CheckoutSHA == "" {
			return nil, fmt.Errorf("event Delete %s is not supported", event)
		}

		// sometime in gitlab tag push event contains no commit
		// in this case we're not supposed to process the event.
		if len(gitEvent.Commits) == 0 {
			return nil, fmt.Errorf("no commits attached to this %s event", event)
		}

		lastCommitIdx := len(gitEvent.Commits) - 1
		processedEvent.Sender = gitEvent.UserUsername
		processedEvent.DefaultBranch = gitEvent.Project.DefaultBranch
		processedEvent.URL = gitEvent.Project.WebURL
		processedEvent.SHA = gitEvent.Commits[lastCommitIdx].ID
		processedEvent.SHAURL = gitEvent.Commits[lastCommitIdx].URL
		processedEvent.SHATitle = gitEvent.Commits[lastCommitIdx].Title
		processedEvent.HeadBranch = gitEvent.Ref
		processedEvent.BaseBranch = gitEvent.Ref
		processedEvent.HeadURL = gitEvent.Project.WebURL
		processedEvent.BaseURL = processedEvent.HeadURL
		processedEvent.TriggerTarget = "push"
		v.pathWithNamespace = gitEvent.Project.PathWithNamespace
		processedEvent.Organization, processedEvent.Repository = getOrgRepo(v.pathWithNamespace)
		v.targetProjectID = gitEvent.ProjectID
		v.sourceProjectID = gitEvent.ProjectID
		v.userID = gitEvent.UserID
		processedEvent.SourceProjectID = gitEvent.ProjectID
		processedEvent.TargetProjectID = gitEvent.ProjectID
		processedEvent.EventType = strings.ReplaceAll(event, " Hook", "")
	case *gitlab.PushEvent:
		if len(gitEvent.Commits) == 0 {
			return nil, fmt.Errorf("no commits attached to this push event")
		}
		lastCommitIdx := len(gitEvent.Commits) - 1
		processedEvent.Sender = gitEvent.UserUsername
		processedEvent.DefaultBranch = gitEvent.Project.DefaultBranch
		processedEvent.URL = gitEvent.Project.WebURL
		processedEvent.SHA = gitEvent.Commits[lastCommitIdx].ID
		processedEvent.SHAURL = gitEvent.Commits[lastCommitIdx].URL
		processedEvent.SHATitle = gitEvent.Commits[lastCommitIdx].Title
		processedEvent.HeadBranch = gitEvent.Ref
		processedEvent.BaseBranch = gitEvent.Ref
		processedEvent.HeadURL = gitEvent.Project.WebURL
		processedEvent.BaseURL = processedEvent.HeadURL
		processedEvent.TriggerTarget = "push"
		v.pathWithNamespace = gitEvent.Project.PathWithNamespace
		processedEvent.Organization, processedEvent.Repository = getOrgRepo(v.pathWithNamespace)
		v.targetProjectID = gitEvent.ProjectID
		v.sourceProjectID = gitEvent.ProjectID
		v.userID = gitEvent.UserID
		processedEvent.SourceProjectID = gitEvent.ProjectID
		processedEvent.TargetProjectID = gitEvent.ProjectID
		processedEvent.EventType = strings.ToLower(strings.ReplaceAll(event, " Hook", ""))
	case *gitlab.MergeCommentEvent:
		processedEvent.Sender = gitEvent.User.Username
		processedEvent.DefaultBranch = gitEvent.Project.DefaultBranch
		processedEvent.URL = gitEvent.Project.WebURL
		processedEvent.SHA = gitEvent.MergeRequest.LastCommit.ID
		processedEvent.SHAURL = gitEvent.MergeRequest.LastCommit.URL
		processedEvent.SHATitle = gitEvent.MergeRequest.LastCommit.Title
		processedEvent.BaseBranch = gitEvent.MergeRequest.TargetBranch
		processedEvent.HeadBranch = gitEvent.MergeRequest.SourceBranch
		processedEvent.BaseURL = gitEvent.MergeRequest.Target.WebURL
		processedEvent.HeadURL = gitEvent.MergeRequest.Source.WebURL

		opscomments.SetEventTypeAndTargetPR(processedEvent, gitEvent.ObjectAttributes.Note, "/")
		v.pathWithNamespace = gitEvent.Project.PathWithNamespace
		processedEvent.Organization, processedEvent.Repository = getOrgRepo(v.pathWithNamespace)
		processedEvent.TriggerTarget = triggertype.PullRequest

		processedEvent.PullRequestNumber = int(gitEvent.MergeRequest.IID)
		v.targetProjectID = gitEvent.MergeRequest.TargetProjectID
		v.sourceProjectID = gitEvent.MergeRequest.SourceProjectID
		v.userID = gitEvent.User.ID
		processedEvent.SourceProjectID = gitEvent.MergeRequest.SourceProjectID
		processedEvent.TargetProjectID = gitEvent.MergeRequest.TargetProjectID
	case *gitlab.CommitCommentEvent:
		// need run in fetching repository
		v.run = run
		return v.handleCommitCommentEvent(ctx, gitEvent, processedEvent)
	default:
		return nil, fmt.Errorf("event %s is not supported", event)
	}

	v.repoURL = processedEvent.URL
	return processedEvent, nil
}

func (v *Provider) initGitLabClient(ctx context.Context, event *info.Event) (*info.Event, error) {
	// This is to ensure the base URL of the client is not reinitialized during tests.
	if v.gitlabClient != nil {
		return event, nil
	}

	// need repo here to get secret info and create gitlab api client
	repo, err := matcher.MatchEventURLRepo(ctx, v.run, event, "")
	if err != nil {
		return event, err
	}

	if repo == nil {
		return event, fmt.Errorf("cannot find a repository match for %s", event.URL)
	}

	// should check global repository for secrets
	secretNS := repo.GetNamespace()
	globalRepo, err := v.run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(v.run.Info.Kube.Namespace).Get(
		ctx, v.run.Info.Controller.GlobalRepository, metav1.GetOptions{},
	)
	if err == nil && globalRepo != nil {
		if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil && globalRepo.Spec.GitProvider != nil && globalRepo.Spec.GitProvider.Secret != nil {
			secretNS = globalRepo.GetNamespace()
		}
		repo.Spec.Merge(globalRepo.Spec)
	}

	kubeInterface, err := kubeinteraction.NewKubernetesInteraction(v.run)
	if err != nil {
		return event, err
	}

	scm := pipelineascode.SecretFromRepository{
		K8int:       kubeInterface,
		Config:      v.GetConfig(),
		Event:       event,
		Repo:        repo,
		WebhookType: v.pacInfo.WebhookType,
		Logger:      v.Logger,
		Namespace:   secretNS,
	}
	if err := scm.Get(ctx); err != nil {
		return event, fmt.Errorf("cannot get secret from repository: %w", err)
	}

	err = v.SetClient(ctx, v.run, event, repo, v.eventEmitter)
	if err != nil {
		return event, err
	}
	return event, nil
}

func (v *Provider) handleCommitCommentEvent(ctx context.Context, event *gitlab.CommitCommentEvent, processedEvent *info.Event) (*info.Event, error) {
	action := "trigger"
	if event.Repository == nil {
		return nil, fmt.Errorf("error parse_payload: the repository in event payload must not be nil")
	}
	// since comment is made on pushed commit, SourceProjectID and TargetProjectID will be equal.
	v.sourceProjectID = event.ProjectID
	v.targetProjectID = event.ProjectID
	processedEvent.SourceProjectID = v.sourceProjectID
	processedEvent.TargetProjectID = v.targetProjectID
	v.userID = event.User.ID
	v.pathWithNamespace = event.Project.PathWithNamespace
	processedEvent.Organization, processedEvent.Repository = getOrgRepo(v.pathWithNamespace)
	processedEvent.Sender = event.User.Username
	processedEvent.Provider.User = processedEvent.Sender
	processedEvent.URL = event.Project.WebURL
	processedEvent.SHA = event.ObjectAttributes.CommitID
	processedEvent.SHATitle = event.Commit.Title
	processedEvent.HeadURL = processedEvent.URL
	processedEvent.BaseURL = processedEvent.URL
	processedEvent.TriggerTarget = triggertype.Push
	opscomments.SetEventTypeAndTargetPR(processedEvent, event.ObjectAttributes.Note, "/")
	// Set Head and Base branch to default_branch of the repo as this comment is made on
	// a pushed commit.
	defaultBranch := event.Project.DefaultBranch
	processedEvent.HeadBranch, processedEvent.BaseBranch = defaultBranch, defaultBranch
	processedEvent.DefaultBranch = defaultBranch

	var (
		branchName string
		prName     string
		tagName    string
		err        error
	)

	// since we're going to make an API call to ensure that the commit is HEAD of the branch
	// therefore we need to initialize GitLab client here
	processedEvent, err = v.initGitLabClient(ctx, processedEvent)
	if err != nil {
		return processedEvent, err
	}

	// get PipelineRun name from comment if it does contain e.g. `/test pr7`
	if provider.IsTestRetestComment(event.ObjectAttributes.Note) {
		prName, branchName, tagName, err = provider.GetPipelineRunAndBranchOrTagNameFromTestComment(event.ObjectAttributes.Note, "/")
		if err != nil {
			return processedEvent, err
		}
		processedEvent.TargetTestPipelineRun = prName
	}

	if provider.IsCancelComment(event.ObjectAttributes.Note) {
		action = "cancellation"
		prName, branchName, tagName, err = provider.GetPipelineRunAndBranchOrTagNameFromCancelComment(event.ObjectAttributes.Note, "/")
		if err != nil {
			return processedEvent, err
		}
		processedEvent.CancelPipelineRuns = true
		processedEvent.TargetCancelPipelineRun = prName
	}

	if tagName != "" {
		tagPath := fmt.Sprintf("refs/tags/%s", tagName)
		tag, _, err := v.gitlabClient.Tags.GetTag(v.sourceProjectID, tagName)
		if err != nil {
			return processedEvent, fmt.Errorf("error getting tag %s: %w", tagName, err)
		}

		if tag.Commit.ID != processedEvent.SHA {
			return processedEvent, fmt.Errorf("provided SHA %s is not the tagged commit for the tag %s", processedEvent.SHA, tagName)
		}

		processedEvent.HeadBranch = tagPath
		processedEvent.BaseBranch = tagPath
		return processedEvent, nil
	}

	if branchName == "" {
		branchName = processedEvent.HeadBranch
	}

	// check if the commit on which comment is made, is HEAD commit of the branch
	if err := v.isHeadCommitOfBranch(processedEvent, branchName); err != nil {
		if provider.IsCancelComment(event.ObjectAttributes.Note) {
			processedEvent.CancelPipelineRuns = false
		}
		return processedEvent, err
	}

	processedEvent.HeadBranch = branchName
	processedEvent.BaseBranch = branchName
	v.Logger.Infof("gitlab commit_comment: pipelinerun %s has been requested on %s/%s#%s", action, processedEvent.Organization, processedEvent.Repository, processedEvent.SHA)
	return processedEvent, nil
}

func isZeroSHA(sha string) bool {
	return sha == "0000000000000000000000000000000000000000"
}
