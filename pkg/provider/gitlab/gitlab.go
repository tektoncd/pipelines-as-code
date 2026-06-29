package gitlab

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/xanzy/go-gitlab"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
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

var _ provider.Interface = (*Provider)(nil)

type Provider struct {
	Client            *gitlab.Client
	Logger            *zap.SugaredLogger
	run               *params.Run
	pacInfo           *info.PacOpts
	Token             *string
	targetProjectID   int
	sourceProjectID   int
	userID            int
	pathWithNamespace string
	repoURL           string
	apiURL            string
	eventEmitter      *events.EventEmitter
	repo              *v1alpha1.Repository
}

func (v *Provider) SetPacInfo(pacInfo *info.PacOpts) {
	v.pacInfo = pacInfo
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
	if event.Provider.WebhookSecret == "" && token != "" {
		return fmt.Errorf("gitlab failed validation: failed to find webhook secret")
	}

	if subtle.ConstantTimeCompare([]byte(event.Provider.WebhookSecret), []byte(token)) == 0 {
		return fmt.Errorf("gitlab failed validation: event's secret doesn't match with webhook secret")
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
	org = strings.ReplaceAll(org, "/", "-")
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
	v.apiURL = apiURL

	v.Client, err = gitlab.NewClient(runevent.Provider.Token, gitlab.WithBaseURL(apiURL))
	if err != nil {
		return err
	}
	v.Token = &runevent.Provider.Token

	// if we don't have sourceProjectID (ie: incoming-webhook) then try to set
	// it ASAP if we can.
	if v.sourceProjectID == 0 && runevent.Organization != "" && runevent.Repository != "" {
		projectSlug := filepath.Join(runevent.Organization, runevent.Repository)
		projectinfo, _, err := v.Client.Projects.GetProject(projectSlug, &gitlab.GetProjectOptions{})
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
	v.run = run
	v.eventEmitter = eventsEmitter
	v.repo = repo

	return nil
}

func (v *Provider) CreateStatus(_ context.Context, event *info.Event, statusOpts provider.StatusOpts,
) error {
	var detailsURL string
	if v.Client == nil {
		return fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}
	switch statusOpts.Conclusion {
	case "skipped":
		statusOpts.Conclusion = "canceled"
		statusOpts.Title = "skipped validating this commit"
	case "neutral":
		statusOpts.Conclusion = "canceled"
		statusOpts.Title = "stopped"
	case "failure":
		statusOpts.Conclusion = "failed"
		statusOpts.Title = "failed"
	case "success":
		statusOpts.Conclusion = "success"
		statusOpts.Title = "successfully validated your commit"
	case "completed":
		statusOpts.Conclusion = "success"
		statusOpts.Title = "completed"
	case "pending":
		statusOpts.Conclusion = "running"
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

	opt := &gitlab.SetCommitStatusOptions{
		State:       gitlab.BuildStateValue(statusOpts.Conclusion),
		Name:        gitlab.Ptr(v.pacInfo.ApplicationName),
		TargetURL:   gitlab.Ptr(detailsURL),
		Description: gitlab.Ptr(statusOpts.Title),
	}

	// In case we have access, set the status. Typically, on a Merge Request (MR)
	// from a fork in an upstream repository, the token needs to have write access
	// to the fork repository in order to create a status. However, the token set on the
	// Repository CR usually doesn't have such broad access, preventing from creating
	// a status comment on it.
	// This would work on a push or an MR from a branch within the same repo.
	// Ignoring errors because of the write access issues,
	if _, _, err := v.Client.Commits.SetCommitStatus(event.SourceProjectID, event.SHA, opt); err != nil {
		v.eventEmitter.EmitMessage(v.repo, zap.ErrorLevel, "FailedToSetCommitStatus",
			"cannot set status with the GitLab token because of: "+err.Error())
	}

	// only add a note when we are on a MR
	if event.EventType == triggertype.PullRequest.String() ||
		event.EventType == "Merge_Request" || event.EventType == "Merge Request" ||
		opscomments.IsAnyOpsEventType(event.EventType) {
		mopt := &gitlab.CreateMergeRequestNoteOptions{Body: gitlab.Ptr(body)}
		_, _, err := v.Client.Notes.CreateMergeRequestNote(event.TargetProjectID, event.PullRequestNumber, mopt)
		return err
	}
	return nil
}

func (v *Provider) GetTektonDir(_ context.Context, event *info.Event, path, provenance string) (string, error) {
	if v.Client == nil {
		return "", fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}
	// default set provenance from head
	revision := event.HeadBranch
	if provenance == "default_branch" {
		revision = event.DefaultBranch
		v.Logger.Infof("Using PipelineRun definition from default_branch: %s", event.DefaultBranch)
	} else {
		v.Logger.Infof("Using PipelineRun definition from source merge request SHA: %s", event.SHA)
	}

	opt := &gitlab.ListTreeOptions{
		Path:      gitlab.Ptr(path),
		Ref:       gitlab.Ptr(revision),
		Recursive: gitlab.Ptr(true),
	}

	objects, resp, err := v.Client.Repositories.ListTree(v.sourceProjectID, opt)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to list %s dir: %w", path, err)
	}

	return v.concatAllYamlFiles(objects, event)
}

// concatAllYamlFiles concat all yaml files from a directory as one big multi document yaml string.
func (v *Provider) concatAllYamlFiles(objects []*gitlab.TreeNode, runevent *info.Event) (string, error) {
	var allTemplates string
	for _, value := range objects {
		if strings.HasSuffix(value.Name, ".yaml") ||
			strings.HasSuffix(value.Name, ".yml") {
			data, err := v.getObject(value.Path, runevent.HeadBranch, v.sourceProjectID)
			if err != nil {
				return "", err
			}
			// validate yaml
			var i any
			if err := yaml.Unmarshal(data, &i); err != nil {
				return "", fmt.Errorf("error unmarshalling yaml file %s: %w", value.Path, err)
			}
			if allTemplates != "" && !strings.HasPrefix(string(data), "---") {
				allTemplates += "---"
			}
			allTemplates += "\n" + string(data) + "\n"
		}
	}

	return allTemplates, nil
}

func (v *Provider) getObject(fname, branch string, pid int) ([]byte, error) {
	opt := &gitlab.GetRawFileOptions{
		Ref: gitlab.Ptr(branch),
	}
	file, resp, err := v.Client.RepositoryFiles.GetRawFile(pid, fname, opt)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to get filename from api %s dir: %w", fname, err)
	}
	if resp != nil && resp.Response.StatusCode == http.StatusNotFound {
		return []byte{}, nil
	}
	return file, nil
}

func (v *Provider) GetFileInsideRepo(_ context.Context, runevent *info.Event, path, _ string) (string, error) {
	getobj, err := v.getObject(path, runevent.HeadBranch, v.sourceProjectID)
	if err != nil {
		return "", err
	}
	return string(getobj), nil
}

func (v *Provider) GetCommitInfo(_ context.Context, runevent *info.Event) error {
	if v.Client == nil {
		return fmt.Errorf("%s", noClientErrStr)
	}

	// if we don't have a SHA (ie: incoming-webhook) then get it from the branch
	// and populate in the runevent.
	if runevent.SHA == "" && runevent.HeadBranch != "" {
		branchinfo, _, err := v.Client.Commits.GetCommit(v.sourceProjectID, runevent.HeadBranch)
		if err != nil {
			return err
		}
		runevent.SHA = branchinfo.ID
		runevent.SHATitle = branchinfo.Title
		runevent.SHAURL = branchinfo.WebURL
	}

	return nil
}

func (v *Provider) GetFiles(_ context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	if v.Client == nil {
		return changedfiles.ChangedFiles{}, fmt.Errorf("no gitlab client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}
	if runevent.TriggerTarget == triggertype.PullRequest {
		mrchanges, _, err := v.Client.MergeRequests.ListMergeRequestDiffs(v.targetProjectID, runevent.PullRequestNumber, &gitlab.ListMergeRequestDiffsOptions{})
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}

		changedFiles := changedfiles.ChangedFiles{}
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
		return changedFiles, nil
	}

	if runevent.TriggerTarget == "push" {
		pushChanges, _, err := v.Client.Commits.GetCommitDiff(v.sourceProjectID, runevent.SHA, &gitlab.GetCommitDiffOptions{})
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}
		changedFiles := changedfiles.ChangedFiles{}
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
		return changedFiles, nil
	}
	return changedfiles.ChangedFiles{}, nil
}

func (v *Provider) CreateToken(_ context.Context, _ []string, _ *info.Event) (string, error) {
	return "", nil
}
