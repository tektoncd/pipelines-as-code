package gitlab

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerMetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/providermetrics"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/zap"
)

const (
	apiPublicURL       = "https://gitlab.com"
	taskStatusTemplate = `
<table>
  <tr><th>Status</th><th>Duration</th><th>Name</th></tr>

{{- range $taskrun := .TaskRunList }}
<tr>
<td>{{ formatCondition $taskrun.PipelineRunTaskRunStatus.Status.Conditions }}</td>
<td>{{ formatDuration $taskrun.PipelineRunTaskRunStatus.Status.StartTime $taskrun.PipelineRunTaskRunStatus.Status.CompletionTime }}</td><td>

{{ $taskrun.ConsoleLogURL }}

</td></tr>
{{- end }}
</table>`
	noClientErrStr = `no gitlab client has been initialized, exiting... (hint: did you forget setting a secret on your repo?)`
)

var anyMergeRequestEventType = []string{"Merge Request", "MergeRequest"}

var _ provider.Interface = (*Provider)(nil)

type Provider struct {
	gitlabClient      *gitlab.Client
	Logger            *zap.SugaredLogger
	run               *params.Run
	pacInfo           *info.PacOpts
	Token             *string
	targetProjectID   int64
	sourceProjectID   int64
	userID            int64
	pathWithNamespace string
	repoURL           string
	apiURL            string
	eventEmitter      *events.EventEmitter
	repo              *v1alpha1.Repository
	triggerEvent      string
	// memberCache caches membership/permission checks by user ID within the
	// current provider instance lifecycle to avoid repeated API calls.
	memberCache        map[int64]bool
	cachedChangedFiles *changedfiles.ChangedFiles
	pacUserID          int64 // user login used by PAC
}

var defaultGitlabListOptions = gitlab.ListOptions{
	PerPage: 100,
}

func (v *Provider) Client() *gitlab.Client {
	providerMetrics.RecordAPIUsage(
		v.Logger,
		// URL used instead of "gitlab" to differentiate in the case of a CI cluster which
		// serves multiple GitLab instances
		v.apiURL,
		v.triggerEvent,
		v.repo,
	)
	return v.gitlabClient
}

func (v *Provider) SetGitLabClient(client *gitlab.Client) {
	v.gitlabClient = client
}

func (v *Provider) SetPacInfo(pacInfo *info.PacOpts) {
	v.pacInfo = pacInfo
}

func (v *Provider) CreateComment(_ context.Context, event *info.Event, commit, updateMarker string) error {
	if v.gitlabClient == nil {
		return fmt.Errorf("no gitlab client has been initialized")
	}

	if event.PullRequestNumber == 0 {
		return fmt.Errorf("create comment only works on merge requests")
	}

	// List comments of the merge request
	if updateMarker != "" {
		commentRe := regexp.MustCompile(regexp.QuoteMeta(updateMarker))
		options := []gitlab.RequestOptionFunc{}

		for {
			comments, resp, err := v.Client().Notes.ListMergeRequestNotes(event.TargetProjectID, int64(event.PullRequestNumber), &gitlab.ListMergeRequestNotesOptions{ListOptions: defaultGitlabListOptions}, options...)
			if err != nil {
				return err
			}

			for _, comment := range comments {
				if commentRe.MatchString(comment.Body) {
					// Get the UserID for the PAC user.
					if v.pacUserID == 0 {
						pacUser, _, err := v.Client().Users.CurrentUser()
						if err != nil {
							return fmt.Errorf("unable to fetch user info: %w", err)
						}
						v.pacUserID = pacUser.ID
					}
					// Only edit comments created by this PAC installation's credentials.
					// Prevents accidentally modifying comments from other users/bots.
					if comment.Author.ID != v.pacUserID {
						v.Logger.Debugf("This comment was not created by PAC, skipping comment edit :%d, created by user %d, PAC user: %d",
							comment.ID, comment.Author.ID, v.pacUserID)
						continue
					}

					_, _, err := v.Client().Notes.UpdateMergeRequestNote(event.TargetProjectID, int64(event.PullRequestNumber), comment.ID, &gitlab.UpdateMergeRequestNoteOptions{
						Body: &commit,
					})
					if err != nil {
						return fmt.Errorf("unable to update merge request note: %w", err)
					}
					return nil
				}
			}

			// Exit the loop when we've seen all pages.
			if resp.NextLink == "" {
				break
			}

			// Otherwise, set param to query the next page
			options = []gitlab.RequestOptionFunc{
				gitlab.WithKeysetPaginationParameters(resp.NextLink),
			}
		}
	}

	_, _, err := v.Client().Notes.CreateMergeRequestNote(event.TargetProjectID, int64(event.PullRequestNumber), &gitlab.CreateMergeRequestNoteOptions{
		Body: &commit,
	})
	if err != nil {
		return fmt.Errorf("unable to create merge request note: %w", err)
	}
	return nil
}

// CheckPolicyAllowing TODO: Implement ME.
func (v *Provider) CheckPolicyAllowing(_ context.Context, _ *info.Event, _ []string) (bool, string) {
	return false, ""
}

func (v *Provider) SetLogger(logger *zap.SugaredLogger) {
	v.Logger = logger
}

func (v *Provider) Validate(_ context.Context, _ *params.Run, event *info.Event) error {
	token := event.Request.Header.Get("X-Gitlab-Token")
	if token == "" {
		return fmt.Errorf("no X-Gitlab-Token header detected: webhook validation requires a token for security")
	}

	if event.Provider.WebhookSecret == "" {
		return fmt.Errorf("no webhook secret configured: set webhook secret in repository CR or secret")
	}

	if subtle.ConstantTimeCompare([]byte(event.Provider.WebhookSecret), []byte(token)) == 0 {
		return fmt.Errorf("gitlab webhook validation failed: token does not match configured secret")
	}
	return nil
}

// If I understood properly, you can have "personal" projects and groups
// attached projects. But this doesn't seem to show in the API, so we
// are just doing it the path_with_namespace to get the "org".
//
// Note that "orgs/groups" may have subgroups, so we get the first parts
// as Orgs and the last element as Repo It's just a detail to show for
// UI, we actually don't use this field for access or other logical
// stuff.
func getOrgRepo(pathWithNamespace string) (string, string) {
	org := filepath.Dir(pathWithNamespace)
	return org, filepath.Base(pathWithNamespace)
}

func (v *Provider) GetConfig() *info.ProviderConfig {
	return &info.ProviderConfig{
		TaskStatusTMPL: taskStatusTemplate,
		APIURL:         apiPublicURL,
		Name:           "gitlab",
	}
}

func (v *Provider) SetClient(_ context.Context, run *params.Run, runevent *info.Event, repo *v1alpha1.Repository, eventsEmitter *events.EventEmitter) error {
	var err error
	if runevent.Provider.Token == "" {
		return fmt.Errorf("no git_provider.secret has been set in the repo crd")
	}

	v.run = run
	v.eventEmitter = eventsEmitter
	v.repo = repo
	v.triggerEvent = runevent.EventType

	// Try to detect automatically the API url if url is not coming from public
	// gitlab. Unless user has set a spec.provider.url in its repo crd
	apiURL := ""
	switch {
	case runevent.Provider.URL != "":
		apiURL = runevent.Provider.URL
	case v.repoURL != "" && !strings.HasPrefix(v.repoURL, apiPublicURL):
		apiURL = strings.ReplaceAll(v.repoURL, v.pathWithNamespace, "")
	case runevent.URL != "":
		burl, err := url.Parse(runevent.URL)
		if err != nil {
			return err
		}
		apiURL = fmt.Sprintf("%s://%s", burl.Scheme, burl.Host)
	default:
		// this really should not happen but let's just hope this is it
		apiURL = apiPublicURL
	}
	_, err = url.Parse(apiURL)
	if err != nil {
		return fmt.Errorf("failed to parse api url %s: %w", apiURL, err)
	}
	v.apiURL = apiURL

	if v.gitlabClient == nil {
		v.gitlabClient, err = gitlab.NewClient(runevent.Provider.Token, gitlab.WithBaseURL(apiURL))
		if err != nil {
			return err
		}
	}
	v.Token = &runevent.Provider.Token

	run.Clients.Log.Infof("gitlab: initialized for client with token for apiURL=%s, org=%s, repo=%s", apiURL, runevent.Organization, runevent.Repository)
	// In a scenario where the source repository is forked and a merge request (MR) is created on the upstream
	// repository, runevent.SourceProjectID will not be 0 when SetClient is called from the pac-watcher code.
	// This is because, in the controller, SourceProjectID is set in the annotation of the pull request,
	// and runevent.SourceProjectID is set before SetClient is called. Therefore, we need to take
	// the ID from runevent.SourceProjectID when v.sourceProject is 0 (nil).
	if v.sourceProjectID == 0 && runevent.SourceProjectID > 0 {
		v.sourceProjectID = runevent.SourceProjectID
	}

	// check that we have access to the source project if it's a private repo, this should only occur on Merge Requests
	if runevent.TriggerTarget == triggertype.PullRequest {
		_, resp, err := v.Client().Projects.GetProject(runevent.SourceProjectID, &gitlab.GetProjectOptions{})
		errmsg := fmt.Sprintf("failed to access GitLab source repository ID %d: please ensure token has 'read_repository' scope on that repository",
			runevent.SourceProjectID)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%s", errmsg)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", errmsg, err)
		}
	}

	// if we don't have sourceProjectID (ie: incoming-webhook) then try to set
	// it ASAP if we can.
	if v.sourceProjectID == 0 && runevent.Organization != "" && runevent.Repository != "" {
		projectSlug := path.Join(runevent.Organization, runevent.Repository)
		projectinfo, _, err := v.Client().Projects.GetProject(projectSlug, &gitlab.GetProjectOptions{})
		if err != nil {
			return err
		}
		// TODO: we really need to move out the runevent.*ProjecTID to v.*ProjectID,
		// I just spent half an hour debugging because i didn't realise it was there instead in v.*
		v.sourceProjectID = projectinfo.ID
		runevent.SourceProjectID = projectinfo.ID
		runevent.TargetProjectID = projectinfo.ID
		runevent.DefaultBranch = projectinfo.DefaultBranch
	}

	return nil
}

//nolint:misspell
func (v *Provider) CreateStatus(ctx context.Context, event *info.Event, statusOpts providerstatus.StatusOpts,
) error {
	var detailsURL string
	if v.gitlabClient == nil {
		return fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}

	var state gitlab.BuildStateValue

	switch statusOpts.Conclusion {
	case providerstatus.ConclusionSkipped:
		state = gitlab.Skipped
		statusOpts.Title = "skipped validating this commit"
	case providerstatus.ConclusionNeutral:
		state = gitlab.Canceled
		statusOpts.Title = "stopped"
	case providerstatus.ConclusionCancelled:
		state = gitlab.Canceled
		statusOpts.Title = "cancelled validating this commit"
	case providerstatus.ConclusionFailure:
		state = gitlab.Failed
		statusOpts.Title = "failed"
	case providerstatus.ConclusionSuccess:
		state = gitlab.Success
		statusOpts.Title = "successfully validated your commit"
	case providerstatus.ConclusionCompleted:
		state = gitlab.Success
		statusOpts.Title = "completed"
	case providerstatus.ConclusionPending:
		state = gitlab.Running
	}

	// When the pipeline is actually running (in_progress), show it as running
	// not pending. Pending is only for waiting states like /ok-to-test approval.
	if statusOpts.Status == "in_progress" {
		state = gitlab.Running
	}
	if statusOpts.DetailsURL != "" {
		detailsURL = statusOpts.DetailsURL
	}

	onPr := ""
	if statusOpts.OriginalPipelineRunName != "" {
		onPr = "/" + statusOpts.OriginalPipelineRunName
	}
	body := fmt.Sprintf("**%s%s** has %s\n\n%s\n\n<small>Full log available [here](%s)</small>",
		v.pacInfo.ApplicationName, onPr, statusOpts.Title, statusOpts.Text, detailsURL)

	contextName := provider.GetCheckName(statusOpts, v.pacInfo)
	opt := &gitlab.SetCommitStatusOptions{
		State:       state,
		Name:        gitlab.Ptr(contextName),
		TargetURL:   gitlab.Ptr(detailsURL),
		Description: gitlab.Ptr(statusOpts.Title),
		Context:     gitlab.Ptr(contextName),
	}

	// In case we have access, set the status. Typically, on a Merge Request (MR)
	// from a fork in an upstream repository, the token needs to have write access
	// to the fork repository in order to create a status. However, the token set on the
	// Repository CR usually doesn't have such broad access, preventing from creating
	// a status comment on it.
	// This would work on a push or an MR from a branch within the same repo.
	// Ignoring errors because of the write access issues,
	_, _, err := v.Client().Commits.SetCommitStatus(event.SourceProjectID, event.SHA, opt)
	if err != nil {
		v.Logger.Debugf("cannot set status with the GitLab token on the source project: %v", err)
	} else {
		// we managed to set the status on the source repo, all good we are done
		v.Logger.Debugf("created commit status on source project ID %d", event.TargetProjectID)
		return nil
	}
	if _, _, err = v.Client().Commits.SetCommitStatus(event.TargetProjectID, event.SHA, opt); err == nil {
		v.Logger.Debugf("created commit status on target project ID %d", event.TargetProjectID)
		// we managed to set the status on the target repo, all good we are done
		return nil
	}
	v.Logger.Debugf("cannot set status with the GitLab token on the target project: %v", err)

	// Skip creating MR comments if the error is a state transition error
	// (e.g., "Cannot transition status via :run from :running").
	// This means the status is already set, so we should not create a comment.
	if strings.Contains(err.Error(), "Cannot transition status") {
		v.Logger.Debugf("skipping MR comment as error is not permission related: %v", err)
		return nil
	}

	// we only show the first error as it's likely something the user has more control to fix
	// the second err is cryptic as it needs a dummy gitlab pipeline to start
	// with and will only give more confusion in the event namespace
	v.eventEmitter.EmitMessage(v.repo, zap.InfoLevel, "FailedToSetCommitStatus",
		fmt.Sprintf("failed to create commit status: source project ID %d, target project ID %d. "+
			"If you want Gitlab Pipeline Status update, ensure your GitLab token giving it access "+
			"to the source repository. %v",
			event.SourceProjectID, event.TargetProjectID, err))

	eventType := triggertype.IsPullRequestType(event.EventType)
	// When a GitOps command is sent on a pushed commit, it mistakenly treats it as a pull_request
	// and attempts to create a note, but notes are not intended for pushed commits.
	if event.TriggerTarget == triggertype.PullRequest && opscomments.IsAnyOpsEventType(event.EventType) {
		eventType = triggertype.PullRequest
	}

	var commentStrategy string

	if v.repo != nil && v.repo.Spec.Settings != nil && v.repo.Spec.Settings.Gitlab != nil {
		commentStrategy = v.repo.Spec.Settings.Gitlab.CommentStrategy
	}
	switch commentStrategy {
	case provider.DisableAllCommentStrategy:
		v.Logger.Warn("Comments related to PipelineRuns status have been disabled for GitLab merge requests")
		return nil
	case provider.UpdateCommentStrategy:
		if eventType == triggertype.PullRequest || provider.Valid(event.EventType, anyMergeRequestEventType) {
			statusComment := v.formatPipelineComment(event.SHA, statusOpts)
			// Creating the prefix that is added to the status comment for a pipeline run.
			plrStatusCommentPrefix := fmt.Sprintf(provider.PlrStatusCommentPrefixTemplate, statusOpts.OriginalPipelineRunName)
			// The entire markdown comment, including the prefix that is added to the pull request for the pipelinerun.
			markdownStatusComment := fmt.Sprintf("%s\n%s", plrStatusCommentPrefix, statusComment)

			if err := v.CreateComment(ctx, event, markdownStatusComment, plrStatusCommentPrefix); err != nil {
				v.eventEmitter.EmitMessage(
					v.repo,
					zap.ErrorLevel,
					"PipelineRunCommentCreationError",
					fmt.Sprintf("failed to create comment: %s", err.Error()),
				)
				return err
			}
		}
	default:
		if eventType == triggertype.PullRequest || provider.Valid(event.EventType, anyMergeRequestEventType) {
			mopt := &gitlab.CreateMergeRequestNoteOptions{Body: gitlab.Ptr(body)}
			_, _, err := v.Client().Notes.CreateMergeRequestNote(event.TargetProjectID, int64(event.PullRequestNumber), mopt)
			return err
		}
	}

	return nil
}

func (v *Provider) GetCommitStatuses(_ context.Context, event *info.Event) ([]provider.CommitStatusInfo, error) {
	if v.gitlabClient == nil {
		return nil, fmt.Errorf("%s", noClientErrStr)
	}

	sourceProjectID := event.SourceProjectID
	if sourceProjectID == 0 {
		sourceProjectID = v.sourceProjectID
	}

	targetProjectID := event.TargetProjectID
	if targetProjectID == 0 {
		targetProjectID = v.targetProjectID
	}

	projectIDs := []int64{}
	if sourceProjectID != 0 {
		projectIDs = append(projectIDs, sourceProjectID)
	}
	if targetProjectID != 0 && targetProjectID != sourceProjectID {
		projectIDs = append(projectIDs, targetProjectID)
	}

	var (
		firstErr error
		result   []provider.CommitStatusInfo
		seen     = map[string]struct{}{}
	)

	for _, projectID := range projectIDs {
		statuses, _, err := v.Client().Commits.GetCommitStatuses(projectID, event.SHA, &gitlab.GetCommitStatusesOptions{})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if v.Logger != nil {
				v.Logger.Debugf("failed to get commit statuses from gitlab project ID %d for SHA %s: %v", projectID, event.SHA, err)
			}
			continue
		}

		for _, s := range statuses {
			key := fmt.Sprintf("%s\x00%s", s.Name, s.Status)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, provider.CommitStatusInfo{
				Name:   s.Name,
				Status: s.Status,
			})
		}
	}

	if len(result) > 0 {
		return result, nil
	}

	return nil, firstErr
}

func (v *Provider) GetTektonDir(_ context.Context, event *info.Event, path, provenance string) (string, error) {
	if v.gitlabClient == nil {
		return "", fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}
	// default set provenance from head
	revision := event.HeadBranch
	if provenance == "default_branch" {
		revision = event.DefaultBranch
		v.Logger.Infof("Using PipelineRun definition from default_branch: %s", event.DefaultBranch)
	} else {
		trigger := event.TriggerTarget.String()
		if event.TriggerTarget == triggertype.PullRequest {
			trigger = "merge request"
		}
		v.Logger.Infof("Using PipelineRun definition from source %s on commit SHA: %s", trigger, event.SHA)
	}

	opt := &gitlab.ListTreeOptions{
		Path:      gitlab.Ptr(path),
		Ref:       gitlab.Ptr(revision),
		Recursive: gitlab.Ptr(true),
		ListOptions: gitlab.ListOptions{
			OrderBy:    "id",
			Pagination: "keyset",
			PerPage:    defaultGitlabListOptions.PerPage,
			Sort:       "asc",
		},
	}

	options := []gitlab.RequestOptionFunc{}
	nodes := []*gitlab.TreeNode{}

	for {
		objects, resp, err := v.Client().Repositories.ListTree(v.sourceProjectID, opt, options...)
		if err != nil {
			return "", fmt.Errorf("failed to list %s dir: %w", path, err)
		}
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return "", nil
		}

		nodes = append(nodes, objects...)

		// Exit the loop when we've seen all pages.
		if resp.NextLink == "" {
			break
		}

		// Otherwise, set param to query the next page
		options = []gitlab.RequestOptionFunc{
			gitlab.WithKeysetPaginationParameters(resp.NextLink),
		}
	}

	return v.concatAllYamlFiles(nodes, revision)
}

// concatAllYamlFiles concat all yaml files from a directory as one big multi document yaml string.
func (v *Provider) concatAllYamlFiles(objects []*gitlab.TreeNode, revision string) (string, error) {
	var allTemplates string
	for _, value := range objects {
		if strings.HasSuffix(value.Name, ".yaml") ||
			strings.HasSuffix(value.Name, ".yml") {
			data, _, err := v.getObject(value.Path, revision, v.sourceProjectID)
			if err != nil {
				return "", err
			}
			if err := provider.ValidateYaml(data, value.Path); err != nil {
				return "", err
			}
			if allTemplates != "" && !strings.HasPrefix(string(data), "---") {
				allTemplates += "---"
			}
			allTemplates += "\n" + string(data) + "\n"
		}
	}

	return allTemplates, nil
}

func (v *Provider) getObject(fname, branch string, pid int64) ([]byte, *gitlab.Response, error) {
	opt := &gitlab.GetRawFileOptions{
		Ref: gitlab.Ptr(branch),
	}
	file, resp, err := v.Client().RepositoryFiles.GetRawFile(pid, fname, opt)
	if err != nil {
		return []byte{}, resp, fmt.Errorf("failed to get filename from api %s dir: %w", fname, err)
	}
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return []byte{}, resp, nil
	}
	return file, resp, nil
}

func (v *Provider) GetFileInsideRepo(_ context.Context, runevent *info.Event, path, _ string) (string, error) {
	getobj, _, err := v.getObject(path, runevent.HeadBranch, v.sourceProjectID)
	if err != nil {
		return "", err
	}
	return string(getobj), nil
}

func (v *Provider) GetCommitInfo(_ context.Context, runevent *info.Event) error {
	if v.gitlabClient == nil {
		return fmt.Errorf("%s", noClientErrStr)
	}

	// if we don't have a SHA (ie: incoming-webhook) then get it from the branch
	// and populate in the runevent.
	if runevent.SHA == "" && runevent.HeadBranch != "" {
		branchinfo, _, err := v.Client().Commits.GetCommit(v.sourceProjectID, runevent.HeadBranch, &gitlab.GetCommitOptions{})
		if err != nil {
			return err
		}
		runevent.SHA = branchinfo.ID
		runevent.SHATitle = branchinfo.Title
		runevent.SHAURL = branchinfo.WebURL

		// Populate full commit information for LLM context
		runevent.SHAMessage = branchinfo.Message
		runevent.SHAAuthorName = branchinfo.AuthorName
		runevent.SHAAuthorEmail = branchinfo.AuthorEmail
		if branchinfo.AuthoredDate != nil {
			runevent.SHAAuthorDate = *branchinfo.AuthoredDate
		}
		runevent.SHACommitterName = branchinfo.CommitterName
		runevent.SHACommitterEmail = branchinfo.CommitterEmail
		if branchinfo.CommittedDate != nil {
			runevent.SHACommitterDate = *branchinfo.CommittedDate
		}
	}
	runevent.HasSkipCommand = provider.SkipCI(runevent.SHAMessage)

	return nil
}

// GetFiles gets and caches the list of files changed by a given event.
func (v *Provider) GetFiles(ctx context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	if v.cachedChangedFiles == nil {
		changes, err := v.fetchChangedFiles(ctx, runevent)
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}
		v.cachedChangedFiles = &changes
	}
	return *v.cachedChangedFiles, nil
}

func (v *Provider) fetchChangedFiles(_ context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	if v.gitlabClient == nil {
		return changedfiles.ChangedFiles{}, fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}

	changedFiles := changedfiles.ChangedFiles{}

	switch runevent.TriggerTarget {
	case triggertype.PullRequest:
		var err error
		changedFiles, err = v.mergeRequestFilesChanged(runevent)
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}
	case triggertype.Push:
		options := gitlab.GetCommitDiffOptions{ListOptions: defaultGitlabListOptions}
		pageOpts := []gitlab.RequestOptionFunc{}

		for {
			pushChanges, resp, err := v.Client().Commits.GetCommitDiff(v.sourceProjectID, runevent.SHA, &options, pageOpts...)
			if err != nil {
				return changedfiles.ChangedFiles{}, err
			}

			for _, change := range pushChanges {
				changedFiles.All = append(changedFiles.All, change.NewPath)
				if change.NewFile {
					changedFiles.Added = append(changedFiles.Added, change.NewPath)
				}
				if change.DeletedFile {
					changedFiles.Deleted = append(changedFiles.Deleted, change.NewPath)
				}
				if !change.RenamedFile && !change.DeletedFile && !change.NewFile {
					changedFiles.Modified = append(changedFiles.Modified, change.NewPath)
				}
				if change.RenamedFile {
					changedFiles.Renamed = append(changedFiles.Renamed, change.NewPath)
				}
			}

			if resp.NextLink == "" {
				// Exit the loop when we've seen all pages.
				break
			}
			// Otherwise, set param to query the next page
			pageOpts = []gitlab.RequestOptionFunc{
				gitlab.WithKeysetPaginationParameters(resp.NextLink),
			}
		}
	default:
		// No action necessary
	}
	return changedFiles, nil
}

func (v *Provider) mergeRequestFilesChanged(runevent *info.Event) (changedfiles.ChangedFiles, error) {
	diffTruncated, changeCount, err := v.isMergeRequestDiffTruncated(v.targetProjectID, int64(runevent.PullRequestNumber))
	if err != nil {
		return changedfiles.ChangedFiles{}, err
	}

	changedFiles := changedfiles.ChangedFiles{
		All:     make([]string, 0, changeCount),
		Added:   []string{},
		Deleted: []string{},
		Renamed: []string{},
	}

	options := []gitlab.RequestOptionFunc{}

	// Only use the repository/compare API if the standard merge_request/diff API endpoint will
	// return a truncated set of changes. The repository/compare API returns the entire set of
	// changes without paging, so it can have a significantly heavier memory footprint if used
	// in all cases.
	if diffTruncated {
		compareOpts := &gitlab.CompareOptions{
			From: &runevent.BaseBranch,
			To:   &runevent.SHA,
		}
		comparison, _, err := v.Client().Repositories.Compare(v.targetProjectID, compareOpts, options...)
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}

		for _, change := range comparison.Diffs {
			changedFiles.All = append(changedFiles.All, change.NewPath)
			if change.NewFile {
				changedFiles.Added = append(changedFiles.Added, change.NewPath)
			}
			if change.DeletedFile {
				changedFiles.Deleted = append(changedFiles.Deleted, change.NewPath)
			}
			if !change.RenamedFile && !change.DeletedFile && !change.NewFile {
				changedFiles.Modified = append(changedFiles.Modified, change.NewPath)
			}
			if change.RenamedFile {
				changedFiles.Renamed = append(changedFiles.Renamed, change.NewPath)
			}
		}
	} else {
		diffOpts := &gitlab.ListMergeRequestDiffsOptions{
			ListOptions: gitlab.ListOptions{
				OrderBy:    "id",
				Pagination: "keyset",
				PerPage:    defaultGitlabListOptions.PerPage,
				Sort:       "asc",
			},
		}
		for {
			mrchanges, resp, err := v.Client().MergeRequests.ListMergeRequestDiffs(v.targetProjectID, int64(runevent.PullRequestNumber), diffOpts, options...)
			if err != nil {
				// TODO: Should this return the files found so far?
				return changedfiles.ChangedFiles{}, err
			}
			for _, change := range mrchanges {
				changedFiles.All = append(changedFiles.All, change.NewPath)
				if change.NewFile {
					changedFiles.Added = append(changedFiles.Added, change.NewPath)
				}
				if change.DeletedFile {
					changedFiles.Deleted = append(changedFiles.Deleted, change.NewPath)
				}
				if !change.RenamedFile && !change.DeletedFile && !change.NewFile {
					changedFiles.Modified = append(changedFiles.Modified, change.NewPath)
				}
				if change.RenamedFile {
					changedFiles.Renamed = append(changedFiles.Renamed, change.NewPath)
				}
			}

			// Exit the loop when we've seen all pages.
			if resp.NextLink == "" {
				break
			}

			// Otherwise, set param to query the next page
			options = []gitlab.RequestOptionFunc{
				gitlab.WithKeysetPaginationParameters(resp.NextLink),
			}
		}
	}
	return changedFiles, nil
}

// isMergeRequestDiffTruncated checks if the merge request is affected by the Gitlab API's Diff Limits.
// This is determined by the Get Merge Request API's returning a ChangeCount number with a "+" suffix.
// See also: https://docs.gitlab.com/administration/diff_limits/
// Returns (bool: isTruncated, int: changeCount, err: error).
func (v *Provider) isMergeRequestDiffTruncated(projectID, mergeRequestID int64) (bool, int, error) {
	out, _, err := v.Client().MergeRequests.GetMergeRequest(projectID, mergeRequestID, &gitlab.GetMergeRequestsOptions{})
	if err != nil {
		return false, 0, fmt.Errorf("error getting merge request %d: %w", mergeRequestID, err)
	}
	fileCount := 0
	truncated := strings.HasSuffix(out.ChangesCount, "+")
	if out.ChangesCount != "" {
		countStr := strings.TrimSuffix(out.ChangesCount, "+")
		fileCount, err = strconv.Atoi(countStr)
		if err != nil {
			return false, 0, err
		}
	}
	return truncated, fileCount, nil
}

func (v *Provider) CreateToken(_ context.Context, _ []string, _ *info.Event) (string, error) {
	return "", nil
}

// isHeadCommitOfBranch validates that branch exists and the SHA is HEAD commit of the branch.
func (v *Provider) isHeadCommitOfBranch(runevent *info.Event, branchName string) error {
	if v.gitlabClient == nil {
		return fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}
	branch, _, err := v.Client().Branches.GetBranch(v.sourceProjectID, branchName)
	if err != nil {
		return err
	}

	if branch.Commit.ID == runevent.SHA {
		return nil
	}

	return fmt.Errorf("provided SHA %s is not the HEAD commit of the branch %s", runevent.SHA, branchName)
}

func (v *Provider) GetTemplate(commentType provider.CommentType) string {
	return provider.GetHTMLTemplate(commentType)
}

//nolint:misspell
func (v *Provider) formatPipelineComment(sha string, status providerstatus.StatusOpts) string {
	var emoji string

	switch status.Conclusion {
	case "canceled":
		emoji = "⚠️"
	case "failed":
		emoji = "❌"
	case "success":
		emoji = "✅"
	case "running":
		emoji = "🚀"
	default:
		emoji = "ℹ️"
	}

	return fmt.Sprintf("%s **%s: %s/%s for %s**\n\n%s\n\n<small>Full log available [here](%s)</small>",
		emoji, status.Title, v.pacInfo.ApplicationName, status.OriginalPipelineRunName, sha, status.Text, status.DetailsURL)
}
