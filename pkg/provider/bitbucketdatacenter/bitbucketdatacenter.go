package bitbucketdatacenter

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v81/github"
	"github.com/jenkins-x/go-scm/scm"
	"github.com/jenkins-x/go-scm/scm/driver/stash"
	"github.com/jenkins-x/go-scm/scm/transport/oauth2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerMetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/providermetrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.uber.org/zap"
)

const taskStatusTemplate = `{{range $taskrun := .TaskRunList }}| **{{ formatCondition $taskrun.PipelineRunTaskRunStatus.Status.Conditions }}** | {{ $taskrun.ConsoleLogURL }} | *{{ formatDuration $taskrun.Status.StartTime $taskrun.Status.CompletionTime }}* |
{{ end }}`
const apiResponseLimit = 100

var _ provider.Interface = (*Provider)(nil)

type Provider struct {
	client                    *scm.Client
	Logger                    *zap.SugaredLogger
	run                       *params.Run
	pacInfo                   *info.PacOpts
	baseURL                   string
	defaultBranchLatestCommit string
	pullRequestNumber         int
	apiURL                    string
	provenance                string
	projectKey                string
	repo                      *v1alpha1.Repository
	triggerEvent              string
	cachedChangedFiles        *changedfiles.ChangedFiles
}

func (v Provider) Client() *scm.Client {
	providerMetrics.RecordAPIUsage(
		v.Logger,
		v.GetConfig().Name,
		v.triggerEvent,
		v.repo,
	)
	return v.client
}

func (v *Provider) CreateComment(_ context.Context, _ *info.Event, _, _ string) error {
	return nil
}

func (v *Provider) SetPacInfo(pacInfo *info.PacOpts) {
	v.pacInfo = pacInfo
}

func (v *Provider) CheckPolicyAllowing(_ context.Context, _ *info.Event, _ []string) (bool, string) {
	return false, ""
}

// GetTaskURI TODO: Implement ME.
func (v *Provider) GetTaskURI(_ context.Context, _ *info.Event, _ string) (bool, string, error) {
	return false, "", nil
}

func (v *Provider) SetLogger(logger *zap.SugaredLogger) {
	v.Logger = logger
}

func (v *Provider) Validate(_ context.Context, _ *params.Run, event *info.Event) error {
	signature := event.Request.Header.Get("X-Hub-Signature")
	if event.Provider.WebhookSecret == "" && signature != "" {
		return fmt.Errorf("bitbucket-datacenter failed validation: failed to find webhook secret")
	}
	return github.ValidateSignature(signature, event.Request.Payload, []byte(event.Provider.WebhookSecret))
}

// sanitizeTitle make sure we only get the tile by remove everything after \n.
func sanitizeTitle(s string) string {
	return strings.Split(s, "\n")[0]
}

func (v *Provider) CreateStatus(ctx context.Context, event *info.Event, statusOpts status.StatusOpts) error {
	detailsURL := event.Provider.URL
	state := scm.StateUnknown

	switch statusOpts.Conclusion {
	case status.ConclusionSkipped:
		state = scm.StateFailure
		statusOpts.Title = "➖ Skipping this commit"
	case status.ConclusionNeutral:
		state = scm.StateFailure
		statusOpts.Title = "➖ CI has stopped"
	case status.ConclusionFailure:
		state = scm.StateFailure
		statusOpts.Title = "❌ Failed"
	case status.ConclusionPending:
		if statusOpts.Status == "queued" {
			state = scm.StateUnknown
		} else {
			// TODO: Should this be scm.StateRunning?
			state = scm.StatePending
			statusOpts.Title = "⚡ CI has started"
		}
	case status.ConclusionSuccess:
		state = scm.StateSuccess
		statusOpts.Title = "Commit has been validated"
	case status.ConclusionCompleted:
		state = scm.StateSuccess
		statusOpts.Title = "Completed"
	case status.ConclusionCancelled:
		// TODO
	}
	if statusOpts.DetailsURL != "" {
		detailsURL = statusOpts.DetailsURL
	}
	if v.client == nil {
		return fmt.Errorf("no token has been set, cannot set status")
	}

	key := statusOpts.PipelineRunName
	if key == "" {
		key = statusOpts.Title
	}

	if v.pacInfo.ApplicationName != "" {
		key = fmt.Sprintf("%s / %s", v.pacInfo.ApplicationName, key)
	}

	OrgAndRepo := fmt.Sprintf("%s/%s", event.Organization, event.Repository)
	opts := &scm.StatusInput{
		State: state,
		Label: key,
		Desc:  statusOpts.Text,
		Link:  detailsURL,
	}
	_, _, err := v.Client().Repositories.CreateStatus(ctx, OrgAndRepo, event.SHA, opts)
	if err != nil {
		return err
	}

	onPr := ""
	if statusOpts.OriginalPipelineRunName != "" {
		onPr = "/" + statusOpts.OriginalPipelineRunName
	}
	bbComment := fmt.Sprintf("**%s%s** - %s\n\n%s", v.pacInfo.ApplicationName, onPr, statusOpts.Title, statusOpts.Text)

	if state == scm.StateSuccess && statusOpts.Status == "completed" &&
		statusOpts.Text != "" && event.TriggerTarget == triggertype.PullRequest && event.PullRequestNumber > 0 {
		input := &scm.CommentInput{
			Body: bbComment,
		}
		_, _, err := v.Client().PullRequests.CreateComment(ctx, OrgAndRepo, event.PullRequestNumber, input)
		if err != nil {
			return err
		}
	}

	// TODO: Completed status
	return nil
}

func (v *Provider) GetCommitStatuses(_ context.Context, _ *info.Event) ([]provider.CommitStatusInfo, error) {
	return nil, nil
}

func (v *Provider) concatAllYamlFiles(ctx context.Context, objects []string, sha string, runevent *info.Event) (string, error) {
	var allTemplates string
	for _, value := range objects {
		if strings.HasSuffix(value, ".yaml") ||
			strings.HasSuffix(value, ".yml") {
			// if sha is empty string then it fetches raw file from
			// default branch which we can use for PAC provenance.
			data, err := v.getRaw(ctx, runevent, sha, value)
			if err != nil {
				return "", err
			}

			if err := provider.ValidateYaml([]byte(data), value); err != nil {
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

func (v *Provider) getRaw(ctx context.Context, runevent *info.Event, revision, path string) (string, error) {
	repo := fmt.Sprintf("%s/%s", runevent.Organization, runevent.Repository)
	content, _, err := v.Client().Contents.Find(ctx, repo, path, revision)
	if err != nil {
		return "", fmt.Errorf("cannot find %s inside the %s repository: %w", path, runevent.Repository, err)
	}
	return string(content.Data), nil
}

func (v *Provider) GetTektonDir(ctx context.Context, event *info.Event, path, provenance string) (string, error) {
	v.provenance = provenance
	// If "at" is empty string "" then default branch will be used as source
	at := ""
	if v.provenance == "source" {
		at = event.SHA
		v.Logger.Infof("Using PipelineRun definition from source %s commit SHA: %s", event.TriggerTarget.String(), event.SHA)
	} else {
		v.Logger.Infof("Using PipelineRun definition from default_branch: %s", event.DefaultBranch)
	}

	orgAndRepo := fmt.Sprintf("%s/%s", event.Organization, event.Repository)
	var fileEntries []*scm.FileEntry
	opts := &scm.ListOptions{Page: 1, Size: apiResponseLimit}
	for {
		entries, _, err := v.Client().Contents.List(ctx, orgAndRepo, path, at, opts)
		if err != nil {
			return "", fmt.Errorf("cannot list content of %s directory: %w", path, err)
		}
		fileEntries = append(fileEntries, entries...)

		if len(entries) < apiResponseLimit {
			break
		}

		opts.Page++
	}

	fpathTmpl := []string{}
	for _, e := range fileEntries {
		fpathTmpl = append(fpathTmpl, filepath.Join(path, e.Path))
	}

	return v.concatAllYamlFiles(ctx, fpathTmpl, at, event)
}

func (v *Provider) GetFileInsideRepo(ctx context.Context, event *info.Event, path, targetBranch string) (string, error) {
	branch := event.SHA
	// TODO: this may be buggy? we need to figure out how to get the fromSource ref
	if targetBranch == event.DefaultBranch {
		branch = v.defaultBranchLatestCommit
	}

	ret, err := v.getRaw(ctx, event, branch, path)
	return ret, err
}

func removeLastSegment(urlStr string) string {
	u, _ := url.Parse(urlStr)
	segments := strings.Split(u.Path, "/")
	switch {
	case len(segments) > 1:
		segments = segments[:len(segments)-1]
	case (len(segments) == 1 && segments[0] != "") || u.Path == "/":
		segments = []string{""}
	}

	newPath := strings.Join(segments, "/")
	if newPath == "" && strings.HasPrefix(u.Path, "/") {
		newPath = "/" // Ensure root path is correctly represented as "/"
	}

	u.Path = newPath
	return u.String()
}

func (v *Provider) SetClient(ctx context.Context, run *params.Run, event *info.Event, repo *v1alpha1.Repository, _ *events.EventEmitter) error {
	if event.Provider.User == "" {
		return fmt.Errorf("no spec.git_provider.user has been set in the repo crd")
	}
	if event.Provider.Token == "" {
		return fmt.Errorf("no spec.git_provider.secret has been set in the repo crd")
	}
	if event.Provider.URL == "" {
		return fmt.Errorf("no spec.git_provider.url has been set in the repo crd")
	}

	// make sure we have /rest at the end of the url
	if !strings.HasSuffix(event.Provider.URL, "/rest") {
		event.Provider.URL += "/rest"
	}

	// make sure we strip slashes from the end of the URL
	event.Provider.URL = strings.TrimSuffix(event.Provider.URL, "/")
	v.apiURL = event.Provider.URL

	if v.client == nil {
		client, err := stash.New(removeLastSegment(event.Provider.URL)) // remove `/rest` from url
		if err != nil {
			return err
		}
		client.Client = &http.Client{
			Transport: &oauth2.Transport{
				Source: oauth2.StaticTokenSource(
					&scm.Token{
						Token: event.Provider.Token,
					},
				),
			},
		}
		v.client = client

		// Added for security audit purposes to log client access when a token is used
		run.Clients.Log.Infof("bitbucket-datacenter: initialized client with provided token for user=%s providerURL=%s", event.Provider.User, event.Provider.URL)
	}
	v.run = run
	v.repo = repo
	v.triggerEvent = event.EventType
	_, resp, err := v.Client().Users.FindLogin(ctx, event.Provider.User)
	if resp != nil && resp.Status == http.StatusUnauthorized {
		return fmt.Errorf("cannot get user %s with token: %w", event.Provider.User, err)
	}
	if err != nil {
		return fmt.Errorf("cannot get user %s: %w", event.Provider.User, err)
	}

	return nil
}

func (v *Provider) GetCommitInfo(_ context.Context, event *info.Event) error {
	OrgAndRepo := fmt.Sprintf("%s/%s", event.Organization, event.Repository)
	commit, _, err := v.Client().Git.FindCommit(context.Background(), OrgAndRepo, event.SHA)
	if err != nil {
		return err
	}
	event.SHATitle = sanitizeTitle(commit.Message)
	event.SHAURL = fmt.Sprintf("%s/projects/%s/repos/%s/commits/%s", v.baseURL, v.projectKey, event.Repository, event.SHA)
	event.HasSkipCommand = provider.SkipCI(commit.Message)

	// Populate full commit information for LLM context
	event.SHAMessage = commit.Message
	event.SHAAuthorName = commit.Author.Name
	event.SHAAuthorEmail = commit.Author.Email
	if !commit.Author.Date.IsZero() {
		event.SHAAuthorDate = commit.Author.Date
	}
	event.SHACommitterName = commit.Committer.Name
	event.SHACommitterEmail = commit.Committer.Email
	if !commit.Committer.Date.IsZero() {
		event.SHACommitterDate = commit.Committer.Date
	}

	ref, _, err := v.Client().Git.GetDefaultBranch(context.Background(), OrgAndRepo)
	if err != nil {
		return err
	}

	v.defaultBranchLatestCommit = ref.Sha
	event.DefaultBranch = ref.Name
	return nil
}

func (v *Provider) GetConfig() *info.ProviderConfig {
	return &info.ProviderConfig{
		TaskStatusTMPL: taskStatusTemplate,
		Name:           "bitbucket-datacenter",
	}
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

func (v *Provider) fetchChangedFiles(ctx context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	changedFiles := changedfiles.ChangedFiles{}

	orgAndRepo := fmt.Sprintf("%s/%s", runevent.Organization, runevent.Repository)

	switch runevent.TriggerTarget {
	case triggertype.PullRequest:
		opts := &scm.ListOptions{Page: 1, Size: apiResponseLimit}
		for {
			changes, _, err := v.Client().PullRequests.ListChanges(ctx, orgAndRepo, runevent.PullRequestNumber, opts)
			if err != nil {
				return changedfiles.ChangedFiles{}, fmt.Errorf("failed to list changes for pull request: %w", err)
			}

			for _, c := range changes {
				changedFiles.All = append(changedFiles.All, c.Path)
				if c.Added {
					changedFiles.Added = append(changedFiles.Added, c.Path)
				}
				if c.Modified {
					changedFiles.Modified = append(changedFiles.Modified, c.Path)
				}
				if c.Renamed {
					changedFiles.Renamed = append(changedFiles.Renamed, c.Path)
				}
				if c.Deleted {
					changedFiles.Deleted = append(changedFiles.Deleted, c.Path)
				}
			}

			// In the Jenkins-x/go-scm package, the `isLastPage` field is not available, and the value of
			// `response.Page.Last` is set to `0`. Therefore, to determine if there are more items to fetch,
			// we can check if the length of the currently fetched items is less than the specified limit.
			// If the length is less than the limit, it indicates that there are no more items to retrieve.
			if len(changes) < apiResponseLimit {
				break
			}

			opts.Page++
		}
	case triggertype.Push:
		opts := &scm.ListOptions{Page: 1, Size: apiResponseLimit}
		for {
			changes, _, err := v.Client().Git.ListChanges(ctx, orgAndRepo, runevent.SHA, opts)
			if err != nil {
				return changedfiles.ChangedFiles{}, fmt.Errorf("failed to list changes for commit %s: %w", runevent.SHA, err)
			}

			for _, c := range changes {
				changedFiles.All = append(changedFiles.All, c.Path)
				if c.Added {
					changedFiles.Added = append(changedFiles.Added, c.Path)
				}
				if c.Modified {
					changedFiles.Modified = append(changedFiles.Modified, c.Path)
				}
				if c.Renamed {
					changedFiles.Renamed = append(changedFiles.Renamed, c.Path)
				}
				if c.Deleted {
					changedFiles.Deleted = append(changedFiles.Deleted, c.Path)
				}
			}

			if len(changes) < apiResponseLimit {
				break
			}

			opts.Page++
		}
	default:
		// No action necessary
	}
	return changedFiles, nil
}

func (v *Provider) CreateToken(_ context.Context, _ []string, _ *info.Event) (string, error) {
	return "", nil
}

func (v *Provider) GetTemplate(commentType provider.CommentType) string {
	return provider.GetMarkdownTemplate(commentType)
}
