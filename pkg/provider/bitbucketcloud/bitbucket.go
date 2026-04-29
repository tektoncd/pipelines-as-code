package bitbucketcloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ktrysmt/go-bitbucket"
	"github.com/mitchellh/mapstructure"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	providerMetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/providermetrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.uber.org/zap"
)

var _ provider.Interface = (*Provider)(nil)

type Provider struct {
	bbClient      *bitbucket.Client
	Logger        *zap.SugaredLogger
	run           *params.Run
	pacInfo       *info.PacOpts
	Token, APIURL *string
	Username      *string
	provenance    string
	eventEmitter  *events.EventEmitter
	repo          *v1alpha1.Repository
	triggerEvent  string
}

func (v *Provider) Client() *bitbucket.Client {
	providerMetrics.RecordAPIUsage(
		v.Logger,
		v.GetConfig().Name,
		v.triggerEvent,
		v.repo,
	)
	return v.bbClient
}

func (v *Provider) CreateComment(_ context.Context, _ *info.Event, _, _ string) error {
	return nil
}

// CheckPolicyAllowing TODO: Implement ME.
func (v *Provider) CheckPolicyAllowing(_ context.Context, _ *info.Event, _ []string) (bool, string) {
	return false, ""
}

// GetTaskURI TODO: Implement ME.
func (v *Provider) GetTaskURI(_ context.Context, _ *info.Event, _ string) (bool, string, error) {
	return false, "", nil
}

func (v *Provider) SetPacInfo(pacInfo *info.PacOpts) {
	v.pacInfo = pacInfo
}

const taskStatusTemplate = `{{range $taskrun := .TaskRunList }} | **{{ formatCondition $taskrun.PipelineRunTaskRunStatus.Status.Conditions }}** | {{ $taskrun.ConsoleLogURL }} | *{{ formatDuration $taskrun.PipelineRunTaskRunStatus.Status.StartTime $taskrun.PipelineRunTaskRunStatus.Status.CompletionTime }}* |
{{ end }}`

func (v *Provider) Validate(_ context.Context, _ *params.Run, _ *info.Event) error {
	return nil
}

func (v *Provider) SetLogger(logger *zap.SugaredLogger) {
	v.Logger = logger
}

func (v *Provider) GetConfig() *info.ProviderConfig {
	return &info.ProviderConfig{
		TaskStatusTMPL: taskStatusTemplate,
		APIURL:         bitbucket.DEFAULT_BITBUCKET_API_BASE_URL,
		Name:           "bitbucket-cloud",
	}
}

func (v *Provider) CreateStatus(_ context.Context, event *info.Event, statusopts status.StatusOpts) error {
	var state types.CommitStatusState

	switch statusopts.Conclusion {
	case status.ConclusionSkipped:
		state = types.StateStopped
		statusopts.Title = "➖ Skipping this commit"
	case status.ConclusionNeutral:
		state = types.StateStopped
		statusopts.Title = "➖ CI has stopped"
	case status.ConclusionFailure:
		state = types.StateFailed
		statusopts.Title = "❌ Failed"
	case status.ConclusionPending:
		state = types.StateInProgress
		statusopts.Title = "⚡ CI has started"
	case status.ConclusionSuccess:
		state = types.StateSuccessful
		statusopts.Title = "✅ Commit has been validated"
	case status.ConclusionCompleted:
		state = types.StateSuccessful
		statusopts.Title = "✅ Completed"
	case status.ConclusionCancelled:
		// TODO
	}
	detailsURL := event.Provider.URL
	if statusopts.DetailsURL != "" {
		detailsURL = statusopts.DetailsURL
	}

	key := provider.GetBBCloudStatusKey(statusopts, v.pacInfo)

	cso := &bitbucket.CommitStatusOptions{
		Key:         key,
		Url:         detailsURL,
		State:       string(state),
		Description: statusopts.Title,
	}
	cmo := &bitbucket.CommitsOptions{
		Owner:    event.Organization,
		RepoSlug: event.Repository,
		Revision: event.SHA,
	}

	if v.bbClient == nil {
		return fmt.Errorf("no token has been set, cannot set status")
	}

	_, err := v.Client().Repositories.Commits.CreateCommitStatus(cmo, cso)
	if err != nil {
		// Only emit an event to notify the user that something went wrong with the commit status API,
		// and proceed with creating the comment (if applicable).
		v.eventEmitter.EmitMessage(v.repo, zap.ErrorLevel, "FailedToSetCommitStatus",
			"cannot set status with the Bitbucket Cloud token because of: "+err.Error())
	}

	eventType := triggertype.IsPullRequestType(event.EventType)
	if state != types.StateStopped && statusopts.Status == "completed" &&
		statusopts.Text != "" &&
		(eventType == triggertype.PullRequest || event.TriggerTarget == triggertype.PullRequest) {
		onPr := ""
		if statusopts.OriginalPipelineRunName != "" {
			onPr = "/" + statusopts.OriginalPipelineRunName
		}
		_, err = v.Client().Repositories.PullRequests.AddComment(
			&bitbucket.PullRequestCommentOptions{
				Owner:         event.Organization,
				RepoSlug:      event.Repository,
				PullRequestID: strconv.Itoa(event.PullRequestNumber),
				Content:       fmt.Sprintf("**%s%s** - %s\n\n%s", v.pacInfo.ApplicationName, onPr, statusopts.Title, statusopts.Text),
			})
		if err != nil {
			return err
		}
	}
	return nil
}

func (v *Provider) GetCommitStatuses(_ context.Context, _ *info.Event) ([]provider.CommitStatusInfo, error) {
	return nil, nil
}

func (v *Provider) GetTektonDir(_ context.Context, event *info.Event, path, provenance string) (string, error) {
	v.provenance = provenance
	repositoryFiles, err := v.getDir(event, path)
	if err != nil {
		return "", err
	}

	return v.concatAllYamlFiles(repositoryFiles, event)
}

func (v *Provider) getDir(event *info.Event, path string) ([]bitbucket.RepositoryFile, error) {
	// default set provenance from the SHA
	revision := event.SHA
	if v.provenance == "default_branch" {
		revision = event.DefaultBranch
		v.Logger.Infof("Using PipelineRun definition from default_branch: %s", event.DefaultBranch)
	} else {
		v.Logger.Infof("Using PipelineRun definition from source %s commit SHA: %s", event.TriggerTarget.String(), event.SHA)
	}
	repoFileOpts := &bitbucket.RepositoryFilesOptions{
		Owner:    event.Organization,
		RepoSlug: event.Repository,
		Ref:      revision,
		Path:     path,
	}

	repositoryFiles, err := v.Client().Repositories.Repository.ListFiles(repoFileOpts)
	if err != nil {
		return nil, err
	}
	return repositoryFiles, nil
}

func (v *Provider) GetFileInsideRepo(_ context.Context, event *info.Event, path, _ string) (string, error) {
	revision := event.SHA
	if v.provenance == "default_branch" {
		revision = event.DefaultBranch
	}
	return v.getBlob(event, revision, path)
}

func (v *Provider) SetClient(_ context.Context, run *params.Run, event *info.Event, repo *v1alpha1.Repository, eventEmitter *events.EventEmitter) error {
	if event.Provider.Token == "" {
		return fmt.Errorf("no git_provider.secret has been set in the repo crd")
	}
	if event.Provider.User == "" {
		return fmt.Errorf("no git_provider.user has been in repo crd")
	}
	bbClient, err := bitbucket.NewBasicAuth(event.Provider.User, event.Provider.Token)
	if err != nil {
		return fmt.Errorf("failed to create bitbucket client: %w", err)
	}
	v.bbClient = bbClient

	// Added log for security audit purposes to log client access when a token is used
	run.Clients.Log.Infof("bitbucket-cloud: initialized client with provided token for user=%s", event.Provider.User)

	v.Token = &event.Provider.Token
	v.Username = &event.Provider.User
	v.run = run
	v.eventEmitter = eventEmitter
	v.repo = repo
	v.triggerEvent = event.EventType
	return nil
}

func (v *Provider) GetCommitInfo(_ context.Context, event *info.Event) error {
	// If we don't have a SHA, get it from the branch first
	sha := event.SHA
	if sha == "" && event.HeadBranch != "" {
		v.Logger.Infof("fetching branch info to get commit SHA for branch: %s", event.HeadBranch)
		branchInfo, err := v.Client().Repositories.Repository.GetBranch(&bitbucket.RepositoryBranchOptions{
			Owner:      event.Organization,
			RepoSlug:   event.Repository,
			BranchName: event.HeadBranch,
		})
		if err != nil {
			return err
		}
		// Extract hash from Target map
		if hash, ok := branchInfo.Target["hash"].(string); ok {
			sha = hash
		} else {
			return fmt.Errorf("cannot extract commit hash from branch %s", event.HeadBranch)
		}
	}

	// Use GetCommit API for direct single-commit fetch (no pagination)
	v.Logger.Infof("fetching commit info using GetCommit API for SHA: %s", sha)
	response, err := v.Client().Repositories.Commits.GetCommit(&bitbucket.CommitsOptions{
		Owner:    event.Organization,
		RepoSlug: event.Repository,
		Revision: sha,
	})
	if err != nil {
		return err
	}

	commitMap, ok := response.(map[string]any)
	if !ok {
		return fmt.Errorf("cannot convert commit response")
	}

	commitinfo := &types.Commit{}
	err = mapstructure.Decode(commitMap, commitinfo)
	if err != nil {
		return err
	}

	// Some silliness since we get first the account id and we fill it properly after
	event.SHATitle = commitinfo.Message
	event.SHAURL = commitinfo.Links.HTML.HRef
	event.SHA = commitinfo.Hash

	// Populate full commit information for LLM context
	event.SHAMessage = commitinfo.Message
	// Bitbucket Cloud API has limited commit author information
	// Use display name or nickname if available
	if commitinfo.Author.User.DisplayName != "" {
		event.SHAAuthorName = commitinfo.Author.User.DisplayName
	} else if commitinfo.Author.Nickname != "" {
		event.SHAAuthorName = commitinfo.Author.Nickname
	}
	// Note: Bitbucket Cloud API doesn't provide author email or timestamps in the basic commit response

	event.HasSkipCommand = provider.SkipCI(commitinfo.Message)
	// now to get the default branch from repository.Get
	repo, err := v.Client().Repositories.Repository.Get(&bitbucket.RepositoryOptions{
		Owner:    event.Organization,
		RepoSlug: event.Repository,
	})
	if err != nil {
		return err
	}
	event.DefaultBranch = repo.Mainbranch.Name
	return nil
}

func (v *Provider) concatAllYamlFiles(objects []bitbucket.RepositoryFile, event *info.Event) (string, error) {
	var allTemplates string

	revision := event.SHA
	if v.provenance == "default_branch" {
		revision = event.DefaultBranch
	}
	for _, value := range objects {
		if value.Type == "commit_directory" {
			objects, err := v.getDir(event, value.Path)
			if err != nil {
				return "", err
			}
			subdirdata, err := v.concatAllYamlFiles(objects, event)
			if err != nil {
				return "", err
			}
			if allTemplates != "" && !strings.HasPrefix(subdirdata, "---") {
				allTemplates += "---"
			}
			allTemplates += fmt.Sprintf("\n%s\n", subdirdata)
		} else if strings.HasSuffix(value.Path, ".yaml") ||
			strings.HasSuffix(value.Path, ".yml") {
			data, err := v.getBlob(event, revision, value.Path)
			if err != nil {
				return "", err
			}
			if err := provider.ValidateYaml([]byte(data), value.Path); err != nil {
				return "", err
			}

			if allTemplates != "" && !strings.HasPrefix(data, "---") {
				allTemplates += "---"
			}
			allTemplates += "\n" + data + "\n"
		}
	}
	return allTemplates, nil
}

func (v *Provider) getBlob(runevent *info.Event, ref, path string) (string, error) {
	blob, err := v.Client().Repositories.Repository.GetFileBlob(&bitbucket.RepositoryBlobOptions{
		Owner:    runevent.Organization,
		RepoSlug: runevent.Repository,
		Ref:      ref,
		Path:     path,
	})
	if err != nil {
		return "", fmt.Errorf("cannot find %s on branch %s in repo %s/%s", path, ref, runevent.Organization, runevent.Repository)
	}
	return blob.String(), nil
}

func (v *Provider) GetFiles(_ context.Context, _ *info.Event) (changedfiles.ChangedFiles, error) {
	return changedfiles.ChangedFiles{}, nil
}

func (v *Provider) CreateToken(_ context.Context, _ []string, _ *info.Event) (string, error) {
	return "", nil
}

func (v *Provider) GetTemplate(commentType provider.CommentType) string {
	return provider.GetMarkdownTemplate(commentType)
}
